from __future__ import annotations
import json
import logging
from dataclasses import dataclass
from typing import Any

log = logging.getLogger(__name__)


@dataclass
class TopologySignature:
    """A compact fingerprint of an incident's topology context for similarity matching."""
    root_entity_type: str
    domain: str
    infra_levels_in_chain: list[str]
    blast_radius_size_bucket: str    # "small" (<5), "medium" (5-20), "large" (>20)
    causal_depth: int


def _make_signature(go_context: dict[str, Any]) -> TopologySignature:
    chain = go_context.get("causal_chain") or []
    levels = list({link.get("from_label", "")[:20] for link in chain if link.get("from_label")})
    blast = go_context.get("blast_radius_size", 0)
    bucket = "small" if blast < 5 else ("medium" if blast < 20 else "large")
    return TopologySignature(
        root_entity_type=go_context.get("root_entity_type", "unknown"),
        domain=go_context.get("topo_domain", go_context.get("domain", "UNKNOWN")),
        infra_levels_in_chain=levels,
        blast_radius_size_bucket=bucket,
        causal_depth=go_context.get("topo_depth", len(chain)),
    )


def topology_similarity(sig_a: TopologySignature, stored: dict[str, Any]) -> float:
    """Compute topology signature similarity in [0, 1].

    70% weight on topology structure, 30% on semantic similarity.
    The stored dict is the Weaviate properties of a past incident.
    """
    score = 0.0

    # Domain match: 0.25
    # Use cluster/namespace as a locality proxy; root_cause_category as domain proxy.
    # Neither is a perfect match for sig_a.domain but they are the only domain signals
    # stored in Weaviate.  Checking alert_title (the previous behaviour) was wrong.
    stored_domain = stored.get("cluster", stored.get("namespace", ""))
    stored_category = stored.get("root_cause_category", "").lower()
    if sig_a.domain != "UNKNOWN" and (
        sig_a.domain.lower() in stored_category or stored_category in sig_a.domain.lower()
    ):
        score += 0.25
    elif sig_a.domain != "UNKNOWN" and stored_domain:
        # Same infrastructure locality (cluster/namespace) — partial credit
        score += 0.10

    # Root entity type match: 0.20
    stored_component = stored.get("root_cause_component", "")
    if sig_a.root_entity_type.lower() in stored_component.lower():
        score += 0.20

    # Blast radius bucket match: 0.15
    stored_rc_summary = stored.get("root_cause_summary", "")
    if sig_a.blast_radius_size_bucket == "large" and any(
        kw in stored_rc_summary.lower() for kw in ("cascade", "widespread", "multiple", "many")
    ):
        score += 0.15
    elif sig_a.blast_radius_size_bucket == "small":
        score += 0.10

    # Causal depth match: 0.10
    # Stored incidents with deep chains get a bonus when current chain is also deep.
    if sig_a.causal_depth >= 3 and "upstream" in stored_rc_summary.lower():
        score += 0.10

    return min(score, 1.0)


async def retrieve_topology_aware_historical(
    go_context: dict[str, Any],
    alert_title: str,
    alert_body: dict[str, Any],
    knowledge_store,  # KnowledgeStore instance
    limit: int = 5,
) -> list[dict[str, Any]]:
    """Hybrid retrieval: 70% topology signature similarity + 30% semantic similarity.

    Prevents false RCA anchoring caused by semantically-similar-but-topologically-different incidents.
    For example, "disk full" in a small single-node incident shouldn't anchor RCA for a large
    cascade storage failure.
    """
    if not knowledge_store or not knowledge_store.client:
        return []

    # 1. Semantic search (standard vector search)
    try:
        semantic_results = await knowledge_store.search_similar(
            alert_title, alert_body, limit=limit * 3
        )
    except Exception as e:
        log.warning(f"Topology-aware retrieval: semantic search failed: {e}")
        return []

    if not semantic_results:
        return []

    if not go_context or (not go_context.get("root_entity_id") and not go_context.get("causal_chain")):
        # No topology context — fall back to pure semantic results.
        return semantic_results[:limit]

    # 2. Re-rank by combined topology + semantic score.
    sig = _make_signature(go_context)

    reranked = []
    for match in semantic_results:
        topo_score = topology_similarity(sig, match)
        semantic_score = match.get("similarity_score", 0.0)
        combined = 0.70 * topo_score + 0.30 * semantic_score
        match = dict(match)
        match["combined_score"] = round(combined, 3)
        match["topo_score"] = round(topo_score, 3)
        reranked.append(match)

    reranked.sort(key=lambda m: m["combined_score"], reverse=True)
    return reranked[:limit]
