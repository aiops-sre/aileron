from __future__ import annotations
import asyncio
import logging
import time
from dataclasses import dataclass, field
from typing import Any, Callable

from models.schemas import Investigation, InvestigationPhase, ToolCall, StreamEvent
from tools import TOOL_MAP
from .domain_classifier import InfraDomain, DomainClassification

log = logging.getLogger(__name__)

# Per-domain mandatory tool execution sequences.
# Each entry is (tool_name, arg_builder_key) — arg_builder_key maps to a
# function that extracts the right arguments from the investigation context.
_DOMAIN_STAGES: dict[InfraDomain, list[tuple[str, str]]] = {
    InfraDomain.KUBERNETES: [
        ("get_pod_status",        "service_namespace"),
        ("get_deployment_status", "service_namespace"),
        ("get_k8s_events",        "namespace"),
        ("get_pvc_status",        "namespace"),
        ("get_node_status",       "cluster"),
        ("get_recent_changes",    "namespace"),
    ],
    InfraDomain.STORAGE: [
        ("get_pvc_status",        "namespace"),
        ("get_k8s_events",        "namespace"),
        ("get_topology_recursive","root_entity"),
        ("get_blast_radius_deep", "root_entity"),
    ],
    InfraDomain.NETWORK: [
        ("get_k8s_events",        "namespace"),
        ("get_topology_recursive","root_entity"),
        ("get_blast_radius_deep", "root_entity"),
        ("get_recent_changes",    "namespace"),
    ],
    InfraDomain.COMPUTE: [
        ("get_pod_status",        "service_namespace"),
        ("get_resource_quota",    "namespace"),
        ("get_k8s_events",        "namespace"),
        ("get_node_status",       "cluster"),
        ("get_topology_recursive","root_entity"),
    ],
    InfraDomain.DATABASE: [
        ("get_topology_recursive","root_entity"),
        ("get_blast_radius_deep", "root_entity"),
        ("query_temporal_propagation", "incident"),
        ("get_recent_changes",    "namespace"),
    ],
    InfraDomain.VIRTUALIZATION: [
        ("get_cloudstack_vms",    "cluster"),
        ("get_cloudstack_hosts",  "cluster"),
        ("get_topology_recursive","root_entity"),
        ("get_recent_changes",    "namespace"),
    ],
    InfraDomain.UNKNOWN: [
        ("get_pod_status",        "service_namespace"),
        ("get_k8s_events",        "namespace"),
        ("get_topology_recursive","root_entity"),
        ("get_recent_changes",    "namespace"),
    ],
}


@dataclass
class DAGResult:
    domain: InfraDomain
    stages_run: list[str]
    tool_outputs: dict[str, str]   # tool_name → raw result string
    tool_errors: dict[str, str]    # tool_name → error string
    duration_ms: int
    skipped_tools: list[str] = field(default_factory=list)


class InvestigationDAGEngine:
    """Deterministic investigation DAG — executes mandatory tool sequences per domain.

    Unlike ReAct (which lets the LLM pick tools), this runs the full domain-specific
    sequence regardless of intermediate results so nothing is silently skipped.
    """

    async def execute(
        self,
        inv: Investigation,
        go_context: dict[str, Any] | None,
        classification: DomainClassification,
        emit: Callable[[StreamEvent], None],
    ) -> DAGResult:
        t0 = time.monotonic()
        domain = classification.domain
        stages = _DOMAIN_STAGES.get(domain, _DOMAIN_STAGES[InfraDomain.UNKNOWN])

        outputs: dict[str, str] = {}
        errors: dict[str, str] = {}
        skipped: list[str] = []
        ran: list[str] = []

        for tool_name, arg_key in stages:
            tool = TOOL_MAP.get(tool_name)
            if tool is None:
                skipped.append(tool_name)
                continue

            args = self._build_args(tool_name, arg_key, inv, go_context)
            emit(StreamEvent(
                investigation_id=inv.id,
                type="tool_call",
                phase=inv.phase,
                data={"tool": tool_name, "args": args, "dag_stage": True},
            ))

            try:
                result, duration_ms = await asyncio.wait_for(
                    tool.run(**args), timeout=30
                )
                outputs[tool_name] = result
                ran.append(tool_name)
                inv.tool_calls.append(ToolCall(
                    tool=tool_name, args=args,
                    result=result[:500], duration_ms=duration_ms,
                ))
                emit(StreamEvent(
                    investigation_id=inv.id,
                    type="tool_result",
                    phase=inv.phase,
                    data={"tool": tool_name, "result_preview": result[:200], "duration_ms": duration_ms},
                ))
            except asyncio.TimeoutError:
                errors[tool_name] = "timeout"
                log.warning(f"DAG tool {tool_name} timed out for investigation {inv.id}")
            except Exception as e:
                errors[tool_name] = str(e)
                log.warning(f"DAG tool {tool_name} error for investigation {inv.id}: {e}")

        return DAGResult(
            domain=domain,
            stages_run=ran,
            tool_outputs=outputs,
            tool_errors=errors,
            duration_ms=int((time.monotonic() - t0) * 1000),
            skipped_tools=skipped,
        )

    def _build_args(
        self,
        tool_name: str,
        arg_key: str,
        inv: Investigation,
        go_context: dict[str, Any] | None,
    ) -> dict[str, Any]:
        ctx = go_context or {}
        ns = inv.namespace or ""
        cluster = inv.cluster or ""
        service = inv.service or ""
        root_entity = ctx.get("root_entity_label") or ctx.get("root_entity_id") or service or ""

        if arg_key == "service_namespace":
            # Each tool has a different parameter name — map explicitly so no tool
            # receives a kwarg it doesn't accept (e.g. service_name on get_pod_status).
            if tool_name == "get_deployment_status":
                return {"namespace": ns, "deployment_name": service or ""}
            elif tool_name == "get_pod_status":
                label_sel = f"app={service}" if service else ""
                return {"namespace": ns, "label_selector": label_sel}
            else:
                # Fallback: namespace only — safe for any namespace-scoped tool.
                return {"namespace": ns}
        if arg_key == "namespace":
            return {"namespace": ns}
        if arg_key == "cluster":
            return {"cluster": cluster}
        if arg_key == "root_entity":
            return {"entity_id": ctx.get("root_entity_id") or root_entity,
                    "entity_label": root_entity}
        if arg_key == "incident":
            return {"incident_id": inv.incident_id or "", "namespace": ns}

        # Generic fallback — namespace is always safe; don't pass service_name
        # as a generic kwarg since most tools don't declare it.
        return {"namespace": ns}
