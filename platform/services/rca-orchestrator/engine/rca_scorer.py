from __future__ import annotations
import math
from dataclasses import dataclass, field
from typing import Any

from .causal_graph import CausalGraphResult

# Evidence source weights — must sum to 1.0
EVIDENCE_WEIGHTS: dict[str, float] = {
    "go_engine":  0.40,   # Go deterministic topology + hypothesis engine
    "graph":      0.25,   # Python causal graph construction
    "blast":      0.15,   # Blast radius (downstream impact count)
    "temporal":   0.10,   # Temporal ordering (first-seen advantage)
    "domain":     0.05,   # Domain classification confidence
    "historical": 0.05,   # Historical similarity match
}


@dataclass
class ScoredHypothesis:
    entity_id: str
    entity_label: str
    entity_type: str
    domain: str
    raw_score: float
    normalized_score: float
    evidence_breakdown: dict[str, float]
    description: str


@dataclass
class RCAScoringResult:
    top_hypothesis: ScoredHypothesis | None
    all_hypotheses: list[ScoredHypothesis]
    confidence: float
    reasoning: list[str]


class RCAScorer:
    """Fuses 6 evidence sources into a ranked list of scored root-cause hypotheses."""

    def score_and_rank(
        self,
        go_context: dict[str, Any] | None,
        graph_result: CausalGraphResult,
        classification_confidence: float,
        historical_matches: list[dict] | None = None,
    ) -> RCAScoringResult:
        ctx = go_context or {}
        candidates: dict[str, dict[str, float]] = {}

        # --- Go engine hypotheses ---
        for h in ctx.get("hypotheses") or []:
            eid = h.get("entity_id", "")
            if not eid:
                continue
            if eid not in candidates:
                candidates[eid] = {"go_engine": 0, "graph": 0, "blast": 0,
                                   "temporal": 0, "domain": 0, "historical": 0,
                                   "_label": h.get("entity_label", eid),
                                   "_type": h.get("entity_type", "unknown"),
                                   "_domain": h.get("domain", "UNKNOWN")}
            candidates[eid]["go_engine"] = h.get("confidence", 0.0)

        # Go root entity directly
        root_id = ctx.get("root_entity_id", "")
        if root_id:
            if root_id not in candidates:
                candidates[root_id] = {"go_engine": 0, "graph": 0, "blast": 0,
                                       "temporal": 0, "domain": 0, "historical": 0,
                                       "_label": ctx.get("root_entity_label", root_id),
                                       "_type": ctx.get("root_entity_type", "unknown"),
                                       "_domain": ctx.get("topo_domain", "UNKNOWN")}
            go_conf = ctx.get("root_confidence", 0.0)
            candidates[root_id]["go_engine"] = max(candidates[root_id]["go_engine"], go_conf)

        # --- Causal graph result ---
        if graph_result.root_cause_node:
            n = graph_result.root_cause_node
            if n.entity_id not in candidates:
                candidates[n.entity_id] = {"go_engine": 0, "graph": 0, "blast": 0,
                                           "temporal": 0, "domain": 0, "historical": 0,
                                           "_label": n.label, "_type": n.entity_type,
                                           "_domain": n.domain}
            candidates[n.entity_id]["graph"] = graph_result.confidence

        # All graph nodes get partial graph score by their blast_radius_score.
        for node in graph_result.all_nodes:
            if node.entity_id not in candidates:
                candidates[node.entity_id] = {"go_engine": 0, "graph": 0, "blast": 0,
                                              "temporal": 0, "domain": 0, "historical": 0,
                                              "_label": node.label, "_type": node.entity_type,
                                              "_domain": node.domain}
            if node.entity_id != (graph_result.root_cause_node.entity_id if graph_result.root_cause_node else ""):
                candidates[node.entity_id]["graph"] = max(
                    candidates[node.entity_id]["graph"],
                    graph_result.confidence * node.blast_radius_score,
                )

        # --- Blast radius scores ---
        blast_size = max(ctx.get("blast_radius_size", 0), 1)
        propagation = ctx.get("propagation_map") or {}
        for eid, prop_score in propagation.items():
            if eid in candidates:
                candidates[eid]["blast"] = prop_score
            elif eid == root_id:
                pass  # already handled above

        # Normalised blast contribution: larger blast = higher root-cause likelihood.
        blast_norm = min(blast_size / 20.0, 1.0)  # cap at 20 impacted entities
        if root_id and root_id in candidates:
            candidates[root_id]["blast"] = max(candidates[root_id]["blast"], blast_norm)

        # --- Temporal scores ---
        for node in graph_result.all_nodes:
            if node.entity_id in candidates:
                candidates[node.entity_id]["temporal"] = node.temporal_score

        # --- Domain confidence (apply fully only to domain-matching candidates) ---
        ctx_domain = (go_context or {}).get("domain", "") or ""
        for eid in candidates:
            cand_domain = candidates[eid].get("_domain", "UNKNOWN").upper()
            if ctx_domain and cand_domain == ctx_domain.upper():
                candidates[eid]["domain"] = classification_confidence
            else:
                candidates[eid]["domain"] = classification_confidence * 0.2

        # --- Historical match bonus ---
        if historical_matches:
            best_match = historical_matches[0] if historical_matches else {}
            match_entity = best_match.get("root_cause_component", "")
            match_conf = float(best_match.get("similarity_score", 0.0))
            for eid, c in candidates.items():
                if match_entity and c["_label"].lower() in match_entity.lower():
                    candidates[eid]["historical"] = match_conf

        # --- Fuse with EVIDENCE_WEIGHTS ---
        raw_scores: list[tuple[str, float]] = []
        for eid, ev in candidates.items():
            score = sum(EVIDENCE_WEIGHTS[k] * ev.get(k, 0.0) for k in EVIDENCE_WEIGHTS)
            raw_scores.append((eid, score))

        if not raw_scores:
            return RCAScoringResult(
                top_hypothesis=None, all_hypotheses=[],
                confidence=0.0,
                reasoning=["No candidate root-cause entities found from any evidence source"],
            )

        # Softmax normalisation
        raw_vals = [s for _, s in raw_scores]
        exp_vals = [math.exp(v * 2) for v in raw_vals]  # temperature=2 preserves rank signal without extreme winner-takes-all
        total = sum(exp_vals)
        norm_vals = [e / total for e in exp_vals]

        scored: list[ScoredHypothesis] = []
        for (eid, raw), norm in zip(raw_scores, norm_vals):
            ev = candidates[eid]
            scored.append(ScoredHypothesis(
                entity_id=eid,
                entity_label=ev["_label"],
                entity_type=ev["_type"],
                domain=ev["_domain"],
                raw_score=raw,
                normalized_score=norm,
                evidence_breakdown={k: ev.get(k, 0.0) for k in EVIDENCE_WEIGHTS},
                description=f"{ev['_label']} ({ev['_type']}) — domain: {ev['_domain']}",
            ))

        scored.sort(key=lambda h: h.normalized_score, reverse=True)
        top = scored[0] if scored else None

        reasoning = []
        if top:
            reasoning.append(f"Top candidate: {top.entity_label} ({top.entity_type}), "
                             f"normalised score: {top.normalized_score:.3f}")
            reasoning.append(f"Evidence: go={top.evidence_breakdown['go_engine']:.2f}, "
                             f"graph={top.evidence_breakdown['graph']:.2f}, "
                             f"blast={top.evidence_breakdown['blast']:.2f}, "
                             f"temporal={top.evidence_breakdown['temporal']:.2f}")

        return RCAScoringResult(
            top_hypothesis=top,
            all_hypotheses=scored[:10],
            confidence=top.normalized_score if top else 0.0,
            reasoning=reasoning,
        )
