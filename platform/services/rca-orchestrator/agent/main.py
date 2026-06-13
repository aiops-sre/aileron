from __future__ import annotations
import asyncio
import json
import logging
import os
import uuid
from contextlib import asynccontextmanager
from typing import Any

import aiohttp
import asyncpg
from fastapi import FastAPI, WebSocket, WebSocketDisconnect, HTTPException, BackgroundTasks
from fastapi.middleware.cors import CORSMiddleware
from aiokafka import AIOKafkaConsumer

from models.schemas import (
    Investigation, InvestigationPhase, StartInvestigationRequest,
    FeedbackRequest, KnowledgeEntry, StreamEvent,
)
from agent.orchestrator import RCAOrchestrator
from agent.orchestrator_v2 import RCAOrchestratorV2
from learning.knowledge_store import KnowledgeStore
from learning.continuous import ingest_kafka_incidents, build_and_update_model

log = logging.getLogger(__name__)
logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")

KAFKA_BROKERS = os.getenv("KAFKA_BROKERS", "alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092")
KAFKA_TOPIC_ALERTS = os.getenv("KAFKA_TOPIC_ALERTS", "raw-alerts")
PG_DSN = os.getenv("POSTGRES_URL", "postgresql://alerthub:@postgres-primary.aileron.svc.cluster.local:5432/alerthub")
BACKEND_URL = os.getenv("BACKEND_URL", "http://alerthub-backend.aileron.svc.cluster.local:3000")
INTERNAL_SERVICE_TOKEN = os.getenv("INTERNAL_SERVICE_TOKEN", "")

# RCA_V2=true enables the deterministic V2 orchestrator. V1 remains the fallback.
RCA_V2_ENABLED = os.getenv("RCA_V2", "true").lower() in ("true", "1", "yes")

# In-memory investigation store (backed by postgres for persistence across restarts)
investigations: dict[str, Investigation] = {}
# Per-investigation list of per-client queues. Each WS client gets its own queue so that
# multiple simultaneous viewers receive all events (broadcast fan-out). Using a list instead
# of a single shared queue prevents the alternating-event split-delivery bug.
stream_queues: dict[str, list[asyncio.Queue]] = {}

knowledge_store = KnowledgeStore()
orchestrator = RCAOrchestrator(knowledge_store=knowledge_store)
orchestrator_v2 = RCAOrchestratorV2(knowledge_store=knowledge_store)


# ─── Postgres Persistence ─────────────────────────────────────────────────────

async def _ensure_investigations_table() -> None:
    try:
        conn = await asyncpg.connect(PG_DSN, timeout=10)
        await conn.execute("""
            CREATE TABLE IF NOT EXISTS rca_investigations (
                id TEXT PRIMARY KEY,
                incident_id TEXT,
                data JSONB NOT NULL,
                created_at TIMESTAMPTZ DEFAULT NOW(),
                updated_at TIMESTAMPTZ DEFAULT NOW()
            )
        """)
        # Add incident_id column if this table already existed without it.
        await conn.execute("""
            ALTER TABLE rca_investigations ADD COLUMN IF NOT EXISTS incident_id TEXT
        """)
        await conn.close()
        log.info("rca_investigations table ready")
    except Exception as e:
        log.warning(f"Could not ensure rca_investigations table: {e}")


async def _load_investigations() -> None:
    try:
        conn = await asyncpg.connect(PG_DSN, timeout=10)
        rows = await conn.fetch(
            "SELECT id, data FROM rca_investigations ORDER BY updated_at DESC LIMIT 500"
        )
        await conn.close()
        for row in rows:
            try:
                raw = row["data"] if isinstance(row["data"], dict) else json.loads(row["data"])
                inv = Investigation(**raw)
                investigations[inv.id] = inv
            except Exception as e:
                log.warning(f"Failed to deserialize investigation {row['id']}: {e}")
        log.info(f"Loaded {len(rows)} investigations from postgres")
    except Exception as e:
        log.warning(f"Could not load investigations from postgres: {e}")


async def _persist_investigation(inv: Investigation) -> None:
    try:
        conn = await asyncpg.connect(PG_DSN, timeout=10)
        # Also populate the incident_id column so backend JOIN queries can find it.
        await conn.execute("""
            INSERT INTO rca_investigations (id, incident_id, data, updated_at)
            VALUES ($1, $2, $3::jsonb, NOW())
            ON CONFLICT (id) DO UPDATE
            SET incident_id = EXCLUDED.incident_id,
                data        = EXCLUDED.data,
                updated_at  = NOW()
        """, inv.id, inv.incident_id, inv.json())
        await conn.close()
    except Exception as e:
        log.warning(f"Could not persist investigation {inv.id}: {e}")


async def _sync_postgres_incidents_to_weaviate() -> None:
    """On startup, backfill resolved postgres incidents into Weaviate for RAG context."""
    if not knowledge_store.client:
        log.info("Weaviate not available — skipping postgres incident backfill")
        return
    try:
        conn = await asyncpg.connect(PG_DSN, timeout=10)
        rows = await conn.fetch("""
            SELECT id, title, severity, ai_root_cause, description, source, created_at
            FROM incidents
            WHERE status = 'resolved'
              AND ai_root_cause IS NOT NULL
              AND ai_root_cause != ''
            ORDER BY created_at DESC
            LIMIT 200
        """)
        await conn.close()
    except Exception as e:
        log.warning(f"Could not query postgres for incident backfill: {e}")
        return

    from models.schemas import Investigation as Inv, InvestigationPhase, RootCause
    synced = 0
    for row in rows:
        try:
            inv = Inv(
                id=str(row["id"]),
                alert_id=str(row["id"]),
                alert_title=row["title"],
                alert_body={"description": row["description"] or ""},
                severity=row["severity"] or "medium",
                phase=InvestigationPhase.completed,
                confirmed=True,
            )
            inv.root_cause = RootCause(
                summary=row["ai_root_cause"],
                component=row.get("source") or "unknown",
                category="auto_ingested",
                confidence=0.7,
                evidence=[],
            )
            await knowledge_store.store_investigation(inv)
            synced += 1
        except Exception as e:
            log.debug(f"Skipped incident {row['id']} during Weaviate sync: {e}")

    log.info(f"Synced {synced}/{len(rows)} resolved incidents to Weaviate")


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Run Weaviate + postgres init in background so the health probe
    # passes immediately and the liveness check doesn't kill the pod.
    asyncio.create_task(_startup_init())
    yield
    log.info("RCA Orchestrator shutting down")


async def _startup_init():
    try:
        knowledge_store.connect()
    except Exception as e:
        log.warning(f"Weaviate connection failed (RAG disabled): {e}")
    await _ensure_investigations_table()
    await _load_investigations()
    asyncio.create_task(_resume_stalled_investigations())
    asyncio.create_task(_sync_postgres_incidents_to_weaviate())
    asyncio.create_task(_kafka_alert_consumer())
    asyncio.create_task(_kafka_learning_consumer())
    asyncio.create_task(_weekly_model_trainer())
    asyncio.create_task(_warm_up_ollama())
    # Bootstrap Weaviate from historical rca_investigations on first startup.
    # Runs async so it doesn't block liveness; safe to re-run (inserts are additive).
    asyncio.create_task(_bootstrap_weaviate_knowledge())


async def _bootstrap_weaviate_knowledge():
    """Seed Weaviate knowledge store from historical rca_investigations on startup."""
    import os as _os
    pg_dsn = _os.getenv(
        "POSTGRES_URL",
        "postgresql://alerthub:@postgres-primary.aileron.svc.cluster.local:5432/alerthub",
    )
    try:
        imported = await knowledge_store.bootstrap_from_history(pg_dsn, limit=500)
        if imported > 0:
            log.info(f"Weaviate bootstrap complete: {imported} historical investigations loaded")
    except Exception as e:
        log.warning(f"Weaviate bootstrap failed (non-fatal): {e}")


async def _warm_up_ollama():
    """Send a minimal prompt to load the model into RAM before the first real request."""
    import aiohttp as _aiohttp
    OLLAMA_URL = os.getenv("OLLAMA_URL", "http://ollama.aileron.svc.cluster.local:11434")
    OLLAMA_MODEL = os.getenv("OLLAMA_MODEL", "qwen2.5:3b")
    try:
        async with _aiohttp.ClientSession() as session:
            async with session.post(
                f"{OLLAMA_URL}/api/chat",
                json={"model": OLLAMA_MODEL, "messages": [{"role": "user", "content": "ping"}],
                      "stream": False, "keep_alive": "10m", "options": {"num_predict": 1}},
                timeout=_aiohttp.ClientTimeout(total=300),
            ) as resp:
                if resp.status == 200:
                    log.info(f"✅ Ollama warm-up complete (model={OLLAMA_MODEL})")
                else:
                    log.warning(f"Ollama warm-up non-200: {resp.status}")
    except Exception as e:
        log.warning(f"Ollama warm-up failed (will retry on first request): {e}")


async def _resume_stalled_investigations():
    """Re-run any investigations that were queued/investigating when the pod last restarted."""
    stalled_phases = {InvestigationPhase.queued, InvestigationPhase.context_gathering,
                      InvestigationPhase.hypothesis_formation, InvestigationPhase.evidence_collection,
                      InvestigationPhase.root_cause_analysis, InvestigationPhase.remediation_planning}
    stalled = [inv for inv in investigations.values() if inv.phase in stalled_phases]
    if not stalled:
        return
    log.info(f"Resuming {len(stalled)} stalled investigation(s) from previous pod lifecycle")
    for inv in stalled:
        # Reset to queued so the agent loop starts fresh
        inv.phase = InvestigationPhase.queued
        if inv.id not in stream_queues:
            stream_queues[inv.id] = []
        asyncio.create_task(_run_investigation(inv))
        log.info(f"Re-queued stalled investigation {inv.id}: {inv.alert_title}")


app = FastAPI(title="RCA Orchestrator", version="1.0.0", lifespan=lifespan)
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_methods=["*"], allow_headers=["*"])


# ─── REST Endpoints ───────────────────────────────────────────────────────────

@app.get("/health")
async def health():
    return {"status": "ok", "model": os.getenv("OLLAMA_MODEL", "qwen2.5:14b")}


@app.post("/api/v1/investigations", response_model=dict)
async def start_investigation(req: StartInvestigationRequest, background: BackgroundTasks):
    # Merge go_context into alert_body so both V1 and V2 can access it without schema changes.
    alert_body = dict(req.alert_body)
    if req.go_context:
        alert_body["go_context"] = req.go_context

    inv = Investigation(
        alert_id=req.alert_id,
        alert_title=req.alert_title,
        alert_body=alert_body,
        severity=req.severity,
        incident_id=req.incident_id,
        namespace=req.namespace,
        cluster=req.cluster,
        service=req.service,
        llm_provider=req.llm_provider,
        llm_model=req.llm_model,
        llm_token=req.llm_token,
        orchestrator_version="v2" if RCA_V2_ENABLED else "v1",
    )
    investigations[inv.id] = inv
    stream_queues[inv.id] = []
    asyncio.create_task(_persist_investigation(inv))
    background.add_task(_run_investigation, inv)
    log.info(f"Started investigation {inv.id} (orchestrator={'v2' if RCA_V2_ENABLED else 'v1'}, "
             f"go_context={'yes' if req.go_context else 'no'})")
    return {"investigation_id": inv.id, "status": "started"}


@app.get("/api/v1/investigations/{inv_id}")
async def get_investigation(inv_id: str):
    inv = investigations.get(inv_id)
    if inv:
        return inv.dict()
    # Fallback: load from DB (handles pod restarts where in-memory dict was cleared)
    try:
        conn = await asyncpg.connect(PG_DSN, timeout=10)
        row = await conn.fetchrow("SELECT data FROM rca_investigations WHERE id = $1", inv_id)
        await conn.close()
        if row:
            raw = row["data"] if isinstance(row["data"], dict) else json.loads(row["data"])
            inv = Investigation(**raw)
            investigations[inv.id] = inv  # re-cache in memory
            return inv.dict()
    except Exception as e:
        log.warning(f"DB fallback for investigation {inv_id} failed: {e}")
    raise HTTPException(404, "Investigation not found")


@app.get("/api/v1/investigations")
async def list_investigations(limit: int = 20):
    all_invs = sorted(investigations.values(), key=lambda i: i.started_at, reverse=True)
    return [i.dict() for i in all_invs[:limit]]


@app.post("/api/v1/investigations/{inv_id}/feedback")
async def submit_feedback(inv_id: str, req: FeedbackRequest):
    inv = investigations.get(inv_id)
    if not inv:
        raise HTTPException(404, "Investigation not found")
    inv.feedback_score = req.score
    inv.confirmed = req.confirmed
    if req.correct_root_cause and inv.root_cause:
        inv.root_cause.summary = req.correct_root_cause
    if req.correct_component and inv.root_cause:
        inv.root_cause.component = req.correct_component
    await knowledge_store.update_feedback(
        inv_id, req.score, req.confirmed, req.correct_root_cause or ""
    )
    asyncio.create_task(_persist_investigation(inv))
    return {"status": "feedback_stored", "investigation_id": inv_id}


@app.get("/api/v1/investigations/{inv_id}/similar")
async def get_similar(inv_id: str):
    inv = investigations.get(inv_id)
    if not inv:
        raise HTTPException(404, "Investigation not found")
    similar = await knowledge_store.search_similar(inv.alert_title, inv.alert_body, limit=5)
    return {"similar": similar}


@app.post("/api/v1/knowledge")
async def add_knowledge(entry: KnowledgeEntry):
    stored_id = await knowledge_store.store_knowledge(entry)
    return {"status": "stored", "id": stored_id}


@app.get("/api/v1/knowledge/search")
async def search_knowledge(q: str, limit: int = 5):
    results = await knowledge_store.search_knowledge(q, limit=limit)
    return {"results": results}


@app.post("/api/v1/knowledge/sync")
async def sync_incidents_to_weaviate(background: BackgroundTasks):
    """Manually trigger a sync of resolved postgres incidents into Weaviate."""
    background.add_task(_sync_postgres_incidents_to_weaviate)
    return {"status": "sync_started"}


@app.post("/api/v1/forecast/{inv_id}")
async def forecast_escalation(inv_id: str):
    inv = investigations.get(inv_id)
    if not inv:
        raise HTTPException(404, "Investigation not found")
    from tools.postgres_tool import GetAlertFrequencyTool
    freq_data_raw, _ = await GetAlertFrequencyTool().run(title_keyword=inv.alert_title.split()[0])
    try:
        freq_data = json.loads(freq_data_raw)
    except Exception:
        freq_data = []
    forecast = await orchestrator.forecast(inv.alert_title, freq_data)
    return {"forecast": forecast, "based_on_alert": inv.alert_title}


@app.post("/api/v1/model/train")
async def trigger_training(background: BackgroundTasks):
    background.add_task(build_and_update_model, knowledge_store)
    return {"status": "training_started"}


@app.get("/api/v1/model/info")
async def model_info():
    import aiohttp
    OLLAMA_URL = os.getenv("OLLAMA_URL", "http://ollama.aileron.svc.cluster.local:11434")
    async with aiohttp.ClientSession() as session:
        async with session.get(f"{OLLAMA_URL}/api/tags", timeout=aiohttp.ClientTimeout(total=5)) as resp:
            tags = await resp.json() if resp.status == 200 else {}
    return {"current_model": os.getenv("OLLAMA_MODEL"), "available_models": tags.get("models", [])}


# ─── WebSocket Stream ─────────────────────────────────────────────────────────

@app.websocket("/ws/investigations/{inv_id}")
async def investigation_stream(websocket: WebSocket, inv_id: str):
    await websocket.accept()
    inv = investigations.get(inv_id)
    if not inv:
        await websocket.close(code=1008, reason="Investigation not found")
        return

    # Each client gets a dedicated queue so all simultaneous viewers receive every event.
    personal_queue: asyncio.Queue = asyncio.Queue(maxsize=500)
    if inv_id not in stream_queues:
        stream_queues[inv_id] = []
    stream_queues[inv_id].append(personal_queue)

    try:
        while True:
            try:
                event: StreamEvent = await asyncio.wait_for(personal_queue.get(), timeout=30)
                await websocket.send_text(event.json())
                if event.type in ("result", "error"):
                    break
            except asyncio.TimeoutError:
                await websocket.send_text(json.dumps({"type": "heartbeat"}))
    except WebSocketDisconnect:
        pass
    finally:
        try:
            stream_queues[inv_id].remove(personal_queue)
        except (ValueError, KeyError):
            pass


# ─── Background Tasks ─────────────────────────────────────────────────────────

INVESTIGATION_TIMEOUT_SECONDS = int(os.getenv("INVESTIGATION_TIMEOUT", "900"))


async def _run_investigation(inv: Investigation):
    def emit(event: StreamEvent):
        # Broadcast to every connected client's personal queue. Drop if full rather than
        # blocking the investigation coroutine (R-1 fix: use put_nowait instead of put).
        for q in list(stream_queues.get(inv.id) or []):
            try:
                q.put_nowait(event)
            except asyncio.QueueFull:
                log.warning(f"Stream queue full for investigation {inv.id} — dropping event type={event.type}")

    use_v2 = RCA_V2_ENABLED or inv.orchestrator_version == "v2"

    try:
        if use_v2:
            await asyncio.wait_for(
                orchestrator_v2.run(inv, emit),
                timeout=INVESTIGATION_TIMEOUT_SECONDS,
            )
        else:
            await asyncio.wait_for(
                orchestrator.run_agent_loop(inv, emit),
                timeout=INVESTIGATION_TIMEOUT_SECONDS,
            )
    except asyncio.TimeoutError:
        log.warning(f"Investigation {inv.id} timed out after {INVESTIGATION_TIMEOUT_SECONDS}s — marking failed")
        inv.phase = InvestigationPhase.failed
        emit(StreamEvent(investigation_id=inv.id, type="error", phase=inv.phase,
                         data=f"Investigation timed out after {INVESTIGATION_TIMEOUT_SECONDS}s"))
    except Exception as e:
        log.exception(f"Investigation {inv.id} crashed")
        inv.phase = InvestigationPhase.failed
        emit(StreamEvent(investigation_id=inv.id, type="error", phase=inv.phase, data=str(e)))
    finally:
        asyncio.create_task(_persist_investigation(inv))
        asyncio.create_task(_notify_backend_callback(inv))


async def _notify_backend_callback(inv: Investigation):
    """POST RCA results back to the backend incident so the UI can show them."""
    if not inv.incident_id or not BACKEND_URL:
        return
    root_cause_text = ""
    confidence = 0.0
    if inv.root_cause:
        root_cause_text = inv.root_cause.summary
        confidence = inv.root_cause.confidence
    status = "completed" if inv.phase == InvestigationPhase.completed else "failed"
    payload = {
        "investigation_id": inv.id,
        "root_cause": root_cause_text,
        "confidence": confidence,
        "status": status,
    }
    url = f"{BACKEND_URL}/api/v1/incidents/{inv.incident_id}/rca-callback"
    headers = {}
    if INTERNAL_SERVICE_TOKEN:
        headers["X-Internal-Token"] = INTERNAL_SERVICE_TOKEN
    # Retry up to 3 times with exponential back-off (5s, 10s) so a transient backend
    # restart doesn't permanently strand the incident in 'investigating' state.
    for attempt in range(3):
        try:
            async with aiohttp.ClientSession() as session:
                async with session.post(url, json=payload, headers=headers, timeout=aiohttp.ClientTimeout(total=10)) as resp:
                    if resp.status == 200:
                        log.info(f"RCA callback delivered for incident {inv.incident_id} (inv={inv.id})")
                        return
                    log.warning(f"RCA callback non-200 attempt {attempt+1}/3 for incident {inv.incident_id}: {resp.status}")
        except Exception as e:
            log.warning(f"RCA callback failed attempt {attempt+1}/3 for incident {inv.incident_id}: {e}")
        if attempt < 2:
            await asyncio.sleep(5 * (2 ** attempt))  # 5s then 10s


async def _kafka_alert_consumer():
    """Consume incoming alerts from Kafka and auto-start investigations for critical ones."""
    consumer = AIOKafkaConsumer(
        KAFKA_TOPIC_ALERTS,
        bootstrap_servers=KAFKA_BROKERS,
        group_id="rca-orchestrator-intake",
        value_deserializer=lambda v: json.loads(v.decode("utf-8")),
        auto_offset_reset="latest",
    )
    try:
        await consumer.start()
        log.info("Kafka alert consumer started")
        async for msg in consumer:
            alert = msg.value
            severity = (
                alert.get("severity")
                or alert.get("labels", {}).get("severity", "")
            ).lower()
            alertname = (
                alert.get("alertname", "")
                or alert.get("title", "")
                or alert.get("name", "")
            ).lower()
            # Pod/container health alerts auto-trigger even at warning level
            pod_alert = any(kw in alertname for kw in (
                "notready", "not_ready", "crashloop", "oomkill", "imagepull",
                "pending", "unschedulable", "container", "pod",
            ))
            if severity in ("critical", "high") or (severity == "warning" and pod_alert):
                await _auto_start_investigation(alert)
    except Exception as e:
        log.error(f"Kafka alert consumer error: {e}")
    finally:
        try:
            await consumer.stop()
        except Exception:
            pass


def _extract_alert_context(alert: dict) -> dict:
    """Normalize alert fields across different alert source formats (Dynatrace, Prometheus, Alertmanager)."""
    labels = alert.get("labels") or alert.get("annotations") or {}
    annotations = alert.get("annotations") or {}

    # Namespace: check common label names across Prometheus/Dynatrace/Alertmanager
    namespace = (
        alert.get("namespace")
        or labels.get("namespace")
        or labels.get("exported_namespace")
        or annotations.get("namespace")
        or labels.get("kubernetes_namespace")
    )

    # Cluster: check cluster/datacenter labels
    cluster = (
        alert.get("cluster")
        or labels.get("cluster")
        or labels.get("cluster_name")
        or labels.get("datacenter")
        or labels.get("site")
        or annotations.get("cluster")
    )

    # Service / pod
    service = (
        alert.get("service")
        or labels.get("service")
        or labels.get("app")
        or labels.get("workload")
        or labels.get("deployment")
        or annotations.get("service")
    )

    # Pod name — relevant for pod-not-ready alerts
    pod = (
        alert.get("pod")
        or labels.get("pod")
        or labels.get("pod_name")
        or annotations.get("pod")
    )

    # Enrich alert_body with extracted context so the LLM has it immediately
    enriched = dict(alert)
    if pod:
        enriched["_extracted_pod"] = pod
    if namespace:
        enriched["_extracted_namespace"] = namespace
    if cluster:
        enriched["_extracted_cluster"] = cluster
    if service:
        enriched["_extracted_service"] = service

    return {
        "namespace": namespace,
        "cluster": cluster,
        "service": service,
        "pod": pod,
        "enriched_body": enriched,
    }


async def _auto_start_investigation(alert: dict):
    """Auto-start investigation for critical alerts from Kafka."""
    ctx = _extract_alert_context(alert)
    inv = Investigation(
        alert_id=alert.get("id", alert.get("fingerprint", str(uuid.uuid4()))),
        alert_title=alert.get("title", alert.get("name", alert.get("alertname", "Unknown Alert"))),
        alert_body=ctx["enriched_body"],
        severity=alert.get("severity", alert.get("labels", {}).get("severity", "high")),
        incident_id=alert.get("incident_id"),
        namespace=ctx["namespace"],
        cluster=ctx["cluster"],
        service=ctx["service"],
        orchestrator_version="v2" if RCA_V2_ENABLED else "v1",
    )
    investigations[inv.id] = inv
    stream_queues[inv.id] = []
    asyncio.create_task(_persist_investigation(inv))
    asyncio.create_task(_run_investigation(inv))
    log.info(f"Auto-started investigation {inv.id} for alert: {inv.alert_title} ns={ctx['namespace']} pod={ctx['pod']}")


async def _kafka_learning_consumer():
    await asyncio.sleep(10)  # Wait for startup
    await ingest_kafka_incidents(knowledge_store)


async def _weekly_model_trainer():
    """Retrain the Ollama model weekly from accumulated confirmed incidents."""
    await asyncio.sleep(3600)  # Wait 1h before first check
    while True:
        try:
            await build_and_update_model(knowledge_store)
        except Exception as e:
            log.error(f"Weekly training error: {e}")
        await asyncio.sleep(7 * 24 * 3600)  # Weekly
