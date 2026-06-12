from __future__ import annotations
import logging
from typing import Any, Callable

from models.schemas import Investigation, InvestigationPhase, StreamEvent
from engine.rca_scorer import RCAScoringResult
from engine.causal_graph import CausalGraphResult

log = logging.getLogger(__name__)

# The narrator NEVER determines root cause — it receives a pre-computed structured
# result and can ONLY write prose, describe the causal chain, and synthesise
# remediation steps from the evidence already gathered.
NARRATOR_SYSTEM_PROMPT = """\
You are an SRE incident narrator. You do NOT determine root cause — that has already been \
computed by deterministic infrastructure analysis engines. Your only job is to:

1. Write a clear, concise prose summary of the incident based on the provided structured findings.
2. Explain the causal chain in plain language that an on-call engineer can act on immediately.
3. Synthesise remediation steps from the evidence provided.

CRITICAL RULES:
- Do NOT contradict the provided root cause entity or confidence score.
- Do NOT add speculation or new hypotheses — only narrate what the data shows.
- Do NOT mention that you are an AI or reference LLM processing.
- Keep the summary under 200 words.
- Remediation steps must be concrete and actionable (specific commands or checks).
- If confidence < 0.4, acknowledge uncertainty explicitly.
"""


def build_narrator_prompt(
    scored_result: RCAScoringResult,
    graph_result: CausalGraphResult,
    dag_outputs: dict[str, str],
    go_context: dict[str, Any],
    historical_matches: list[dict],
) -> str:
    top = scored_result.top_hypothesis
    root_section = ""
    if top:
        root_section = f"""
ROOT CAUSE (deterministic engine output — do not contradict):
  Entity: {top.entity_label} ({top.entity_type})
  Domain: {top.domain}
  Confidence: {top.normalized_score:.1%}
  Evidence: go_engine={top.evidence_breakdown.get('go_engine', 0):.2f}, \
graph={top.evidence_breakdown.get('graph', 0):.2f}, \
blast={top.evidence_breakdown.get('blast', 0):.2f}, \
temporal={top.evidence_breakdown.get('temporal', 0):.2f}
"""

    chain_section = ""
    if graph_result.path_to_root:
        path_str = " → ".join(n.label for n in graph_result.path_to_root[:8])
        chain_section = f"\nCAUSAL CHAIN: {path_str}\n"

    context_section = f"""
GO ENGINE CONTEXT:
  Domain: {go_context.get('domain', 'unknown')}
  Blast radius: {go_context.get('blast_radius_size', 0)} entities
  Topo depth: {go_context.get('topo_depth', 0)}
  Reasoning: {'; '.join((go_context.get('topo_reasoning') or [])[:3])}
"""

    historical_section = ""
    if historical_matches:
        best = historical_matches[0]
        historical_section = f"""
MOST SIMILAR PAST INCIDENT (for remediation reference):
  Title: {best.get('alert_title', 'unknown')}
  Root cause: {best.get('root_cause_summary', '')}
  Remediation: {str(best.get('remediation', ''))[:400]}
  Similarity: {best.get('combined_score', best.get('similarity_score', 0)):.1%}
"""

    tool_summary = ""
    relevant_tools = {k: v for k, v in dag_outputs.items() if v and "error" not in v.lower()[:20]}
    if relevant_tools:
        snippets = []
        for tool_name, output in list(relevant_tools.items())[:4]:
            snippets.append(f"  [{tool_name}]: {output[:300]}")
        tool_summary = "\nTOOL EVIDENCE SNIPPETS:\n" + "\n".join(snippets)

    return f"""\
{root_section}{chain_section}{context_section}{historical_section}{tool_summary}

Based on the above deterministic analysis, write:
1. A concise incident summary (2-3 sentences, plain language, under 150 words).
2. The causal chain explanation (1-2 sentences).
3. Exactly 3-5 concrete remediation steps (numbered list, each with a specific command or check).
4. Confidence assessment (1 sentence).

Respond in valid JSON with this structure:
{{
  "summary": "...",
  "causal_explanation": "...",
  "remediation_steps": [
    {{"step": 1, "action": "...", "command": "kubectl ...", "risk": "low"}},
    ...
  ],
  "confidence_assessment": "..."
}}
"""


async def narrate(
    inv: Investigation,
    scored_result: RCAScoringResult,
    graph_result: CausalGraphResult,
    dag_outputs: dict[str, str],
    go_context: dict[str, Any],
    historical_matches: list[dict],
    llm_caller,  # async callable(messages, json_mode=True) → dict | None
    emit: Callable[[StreamEvent], None],
) -> dict[str, Any]:
    """Call the LLM in narrator-only mode with pre-computed structured results."""
    user_prompt = build_narrator_prompt(
        scored_result, graph_result, dag_outputs, go_context, historical_matches
    )
    messages = [
        {"role": "system", "content": NARRATOR_SYSTEM_PROMPT},
        {"role": "user", "content": user_prompt},
    ]

    emit(StreamEvent(
        investigation_id=inv.id,
        type="thought",
        phase=inv.phase,
        data="[Narrator] Writing incident summary from deterministic engine results...",
    ))

    import json as _json
    import re

    response = await llm_caller(messages, json_mode=True)
    content = (response or {}).get("message", {}).get("content", "")

    if not content:
        log.warning(f"Narrator returned empty content for investigation {inv.id}")
        return {}

    # Parse JSON response
    try:
        return _json.loads(content)
    except Exception:
        match = re.search(r"\{[\s\S]*\}", content)
        if match:
            try:
                return _json.loads(match.group())
            except Exception:
                pass
    log.warning(f"Narrator: could not parse JSON from LLM response (inv={inv.id})")
    return {"summary": content[:600], "remediation_steps": [], "causal_explanation": ""}
