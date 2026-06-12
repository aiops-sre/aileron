from __future__ import annotations
import math
from dataclasses import dataclass, field
from typing import Any

from .domain_classifier import InfraDomain

# Infrastructure level priors: lower level = more likely root cause.
# Physical → hypervisor → OS → k8s node → k8s workload → application
_INFRA_LEVEL_PRIOR: dict[str, float] = {
    "PHYSICAL":     0.90,
    "HYPERVISOR":   0.85,
    "OS":           0.80,
    "NODE":         0.75,
    "CLUSTER":      0.70,
    "NAMESPACE":    0.60,
    "DEPLOYMENT":   0.50,
    "POD":          0.45,
    "CONTAINER":    0.40,
    "SERVICE":      0.35,
    "APPLICATION":  0.30,
    "UNKNOWN":      0.25,
}


@dataclass
class InfraNode:
    entity_id: str
    label: str
    entity_type: str
    infra_level: str
    domain: str
    blast_radius_score: float = 0.0
    temporal_score: float = 0.0
    topo_depth: int = 0


@dataclass
class CausalEdge:
    from_id: str
    to_id: str
    edge_type: str
    weight: float
    hop_index: int


@dataclass
class CausalGraphResult:
    root_cause_node: InfraNode | None
    confidence: float
    path_to_root: list[InfraNode]
    causal_edges: list[CausalEdge]
    all_nodes: list[InfraNode]
    reasoning: list[str]


class CausalGraph:
    def __init__(self) -> None:
        self.nodes: dict[str, InfraNode] = {}
        self.edges: list[CausalEdge] = []

    def add_node(self, node: InfraNode) -> None:
        self.nodes[node.entity_id] = node

    def add_edge(self, edge: CausalEdge) -> None:
        self.edges.append(edge)

    def find_root_cause(self) -> CausalGraphResult:
        if not self.nodes:
            return CausalGraphResult(
                root_cause_node=None, confidence=0.0,
                path_to_root=[], causal_edges=[], all_nodes=[],
                reasoning=["No nodes in causal graph"],
            )

        scores: dict[str, float] = {}
        for nid, node in self.nodes.items():
            infra_prior = _INFRA_LEVEL_PRIOR.get(node.infra_level.upper(), 0.25)
            blast_weight = min(node.blast_radius_score, 1.0)
            temporal_bonus = node.temporal_score * 0.2

            # Path confidence: nodes with more edges pointing TO them are more central.
            in_degree = sum(1 for e in self.edges if e.to_id == nid)
            path_conf = 1.0 - math.exp(-in_degree * 0.5)

            score = infra_prior * 0.4 + blast_weight * 0.35 + path_conf * 0.15 + temporal_bonus
            scores[nid] = score

        best_id = max(scores, key=lambda k: scores[k])
        best_node = self.nodes[best_id]
        best_score = scores[best_id]

        # Reconstruct path from best_node back to the alert entity (lowest infra level).
        path = self._reconstruct_path(best_id)
        path_edges = [e for e in self.edges if e.from_id in {n.entity_id for n in path}]

        reasoning = [
            f"Root cause: {best_node.label} ({best_node.entity_type}) "
            f"at infra level {best_node.infra_level}",
            f"Score breakdown: infra_prior={_INFRA_LEVEL_PRIOR.get(best_node.infra_level.upper(), 0.25):.2f}, "
            f"blast={best_node.blast_radius_score:.2f}, temporal={best_node.temporal_score:.2f}",
            f"Total graph score: {best_score:.3f} (nodes={len(self.nodes)}, edges={len(self.edges)})",
        ]

        return CausalGraphResult(
            root_cause_node=best_node,
            confidence=min(best_score, 0.97),
            path_to_root=path,
            causal_edges=path_edges,
            all_nodes=list(self.nodes.values()),
            reasoning=reasoning,
        )

    def _reconstruct_path(self, root_id: str) -> list[InfraNode]:
        visited = {root_id}
        path = [self.nodes[root_id]]
        # Walk upstream along DEPENDS_ON edges (from_id depends on to_id) starting
        # from the root cause node. Edges are stored as: dependent → dependency, so
        # to find what depends on root_id we look for edges where to_id == root_id.
        frontier = {root_id}
        for _ in range(10):
            next_frontier = set()
            for e in self.edges:
                if e.to_id in frontier and e.from_id not in visited:
                    visited.add(e.from_id)
                    next_frontier.add(e.from_id)
                    if e.from_id in self.nodes:
                        path.append(self.nodes[e.from_id])
            if not next_frontier:
                break
            frontier = next_frontier
        return path


class CausalGraphEngine:
    """Builds a CausalGraph from Go engine context + DAG tool outputs and finds the root cause."""

    def build_and_solve(
        self,
        go_context: dict | None,
        dag_outputs: dict[str, str],
        temporal_sequence: list[dict] | None = None,
    ) -> CausalGraphResult:
        graph = CausalGraph()
        ctx = go_context or {}

        # --- Populate from Go topology traversal ---
        causal_chain = ctx.get("causal_chain") or []
        for link in causal_chain:
            for side in ("from", "to"):
                node_id = link.get(f"{side}_id", "")
                node_label = link.get(f"{side}_label", node_id)
                if node_id and node_id not in graph.nodes:
                    graph.add_node(InfraNode(
                        entity_id=node_id,
                        label=node_label,
                        entity_type="infrastructure",
                        infra_level="UNKNOWN",
                        domain=ctx.get("topo_domain", "UNKNOWN"),
                    ))
            if link.get("from_id") and link.get("to_id"):
                graph.add_edge(CausalEdge(
                    from_id=link["from_id"],
                    to_id=link["to_id"],
                    edge_type=link.get("edge_type", "DEPENDS_ON"),
                    weight=link.get("weight", 1.0),
                    hop_index=link.get("hop_index", 0),
                ))

        # Root entity from Go engine gets elevated blast_radius_score.
        root_id = ctx.get("root_entity_id", "")
        if root_id:
            if root_id not in graph.nodes:
                graph.add_node(InfraNode(
                    entity_id=root_id,
                    label=ctx.get("root_entity_label", root_id),
                    entity_type=ctx.get("root_entity_type", "infrastructure"),
                    infra_level="NODE",
                    domain=ctx.get("topo_domain", "UNKNOWN"),
                ))
            graph.nodes[root_id].blast_radius_score = ctx.get("root_confidence", 0.8)

        # Blast radius entities get moderate scores.
        propagation = ctx.get("propagation_map") or {}
        for entity_id in ctx.get("blast_radius_entity_ids") or []:
            if entity_id not in graph.nodes:
                graph.add_node(InfraNode(
                    entity_id=entity_id,
                    label=entity_id,
                    entity_type="infrastructure",
                    infra_level="UNKNOWN",
                    domain=ctx.get("topo_domain", "UNKNOWN"),
                ))
            graph.nodes[entity_id].blast_radius_score = propagation.get(entity_id, 0.3)

        # Go hypotheses — treat top hypotheses as additional candidate nodes.
        for hyp in (ctx.get("hypotheses") or [])[:5]:
            eid = hyp.get("entity_id", "")
            if eid and eid not in graph.nodes:
                graph.add_node(InfraNode(
                    entity_id=eid,
                    label=hyp.get("entity_label", eid),
                    entity_type=hyp.get("entity_type", "unknown"),
                    infra_level="UNKNOWN",
                    domain=hyp.get("domain", "UNKNOWN"),
                ))
            if eid and eid in graph.nodes:
                graph.nodes[eid].blast_radius_score = max(
                    graph.nodes[eid].blast_radius_score,
                    hyp.get("confidence", 0.0),
                )

        # Temporal scores: first-seen alerts get higher temporal score.
        if temporal_sequence:
            for i, entry in enumerate(temporal_sequence[:20]):
                eid = entry.get("entity_id", "")
                if eid and eid in graph.nodes:
                    # Earlier in sequence → higher temporal priority.
                    temporal_score = 1.0 - (i / max(len(temporal_sequence), 1))
                    graph.nodes[eid].temporal_score = max(
                        graph.nodes[eid].temporal_score, temporal_score
                    )

        if not graph.nodes:
            return CausalGraphResult(
                root_cause_node=None, confidence=0.0,
                path_to_root=[], causal_edges=[], all_nodes=[],
                reasoning=["Go context contained no topology nodes — insufficient data for causal graph"],
            )

        return graph.find_root_cause()
