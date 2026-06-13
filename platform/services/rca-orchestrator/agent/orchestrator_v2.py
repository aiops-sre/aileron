from __future__ import annotations
import json
import logging
import os
from datetime import datetime
from typing import Any, Callable

from models.schemas import (
    Investigation, InvestigationPhase, Hypothesis, RootCause, RemediationStep, StreamEvent,
)
from engine import DomainClassifier, InvestigationDAGEngine, CausalGraphEngine, RCAScorer
from tools.temporal_tool import QueryTemporalPropagationTool
from learning.topology_aware_retrieval import retrieve_topology_aware_historical
from agent.llm_narrator import narrate

log = logging.getLogger(__name__)

OLLAMA_URL = os.getenv("OLLAMA_URL", "http://ollama.aileron.svc.cluster.local:11434")
OLLAMA_MODEL = os.getenv("OLLAMA_MODEL", "qwen2.5:3b")
OLLAMA_TIMEOUT = int(os.getenv("OLLAMA_TIMEOUT", "240"))


# Patterns that indicate internal AlertHub plumbing state — must never appear
# in a customer-facing RCA narrative.
_INTERNAL_NOISE_PATTERNS = [
    "neo4j unavailable",
    "fell back to redis",
    "redis topology graph",
    "go engine indicates",
    "topology graph analysis via cached",
    "in-memory fallback",
]

def _scrub_internal_text(text: str) -> str:
    """Remove internal AlertHub plumbing language from customer-facing RCA text."""
    if not text:
        return text
    import re
    for pattern in _INTERNAL_NOISE_PATTERNS:
        if pattern.lower() in text.lower():
            cleaned = re.sub(
                r'[^.!?\n]*' + re.escape(pattern) + r'[^.!?\n]*[.!?]?\s*',
                '', text, flags=re.IGNORECASE
            ).strip()
            text = cleaned if cleaned.strip() else text
    result = text.strip(" .,;")
    return result if result else "Root cause analysis inconclusive — operator investigation required."


class RCAOrchestratorV2:
    """Deterministic 9-phase RCA orchestrator.

    Replaces LLM ReAct tool selection with a deterministic pipeline:
    1. Ingest Go context
    2. Domain classification
    3. Topology-aware historical retrieval
    4. Deterministic investigation DAG
    5. Temporal propagation reconstruction
    6. Causal graph construction
    7. Probabilistic scoring (6-source evidence fusion)
    8. LLM narration (prose only — LLM cannot change root cause)
    9. Store + emit result
    """

    def __init__(self, knowledge_store=None) -> None:
        self.knowledge_store = knowledge_store
        self._domain_classifier = DomainClassifier()
        self._dag_engine = InvestigationDAGEngine()
        self._graph_engine = CausalGraphEngine()
        self._scorer = RCAScorer()
        self._temporal_tool = QueryTemporalPropagationTool()

    async def run(
        self, inv: Investigation, emit: Callable[[StreamEvent], None]
    ) -> Investigation:
        go_context: dict[str, Any] = inv.alert_body.get("go_context") or {}

        # ── Phase 1: Ingest Go context ──────────────────────────────────────
        await self._set_phase(inv, InvestigationPhase.context_gathering, emit)
        self._annotate_from_go_context(inv, go_context)
        emit(StreamEvent(
            investigation_id=inv.id, type="thought",
            phase=inv.phase,
            data=f"[V2] Go context ingested: domain={go_context.get('domain')}, "
                 f"root_entity={go_context.get('root_entity_label')}, "
                 f"blast_radius={go_context.get('blast_radius_size', 0)}, "
                 f"hypotheses={len(go_context.get('hypotheses') or [])}",
        ))

        # ── Phase 2: Domain classification ──────────────────────────────────
        classification = self._domain_classifier.classify(inv.alert_body, go_context)
        emit(StreamEvent(
            investigation_id=inv.id, type="thought",
            phase=inv.phase,
            data=f"[V2] Domain: {classification.domain.value} "
                 f"(conf={classification.confidence:.2f}, "
                 f"go_deferred={classification.go_domain_used}, "
                 f"chain={[d.value for d in classification.investigation_chain]})",
        ))

        # ── Phase 3: Topology-aware historical retrieval ─────────────────────
        await self._set_phase(inv, InvestigationPhase.hypothesis_formation, emit)
        historical = await retrieve_topology_aware_historical(
            go_context=go_context,
            alert_title=inv.alert_title,
            alert_body=inv.alert_body,
            knowledge_store=self.knowledge_store,
            limit=5,
        )
        inv.similar_incidents = historical
        if historical:
            emit(StreamEvent(
                investigation_id=inv.id, type="thought",
                phase=inv.phase,
                data=f"[V2] Historical: {len(historical)} topology-matched past incidents "
                     f"(best combined_score={historical[0].get('combined_score', 0):.2f})",
            ))

        # Populate Hypothesis objects from Go engine hypotheses.
        for h in (go_context.get("hypotheses") or [])[:5]:
            inv.hypotheses.append(Hypothesis(
                description=f"{h.get('entity_label', h.get('entity_id', 'unknown'))} "
                            f"({h.get('entity_type', '')}) in domain {h.get('domain', '')}",
                confidence=h.get("confidence", 0.0),
                supporting_evidence=[f"Go engine confidence: {h.get('confidence', 0):.2f}"],
            ))

        # ── Phase 4: Deterministic investigation DAG ─────────────────────────
        await self._set_phase(inv, InvestigationPhase.evidence_collection, emit)
        dag_result = await self._dag_engine.execute(inv, go_context, classification, emit)
        emit(StreamEvent(
            investigation_id=inv.id, type="thought",
            phase=inv.phase,
            data=f"[V2] DAG complete: {len(dag_result.stages_run)} tools run, "
                 f"{len(dag_result.tool_errors)} errors, {dag_result.duration_ms}ms",
        ))

        # ── Phase 5: Temporal propagation reconstruction ─────────────────────
        temporal_sequence: list[dict] = []
        if inv.incident_id:
            try:
                raw, _ = await self._temporal_tool.run(
                    incident_id=inv.incident_id,
                    namespace=inv.namespace or "",
                )
                temporal_sequence = json.loads(raw) if raw and "[" in raw else []
                if temporal_sequence:
                    emit(StreamEvent(
                        investigation_id=inv.id, type="thought",
                        phase=inv.phase,
                        data=f"[V2] Temporal: {len(temporal_sequence)} alerts in propagation sequence",
                    ))
            except Exception as e:
                log.warning(f"Temporal propagation failed for investigation {inv.id}: {e}")

        # ── Phase 6: Causal graph construction ──────────────────────────────
        await self._set_phase(inv, InvestigationPhase.root_cause_analysis, emit)
        graph_result = self._graph_engine.build_and_solve(
            go_context=go_context,
            dag_outputs=dag_result.tool_outputs,
            temporal_sequence=temporal_sequence,
        )
        for line in graph_result.reasoning:
            emit(StreamEvent(
                investigation_id=inv.id, type="thought",
                phase=inv.phase, data=f"[V2 Graph] {line}",
            ))

        # ── Phase 7: Probabilistic scoring (6-source fusion) ─────────────────
        scored_result = self._scorer.score_and_rank(
            go_context=go_context,
            graph_result=graph_result,
            classification_confidence=classification.confidence,
            historical_matches=historical,
        )
        for line in scored_result.reasoning:
            emit(StreamEvent(
                investigation_id=inv.id, type="thought",
                phase=inv.phase, data=f"[V2 Scorer] {line}",
            ))

        # ── Phase 8: LLM narration (prose only) ─────────────────────────────
        await self._set_phase(inv, InvestigationPhase.remediation_planning, emit)
        narrator_output = await narrate(
            inv=inv,
            scored_result=scored_result,
            graph_result=graph_result,
            dag_outputs=dag_result.tool_outputs,
            go_context=go_context,
            historical_matches=historical,
            llm_caller=self._llm_call,
            emit=emit,
        )

        # ── Phase 9: Assemble final result ───────────────────────────────────
        top = scored_result.top_hypothesis
        root_entity = (top.entity_label if top else
                       go_context.get("root_entity_label") or
                       (graph_result.root_cause_node.label if graph_result.root_cause_node else "unknown"))
        root_type = (top.entity_type if top else
                     go_context.get("root_entity_type") or "unknown")
        root_domain = (top.domain if top else
                       go_context.get("domain") or classification.domain.value)
        final_conf = scored_result.confidence or (graph_result.confidence * 0.8)

        summary = narrator_output.get("summary") or (
            f"Root cause identified: {root_entity} ({root_type}) "
            f"in domain {root_domain} with {final_conf:.0%} confidence. "
            + "; ".join(scored_result.reasoning[:2])
        )

        evidence_list = [
            f"Go engine hypotheses: {len(go_context.get('hypotheses') or [])} candidates",
            f"Topology depth: {go_context.get('topo_depth', 0)} hops",
            f"Blast radius: {go_context.get('blast_radius_size', 0)} entities",
        ]
        if graph_result.reasoning:
            evidence_list.append(graph_result.reasoning[0])

        inv.summary = _scrub_internal_text(summary)
        inv.root_cause = RootCause(
            summary=_scrub_internal_text(summary),
            component=root_entity,
            category=root_domain.lower(),
            confidence=final_conf,
            evidence=evidence_list,
            timeline=self._build_timeline(go_context, temporal_sequence),
        )

        # Map narrator remediation steps
        for step_data in narrator_output.get("remediation_steps", []):
            if isinstance(step_data, dict):
                inv.remediation.append(RemediationStep(
                    step=step_data.get("step", len(inv.remediation) + 1),
                    action=step_data.get("action", ""),
                    command=step_data.get("command"),
                    automated=False,
                    risk=step_data.get("risk", "low"),
                ))

        if inv.root_cause and inv.root_cause.summary:
            inv.phase = InvestigationPhase.completed
        else:
            inv.phase = InvestigationPhase.failed
            log.warning(f"V2 investigation {inv.id} produced no root cause")

        emit(StreamEvent(
            investigation_id=inv.id,
            type="result",
            phase=inv.phase,
            data={
                "summary": inv.summary,
                "root_cause": inv.root_cause.dict() if inv.root_cause else None,
                "remediation": [r.dict() for r in inv.remediation],
                "v2_scoring": {
                    "top_hypothesis": top.entity_label if top else None,
                    "confidence": final_conf,
                    "domain": root_domain,
                    "evidence_breakdown": top.evidence_breakdown if top else {},
                },
            },
        ))

        if self.knowledge_store and inv.root_cause:
            try:
                await self.knowledge_store.store_investigation(inv)
            except Exception as e:
                log.warning(f"V2: failed to store investigation in knowledge base: {e}")

        return inv

    async def _llm_call(self, messages: list[dict], json_mode: bool = False) -> dict | None:
        import asyncio
        import aiohttp

        async def _call_with_model(model: str) -> tuple[int, any]:
            body: dict = {
                "model": model,
                "messages": messages,
                "stream": False,
                "keep_alive": "10m",
                "options": {"temperature": 0.05, "num_ctx": 3072, "num_predict": 768},
            }
            if json_mode:
                body["format"] = "json"
            async with aiohttp.ClientSession() as session:
                async with session.post(
                    f"{OLLAMA_URL}/api/chat",
                    json=body,
                    timeout=aiohttp.ClientTimeout(total=OLLAMA_TIMEOUT),
                ) as resp:
                    return resp.status, (await resp.json() if resp.status == 200 else await resp.text())

        try:
            status, result = await _call_with_model(OLLAMA_MODEL)
            if status == 200:
                return result
            # Model not found — fall back to qwen2.5:3b if a different model was configured
            if status == 404 and OLLAMA_MODEL != "qwen2.5:3b":
                log.warning(f"Ollama model {OLLAMA_MODEL!r} not found (404) — falling back to qwen2.5:3b")
                status2, result2 = await _call_with_model("qwen2.5:3b")
                if status2 == 200:
                    return result2
                log.error(f"Ollama fallback model qwen2.5:3b also failed: {status2}: {result2}")
                return None
            log.error(f"Ollama error {status}: {result}")
            return None
        except asyncio.TimeoutError:
            log.error("Ollama narrator request timed out")
            return None
        except Exception as e:
            log.error(f"Ollama narrator request failed: {e}")
            return None

    async def _oidc_call(
        self, messages: list[dict], model: str, token: str, json_mode: bool = False
    ) -> dict | None:
        """Delegate to the existing OIDC Provider caller on the V1 orchestrator."""
        from agent.orchestrator import RCAOrchestrator
        tmp = RCAOrchestrator()
        return await tmp._oidc_chat(messages, model, token, json_mode)

    async def _set_phase(
        self, inv: Investigation, phase: InvestigationPhase, emit: Callable
    ) -> None:
        inv.phase = phase
        emit(StreamEvent(
            investigation_id=inv.id, type="phase_change",
            phase=phase, data=phase.value,
        ))

    def _annotate_from_go_context(
        self, inv: Investigation, go_context: dict[str, Any]
    ) -> None:
        if go_context.get("root_entity_label") and not inv.service:
            inv.service = go_context["root_entity_label"]

    def _build_timeline(
        self,
        go_context: dict[str, Any],
        temporal_sequence: list[dict],
    ) -> list[dict[str, str]]:
        timeline = []
        anchor = go_context.get("temporal_anchor")
        if anchor:
            timeline.append({
                "time": str(anchor),
                "event": f"Go engine: topology traversal anchored at "
                         f"{go_context.get('root_entity_label', 'root entity')}",
            })
        for entry in temporal_sequence[:5]:
            timeline.append({
                "time": str(entry.get("fired_at", "")),
                "event": f"{entry.get('alert_title', entry.get('source', 'alert'))} "
                         f"on {entry.get('entity_id', 'unknown')} "
                         f"(+{entry.get('seconds_after_first', 0)}s)",
            })
        return timeline
