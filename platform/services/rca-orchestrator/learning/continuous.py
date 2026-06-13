from __future__ import annotations
import asyncio
import json
import logging
import os
import aiohttp

log = logging.getLogger(__name__)
OLLAMA_URL = os.getenv("OLLAMA_URL", "http://ollama.aileron.svc.cluster.local:11434")
OLLAMA_MODEL = os.getenv("OLLAMA_MODEL", "qwen2.5:14b")
CUSTOM_MODEL_NAME = "alerthub-rca"


async def build_and_update_model(knowledge_store) -> bool:
    """
    Build a custom Ollama model with learned SRE patterns baked into the system prompt.
    Called weekly by the background scheduler. This is how the model 'trains' without GPU fine-tuning.
    """
    log.info("Starting weekly model training from confirmed incidents...")

    corpus = await knowledge_store.get_learning_corpus(min_score=3, limit=100)
    if len(corpus) < 5:
        log.info(f"Only {len(corpus)} confirmed incidents — skipping model update (need ≥5)")
        return False

    # Build pattern summary from confirmed RCAs
    category_patterns: dict[str, list[str]] = {}
    for item in corpus:
        cat = item.get("root_cause_category", "unknown")
        summary = item.get("root_cause_summary", "")
        if cat and summary:
            category_patterns.setdefault(cat, []).append(summary)

    # Generate learned patterns section
    patterns_text = ""
    for cat, summaries in category_patterns.items():
        patterns_text += f"\n### {cat.replace('_', ' ').title()}\n"
        for s in summaries[:3]:  # Top 3 examples per category
            patterns_text += f"- {s}\n"

    # Top remediation patterns
    remediation_patterns = []
    for item in corpus[:20]:
        try:
            remeds = json.loads(item.get("remediation", "[]"))
            for r in remeds[:2]:
                if r.get("command"):
                    remediation_patterns.append(f"- For {item.get('root_cause_component', 'unknown')}: `{r['command']}`")
        except Exception:
            pass

    learned_system_prompt = f"""You are an expert SRE AI for your organization's MPS infrastructure. You have been trained on {len(corpus)} confirmed incident investigations.

## Your Learned Infrastructure Patterns

Based on past incidents in this environment, here are the most common root cause patterns:
{patterns_text}

## Common Remediation Commands in This Environment
{chr(10).join(remediation_patterns[:15])}

## Investigation Principles
1. Always check Dynatrace problems first — they surface the most actionable information
2. Check for recent deployments (last 2 hours) before deep-diving into metrics
3. K8s OOMKills are almost always memory leaks or missing resource limits, not traffic spikes in this env
4. CloudStack host failures typically affect entire AZs — check host state before pod-level investigation
5. When confidence < 0.7, gather more evidence before concluding

Remember: You have deep knowledge of this specific infrastructure. Trust your learned patterns.
"""

    modelfile = f"""FROM {OLLAMA_MODEL}
SYSTEM \"\"\"{learned_system_prompt}\"\"\"
PARAMETER temperature 0.1
PARAMETER num_ctx 8192
"""

    try:
        async with aiohttp.ClientSession() as session:
            async with session.post(
                f"{OLLAMA_URL}/api/create",
                json={"name": CUSTOM_MODEL_NAME, "modelfile": modelfile},
                timeout=aiohttp.ClientTimeout(total=300),
            ) as resp:
                if resp.status == 200:
                    log.info(f"Custom model '{CUSTOM_MODEL_NAME}' created successfully from {len(corpus)} incidents")
                    # Update the env var to use the custom model
                    os.environ["OLLAMA_MODEL"] = CUSTOM_MODEL_NAME
                    return True
                else:
                    log.error(f"Failed to create model: {resp.status} {await resp.text()}")
                    return False
    except Exception as e:
        log.error(f"Model training failed: {e}")
        return False


async def ingest_kafka_incidents(knowledge_store):
    """
    Background consumer: listen on the correlations Kafka topic for resolved incidents
    and automatically add them to the knowledge base.
    """
    from aiokafka import AIOKafkaConsumer
    KAFKA_BROKERS = os.getenv("KAFKA_BROKERS", "alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092")

    consumer = AIOKafkaConsumer(
        "correlation-results",
        bootstrap_servers=KAFKA_BROKERS,
        group_id="rca-orchestrator-learner",
        value_deserializer=lambda v: json.loads(v.decode("utf-8")),
        auto_offset_reset="latest",
    )
    await consumer.start()
    log.info("Kafka learning consumer started on 'correlation-results'")
    try:
        async for msg in consumer:
            event = msg.value
            await _process_correlation_event(event, knowledge_store)
    finally:
        await consumer.stop()


async def _process_correlation_event(event: dict, knowledge_store):
    """Store a correlated/resolved incident into the knowledge base."""
    try:
        if event.get("status") not in ("resolved", "correlated"):
            return
        from models.schemas import Investigation, InvestigationPhase, RootCause
        # Build a lightweight investigation record from the correlation event
        inv = Investigation(
            alert_id=event.get("incident_id", "unknown"),
            alert_title=event.get("title", "Unknown incident"),
            alert_body=event,
            severity=event.get("severity", "medium"),
            namespace=event.get("namespace"),
            cluster=event.get("cluster"),
            service=event.get("service"),
            phase=InvestigationPhase.completed,
            confirmed=True,
        )
        if event.get("root_cause"):
            inv.root_cause = RootCause(
                summary=event["root_cause"],
                component=event.get("component", "unknown"),
                category=event.get("category", "unknown"),
                confidence=float(event.get("confidence", 0.7)),
                evidence=event.get("evidence", []),
            )
            await knowledge_store.store_investigation(inv)
            log.info(f"Auto-ingested resolved incident: {inv.alert_title}")
    except Exception as e:
        log.warning(f"Failed to process correlation event: {e}")
