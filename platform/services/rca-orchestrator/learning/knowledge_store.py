from __future__ import annotations
import json
import logging
import os
import uuid
from datetime import datetime
from typing import Any
import weaviate
from weaviate.classes.init import Auth
from weaviate.classes.config import Configure, Property, DataType
from weaviate.classes.query import MetadataQuery
import aiohttp

log = logging.getLogger(__name__)

WEAVIATE_URL = os.getenv("WEAVIATE_URL", "http://weaviate.alert-engine-poc.svc.cluster.local:8080")
WEAVIATE_API_KEY = os.getenv("WEAVIATE_API_KEY", "weaviate-AIOps-Key-2024")
OLLAMA_URL = os.getenv("OLLAMA_URL", "http://ollama.alert-engine-poc.svc.cluster.local:11434")
EMBED_MODEL = os.getenv("EMBED_MODEL", "nomic-embed-text")

INCIDENT_CLASS = "RCAIncident"
KNOWLEDGE_CLASS = "SREKnowledge"


BERT_URL = os.getenv("BERT_URL", "http://alerthub-bert-service:8765")

async def _embed(text: str) -> list[float]:
    """Generate embedding. Tries Ollama nomic-embed-text first, falls back to BERT service.

    The BERT service (384-dim all-MiniLM-L6-v2) is always available in the cluster;
    Ollama may be Pending due to resource constraints. Both produce usable embeddings
    for similarity search — BERT is smaller but fast and reliable.
    """
    # Primary: Ollama nomic-embed-text (768-dim, higher quality)
    try:
        async with aiohttp.ClientSession() as session:
            async with session.post(
                f"{OLLAMA_URL}/api/embeddings",
                json={"model": EMBED_MODEL, "prompt": text},
                timeout=aiohttp.ClientTimeout(total=8),
            ) as resp:
                if resp.status == 200:
                    data = await resp.json()
                    vec = data.get("embedding", [])
                    if vec:
                        return vec
    except Exception:
        pass  # fall through to BERT

    # Fallback: BERT service (384-dim all-MiniLM-L6-v2) — always available in cluster
    try:
        async with aiohttp.ClientSession() as session:
            async with session.post(
                f"{BERT_URL}/embed",
                json={"text": text},
                timeout=aiohttp.ClientTimeout(total=10),
            ) as resp:
                if resp.status == 200:
                    data = await resp.json()
                    vec = data.get("embedding", data.get("vector", []))
                    if vec:
                        return vec
    except Exception as e:
        log.debug(f"BERT fallback embed failed: {e}")

    return []


class KnowledgeStore:
    def __init__(self):
        self.client = None

    def connect(self):
        host = WEAVIATE_URL.replace("http://", "").replace("https://", "").split(":")[0]
        port = int(WEAVIATE_URL.split(":")[-1]) if ":" in WEAVIATE_URL.split("/")[-1] else 8080
        auth = Auth.api_key(WEAVIATE_API_KEY) if WEAVIATE_API_KEY else None
        try:
            self.client = weaviate.connect_to_custom(
                http_host=host,
                http_port=port,
                http_secure=False,
                grpc_host=host,
                grpc_port=50051,
                grpc_secure=False,
                auth_credentials=auth,
                skip_init_checks=True,
                additional_config=weaviate.classes.init.AdditionalConfig(
                    timeout=weaviate.classes.init.Timeout(init=30, query=60, insert=120),
                ),
            )
            self._ensure_schema()
            log.info("KnowledgeStore connected to Weaviate")
        except Exception as e:
            log.warning(f"Weaviate unavailable (RAG disabled): {e}")
            self.client = None

    def _ensure_schema(self):
        if not self.client.collections.exists(INCIDENT_CLASS):
            self.client.collections.create(
                name=INCIDENT_CLASS,
                description="RCA investigations with root causes and resolutions",
                properties=[
                    Property(name="alert_title", data_type=DataType.TEXT),
                    Property(name="alert_body", data_type=DataType.TEXT),
                    Property(name="root_cause_summary", data_type=DataType.TEXT),
                    Property(name="root_cause_component", data_type=DataType.TEXT),
                    Property(name="root_cause_category", data_type=DataType.TEXT),
                    Property(name="confidence", data_type=DataType.NUMBER),
                    Property(name="severity", data_type=DataType.TEXT),
                    Property(name="namespace", data_type=DataType.TEXT),
                    Property(name="cluster", data_type=DataType.TEXT),
                    Property(name="service", data_type=DataType.TEXT),
                    Property(name="evidence", data_type=DataType.TEXT),
                    Property(name="remediation", data_type=DataType.TEXT),
                    Property(name="thought_log", data_type=DataType.TEXT),
                    Property(name="feedback_score", data_type=DataType.INT),
                    Property(name="confirmed", data_type=DataType.BOOL),
                    Property(name="investigation_id", data_type=DataType.TEXT),
                    Property(name="occurred_at", data_type=DataType.DATE),
                ],
                vectorizer_config=Configure.Vectorizer.none(),
            )
            log.info(f"Created Weaviate class: {INCIDENT_CLASS}")

        if not self.client.collections.exists(KNOWLEDGE_CLASS):
            self.client.collections.create(
                name=KNOWLEDGE_CLASS,
                description="SRE runbooks, known issues, and domain knowledge",
                properties=[
                    Property(name="title", data_type=DataType.TEXT),
                    Property(name="content", data_type=DataType.TEXT),
                    Property(name="category", data_type=DataType.TEXT),
                    Property(name="tags", data_type=DataType.TEXT),
                    Property(name="created_by", data_type=DataType.TEXT),
                    Property(name="knowledge_id", data_type=DataType.TEXT),
                ],
                vectorizer_config=Configure.Vectorizer.none(),
            )
            log.info(f"Created Weaviate class: {KNOWLEDGE_CLASS}")

    async def store_investigation(self, inv) -> str:
        """Store a completed investigation for future RAG retrieval."""
        if not self.client or not inv.root_cause:
            return ""
        try:
            text = f"{inv.alert_title} {inv.root_cause.summary} {inv.root_cause.component} {' '.join(inv.root_cause.evidence)}"
            vector = await _embed(text)
            collection = self.client.collections.get(INCIDENT_CLASS)
            uuid_val = collection.data.insert(
                properties={
                    "alert_title": inv.alert_title,
                    "alert_body": json.dumps(inv.alert_body, default=str)[:2000],
                    "root_cause_summary": inv.root_cause.summary,
                    "root_cause_component": inv.root_cause.component,
                    "root_cause_category": inv.root_cause.category,
                    "confidence": inv.root_cause.confidence,
                    "severity": inv.severity,
                    "namespace": inv.namespace or "",
                    "cluster": inv.cluster or "",
                    "service": inv.service or "",
                    "evidence": json.dumps(inv.root_cause.evidence),
                    "remediation": json.dumps([r.dict() for r in inv.remediation], default=str)[:3000],
                    "thought_log": "\n".join(inv.thought_log)[:3000],
                    "feedback_score": inv.feedback_score or 0,
                    "confirmed": inv.confirmed,
                    "investigation_id": inv.id,
                    "occurred_at": inv.started_at.isoformat() + "Z",
                },
                vector=vector,
            )
            log.info(f"Stored investigation {inv.id} in Weaviate")
            return str(uuid_val)
        except Exception as e:
            log.warning(f"Weaviate store_investigation failed (non-fatal): {e}")
            return ""

    async def search_similar(self, title: str, alert_body: dict, limit: int = 3) -> list[dict]:
        """Find similar past incidents using vector similarity."""
        if not self.client:
            return []
        try:
            query_text = f"{title} {json.dumps(alert_body, default=str)[:500]}"
            vector = await _embed(query_text)
            collection = self.client.collections.get(INCIDENT_CLASS)
            results = collection.query.near_vector(
                near_vector=vector,
                limit=limit,
                return_metadata=MetadataQuery(distance=True),
                return_properties=[
                    "alert_title", "root_cause_summary", "root_cause_component",
                    "root_cause_category", "confidence", "evidence", "remediation",
                    "feedback_score", "confirmed", "occurred_at",
                ],
            )
            similar = []
            for obj in results.objects:
                if obj.metadata.distance < 0.6:  # similarity > 0.4 — broad enough to surface relevant past incidents
                    item = dict(obj.properties)
                    item["similarity_score"] = round(1 - obj.metadata.distance, 3)
                    try:
                        item["evidence"] = json.loads(item.get("evidence", "[]"))
                        item["remediation"] = json.loads(item.get("remediation", "[]"))
                    except Exception:
                        pass
                    similar.append(item)
            return similar
        except Exception as e:
            log.warning(f"Weaviate search_similar failed (non-fatal): {e}")
            return []

    async def store_knowledge(self, entry) -> str:
        """Store a knowledge base entry (runbook, known issue, etc)."""
        if not self.client:
            return ""
        try:
            text = f"{entry.title} {entry.content} {' '.join(entry.tags)}"
            vector = await _embed(text)
            collection = self.client.collections.get(KNOWLEDGE_CLASS)
            uuid_val = collection.data.insert(
                properties={
                    "title": entry.title,
                    "content": entry.content,
                    "category": entry.category,
                    "tags": json.dumps(entry.tags),
                    "created_by": entry.created_by,
                    "knowledge_id": entry.id,
                },
                vector=vector,
            )
            return str(uuid_val)
        except Exception as e:
            log.warning(f"Weaviate store_knowledge failed (non-fatal): {e}")
            return ""

    async def bootstrap_from_history(self, pg_dsn: str, limit: int = 500) -> int:
        """Seed Weaviate from historical rca_investigations rows.

        Queries the rca_investigations table for completed investigations with
        a non-empty root cause summary and stores them as RCAIncident vectors.
        This cold-starts the knowledge store so search_similar() returns results
        on the very first investigation after deployment.

        Returns the number of investigations imported.
        """
        if not self.client:
            return 0
        try:
            import asyncpg
            conn = await asyncpg.connect(pg_dsn, timeout=15)
            rows = await conn.fetch(
                """
                SELECT
                    id::text                                          AS id,
                    COALESCE(data->>'title', 'Unknown incident')     AS alert_title,
                    COALESCE(data->>'severity', 'medium')            AS severity,
                    COALESCE(data->>'namespace', '')                 AS namespace,
                    COALESCE(data->>'cluster', '')                   AS cluster,
                    COALESCE(data->>'service', '')                   AS service,
                    COALESCE(
                        data->'root_causes'->0->>'description',
                        data->>'root_cause_summary',
                        ''
                    )                                                AS root_cause_summary,
                    COALESCE(
                        data->'root_causes'->0->>'component',
                        data->>'root_entity_label',
                        'unknown'
                    )                                                AS root_cause_component,
                    COALESCE(
                        data->'go_context'->>'domain',
                        'kubernetes'
                    )                                                AS root_cause_category,
                    COALESCE(
                        (data->'root_causes'->0->>'confidence')::float,
                        0.0
                    )                                                AS confidence,
                    created_at
                FROM (
                    -- Source 1: OIE structured investigations (high quality, evidence-based)
                    SELECT
                        inv.id::text                                           AS id,
                        COALESCE(i.title, 'Unknown incident')                  AS alert_title,
                        COALESCE(i.severity, 'medium')                         AS severity,
                        SPLIT_PART(COALESCE(inv.topology_path, ''), '/', 2)    AS namespace,
                        SPLIT_PART(COALESCE(inv.topology_path, ''), '/', 1)    AS cluster,
                        ''                                                      AS service,
                        COALESCE(inv.root_cause_summary,
                            i.ai_root_cause, '')                               AS root_cause_summary,
                        COALESCE(inv.topology_path, 'kubernetes')              AS root_cause_component,
                        COALESCE(inv.domain::text, 'kubernetes')               AS root_cause_category,
                        COALESCE(inv.confidence, 0.0)                          AS confidence,
                        inv.triggered_at                                        AS created_at
                    FROM investigations_2026_06 inv
                    LEFT JOIN incidents i ON i.rca_investigation_id = inv.id::text
                    WHERE inv.status = 'COMPLETED'
                      AND COALESCE(inv.confidence, 0.0) > 0.30
                      AND COALESCE(inv.root_cause_summary, i.ai_root_cause, '') != ''
                      AND COALESCE(inv.root_cause_summary, '') NOT LIKE '%unknown%'

                    UNION ALL

                    -- Source 2: Legacy rca_investigations (LLM-based, filter for useful ones)
                    SELECT
                        id::text,
                        COALESCE(data->>'title', data->>'alert_title', 'Unknown incident'),
                        COALESCE(data->>'severity', 'medium'),
                        COALESCE(data->>'namespace', ''),
                        COALESCE(data->>'cluster', ''),
                        COALESCE(data->>'service', ''),
                        COALESCE(data->'root_cause'->>'summary',
                                 data->>'summary', ''),
                        COALESCE(data->'root_cause'->>'component', 'unknown'),
                        COALESCE(data->>'v2_domain', 'kubernetes'),
                        COALESCE((data->'root_cause'->>'confidence')::float, 0.0),
                        created_at
                    FROM rca_investigations
                    WHERE data->>'phase' = 'completed'
                      AND COALESCE(data->'root_cause'->>'summary', '') != ''
                      AND COALESCE(data->'root_cause'->>'summary', '') NOT LIKE '%unknown%'
                      AND COALESCE((data->'root_cause'->>'confidence')::float, 0.0) > 0.45
                ) combined
                ORDER BY confidence DESC, created_at DESC
                LIMIT $1
                """,
                limit,
            )
            await conn.close()

            imported = 0
            collection = self.client.collections.get(INCIDENT_CLASS)

            for row in rows:
                try:
                    text = (
                        f"{row['alert_title']} "
                        f"{row['root_cause_summary']} "
                        f"{row['root_cause_component']} "
                        f"{row['root_cause_category']}"
                    )
                    vector = await _embed(text)
                    if not vector:
                        continue

                    collection.data.insert(
                        properties={
                            "alert_title":          row["alert_title"],
                            "alert_body":           "",
                            "root_cause_summary":   row["root_cause_summary"],
                            "root_cause_component": row["root_cause_component"],
                            "root_cause_category":  row["root_cause_category"],
                            "confidence":           float(row["confidence"]),
                            "severity":             row["severity"],
                            "namespace":            row["namespace"],
                            "cluster":              row["cluster"],
                            "service":              row["service"],
                            "evidence":             "[]",
                            "remediation":          "[]",
                            "thought_log":          "",
                            "feedback_score":       0,
                            "confirmed":            row["confidence"] >= 0.70,
                            "investigation_id":     row["id"],
                            "occurred_at":          row["created_at"].isoformat() + "Z",
                        },
                        vector=vector,
                    )
                    imported += 1
                except Exception as e:
                    log.debug(f"Bootstrap: skipping row {row['id']}: {e}")

            log.info(f"Weaviate bootstrap: imported {imported}/{len(rows)} historical investigations")
            return imported

        except Exception as e:
            log.warning(f"Weaviate bootstrap_from_history failed (non-fatal): {e}")
            return 0
            log.warning(f"Weaviate store_knowledge failed (non-fatal): {e}")
            return ""

    async def search_knowledge(self, query: str, limit: int = 3) -> list[dict]:
        """Search knowledge base for relevant runbooks and known issues."""
        if not self.client:
            return []
        try:
            vector = await _embed(query)
            collection = self.client.collections.get(KNOWLEDGE_CLASS)
            results = collection.query.near_vector(
                near_vector=vector,
                limit=limit,
                return_metadata=MetadataQuery(distance=True),
                return_properties=["title", "content", "category", "tags", "knowledge_id"],
            )
            return [
                {**dict(obj.properties), "score": round(1 - obj.metadata.distance, 3)}
                for obj in results.objects
            ]
        except Exception as e:
            log.warning(f"Weaviate search_knowledge failed (non-fatal): {e}")
            return []

    async def update_feedback(self, investigation_id: str, score: int, confirmed: bool, correct_rca: str = "") -> bool:
        """Update a stored investigation with operator feedback — improves future retrievals."""
        if not self.client:
            return False
        try:
            collection = self.client.collections.get(INCIDENT_CLASS)
            results = collection.query.fetch_objects(
                filters=weaviate.classes.query.Filter.by_property("investigation_id").equal(investigation_id),
                limit=1,
            )
            if not results.objects:
                return False
            obj_uuid = results.objects[0].uuid
            update_props = {"feedback_score": score, "confirmed": confirmed}
            if correct_rca:
                update_props["root_cause_summary"] = correct_rca
                existing = dict(results.objects[0].properties)
                new_text = f"{existing.get('alert_title', '')} {correct_rca}"
                update_props["_vector"] = await _embed(new_text)
            collection.data.update(uuid=obj_uuid, properties=update_props)
            log.info(f"Updated feedback for investigation {investigation_id}: score={score}, confirmed={confirmed}")
            return True
        except Exception as e:
            log.warning(f"Weaviate update_feedback failed (non-fatal): {e}")
            return False

    async def get_learning_corpus(self, min_score: int = 3, limit: int = 100) -> list[dict]:
        """Get high-quality confirmed RCAs for model training corpus generation."""
        if not self.client:
            return []
        try:
            collection = self.client.collections.get(INCIDENT_CLASS)
            results = collection.query.fetch_objects(
                filters=(
                    weaviate.classes.query.Filter.by_property("feedback_score").greater_or_equal(min_score) |
                    weaviate.classes.query.Filter.by_property("confirmed").equal(True)
                ),
                limit=limit,
                return_properties=[
                    "alert_title", "alert_body", "root_cause_summary", "root_cause_component",
                    "root_cause_category", "evidence", "remediation", "thought_log", "feedback_score",
                ],
            )
            return [dict(obj.properties) for obj in results.objects]
        except Exception as e:
            log.warning(f"Weaviate get_learning_corpus failed (non-fatal): {e}")
            return []
