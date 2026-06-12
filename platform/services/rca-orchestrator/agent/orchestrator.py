from __future__ import annotations
import base64
import hmac
import json
import logging
import os
import asyncio
import struct
import time
from hashlib import sha1
from typing import AsyncGenerator, Any, Callable
import aiohttp

from models.schemas import (
    Investigation, InvestigationPhase, ToolCall, Hypothesis, RootCause,
    RemediationStep, StreamEvent,
)
from tools import TOOL_MAP, ALL_TOOLS, CORE_TOOLS
from agent.prompts import SYSTEM_PROMPT, RCA_EXTRACTION_PROMPT, SIMILAR_INCIDENTS_PROMPT

log = logging.getLogger(__name__)

OLLAMA_URL = os.getenv("OLLAMA_URL", "http://ollama.alert-engine-poc.svc.cluster.local:11434")
OLLAMA_MODEL = os.getenv("OLLAMA_MODEL", "qwen2.5:3b")
MAX_TOOL_ROUNDS = int(os.getenv("MAX_TOOL_ROUNDS", "3"))
OLLAMA_TIMEOUT = int(os.getenv("OLLAMA_TIMEOUT", "240"))

# Endor (Apple internal LLM gateway) configuration — all values from env vars
_ENDOR_CLIENT_ID = os.getenv("ENDOR_CLIENT_ID", "")
_ENDOR_CLIENT_SECRET = os.getenv("ENDOR_CLIENT_SECRET", "")
_ENDOR_TOTP_SECRET = os.getenv("ENDOR_TOTP_SECRET", "")  # base32 seed
_ENDOR_APP_ID_KEY = os.getenv("ENDOR_APP_ID_KEY", "")
_ENDOR_ACCOUNT_NAME = os.getenv("ENDOR_ACCOUNT_NAME", "")
_ENDOR_ACCOUNT_PASSWORD = os.getenv("ENDOR_ACCOUNT_PASSWORD", "")
_ENDOR_OIDC_APP_ID = os.getenv("ENDOR_OIDC_APP_ID", "")
_ENDOR_DEVICE_ID = int(os.getenv("ENDOR_DEVICE_ID", "1"))
_ENDOR_RESOURCE_SERVER = os.getenv("ENDOR_RESOURCE_SERVER", "https://api.endor.apple.com/")
_ENDOR_DEFAULT_MODEL = os.getenv("ENDOR_DEFAULT_MODEL", "gemini-2.5-flash")


class RCAOrchestrator:
    def __init__(self, knowledge_store=None):
        self.knowledge_store = knowledge_store

    async def investigate(
        self, inv: Investigation, emit: Callable[[StreamEvent], None]
    ) -> Investigation:
        """Run the full multi-phase investigation. Emits stream events throughout."""
        try:
            await self._phase(inv, InvestigationPhase.context_gathering, emit)
            await self._phase(inv, InvestigationPhase.hypothesis_formation, emit)
            await self._phase(inv, InvestigationPhase.evidence_collection, emit)
            await self._phase(inv, InvestigationPhase.root_cause_analysis, emit)
            await self._phase(inv, InvestigationPhase.remediation_planning, emit)
            inv.phase = InvestigationPhase.completed
        except Exception as e:
            log.exception(f"Investigation {inv.id} failed")
            inv.phase = InvestigationPhase.failed
            emit(StreamEvent(investigation_id=inv.id, type="error", phase=inv.phase, data=str(e)))
        return inv

    async def _phase(self, inv: Investigation, phase: InvestigationPhase, emit: Callable):
        inv.phase = phase
        emit(StreamEvent(investigation_id=inv.id, type="phase_change", phase=phase, data=phase.value))

    async def run_agent_loop(
        self, inv: Investigation, emit: Callable[[StreamEvent], None]
    ) -> Investigation:
        """Main ReAct agent loop using Ollama tool calling or Floodgate Claude."""
        from learning.knowledge_store import KnowledgeStore
        ks = self.knowledge_store

        use_floodgate = inv.llm_provider == "floodgate" and inv.llm_token
        use_endor = inv.llm_provider == "endor"
        if use_floodgate:
            log.info(f"Investigation {inv.id}: using Floodgate {inv.llm_model}")
        elif use_endor:
            log.info(f"Investigation {inv.id}: using Endor {inv.llm_model or _ENDOR_DEFAULT_MODEL}")
        else:
            log.info(f"Investigation {inv.id}: using local Ollama {OLLAMA_MODEL}")

        # Retrieve similar past incidents for RAG context
        similar = []
        if ks:
            try:
                similar = await ks.search_similar(inv.alert_title, inv.alert_body, limit=3)
                inv.similar_incidents = similar
            except Exception as e:
                log.warning(f"RAG retrieval failed: {e}")

        system = SYSTEM_PROMPT
        if similar:
            system += "\n\n" + SIMILAR_INCIDENTS_PROMPT.format(
                incidents=json.dumps(similar, indent=2, default=str)
            )

        # Build initial user message
        alert_context = {
            "title": inv.alert_title,
            "severity": inv.severity,
            "namespace": inv.namespace,
            "cluster": inv.cluster,
            "service": inv.service,
            "details": inv.alert_body,
        }
        messages = [
            {"role": "system", "content": system},
            {
                "role": "user",
                "content": f"Investigate this incident and find the root cause:\n\n{json.dumps(alert_context, indent=2, default=str)}\n\nStart by gathering context about the affected system.",
            },
        ]

        tools = [t.to_ollama_tool() for t in CORE_TOOLS]

        # ReAct loop
        await self._phase(inv, InvestigationPhase.context_gathering, emit)
        round_count = 0

        while round_count < MAX_TOOL_ROUNDS:
            round_count += 1
            if use_floodgate:
                # Floodgate Claude does not support Ollama-style tool calling natively;
                # run a single-shot summarisation round instead.
                response = await self._floodgate_chat(messages, inv.llm_model, inv.llm_token)
            elif use_endor:
                response = await self._endor_chat(messages, inv.llm_model)
            else:
                response = await self._ollama_chat(messages, tools)

            if not response:
                break

            content = response.get("message", {}).get("content", "")
            tool_calls_raw = response.get("message", {}).get("tool_calls", [])

            # Emit thought
            if content and content.strip():
                inv.thought_log.append(content)
                emit(StreamEvent(
                    investigation_id=inv.id,
                    type="thought",
                    phase=inv.phase,
                    data=content,
                ))

            # Advance phase based on round
            if round_count == 3:
                await self._phase(inv, InvestigationPhase.hypothesis_formation, emit)
            elif round_count == 5:
                await self._phase(inv, InvestigationPhase.evidence_collection, emit)

            # For Floodgate/Endor (no tool calls), stop after first substantive response
            if use_floodgate or use_endor or not tool_calls_raw:
                break

            # Execute tool calls (Ollama path only)
            messages.append({"role": "assistant", "content": content, "tool_calls": tool_calls_raw})
            tool_results = []

            for tc in tool_calls_raw:
                fn = tc.get("function", {})
                tool_name = fn.get("name", "")
                tool_args = fn.get("arguments", {})
                if isinstance(tool_args, str):
                    try:
                        tool_args = json.loads(tool_args)
                    except Exception:
                        tool_args = {}

                emit(StreamEvent(
                    investigation_id=inv.id,
                    type="tool_call",
                    phase=inv.phase,
                    data={"tool": tool_name, "args": tool_args},
                ))

                tool = TOOL_MAP.get(tool_name)
                if tool:
                    result, duration_ms = await tool.run(**tool_args)
                else:
                    result, duration_ms = f"Unknown tool: {tool_name}", 0

                inv.tool_calls.append(ToolCall(
                    tool=tool_name,
                    args=tool_args,
                    result=result[:500],
                    duration_ms=duration_ms,
                ))

                emit(StreamEvent(
                    investigation_id=inv.id,
                    type="tool_result",
                    phase=inv.phase,
                    data={"tool": tool_name, "result_preview": result[:200], "duration_ms": duration_ms},
                ))

                tool_results.append({
                    "role": "tool",
                    "content": result[:4000],  # Limit context size
                    "name": tool_name,
                })

            messages.extend(tool_results)

        # Phase: RCA extraction
        await self._phase(inv, InvestigationPhase.root_cause_analysis, emit)
        messages.append({"role": "user", "content": RCA_EXTRACTION_PROMPT})
        if use_floodgate:
            rca_response = await self._floodgate_chat(messages, inv.llm_model, inv.llm_token)
        elif use_endor:
            rca_response = await self._endor_chat(messages, inv.llm_model)
        else:
            rca_response = await self._ollama_chat(messages, tools=None, json_mode=True)
        rca_content = rca_response.get("message", {}).get("content", "") if rca_response else ""

        rca_data = {}
        if rca_content:
            # Strategy 1: direct parse
            try:
                rca_data = json.loads(rca_content)
            except json.JSONDecodeError:
                pass

            # Strategy 2: extract first {...} block (handles markdown fences)
            if not rca_data:
                import re
                match = re.search(r'\{[\s\S]*\}', rca_content)
                if match:
                    try:
                        rca_data = json.loads(match.group())
                    except Exception:
                        pass

        if rca_data:
            inv.summary = rca_data.get("summary", "")
            rc = rca_data.get("root_cause", {})
            inv.root_cause = RootCause(
                summary=rc.get("summary", inv.summary),
                component=rc.get("component", "unknown"),
                category=rc.get("category", "unknown"),
                confidence=float(rc.get("confidence", 0.5)),
                evidence=rc.get("evidence", []),
                timeline=rc.get("timeline", []),
            )
            inv.remediation = [
                RemediationStep(**step)
                for step in rca_data.get("remediation", [])
                if isinstance(step, dict)
            ]

        # Strategy 3: if no structured root cause, synthesise one from agent thoughts.
        # This handles small models (qwen2.5:3b) that generate good analysis in free text
        # but fail to output valid JSON on the extraction step.
        has_root_cause = bool(inv.root_cause and inv.root_cause.summary and inv.root_cause.summary.strip())
        if not has_root_cause and inv.thought_log:
            # Use the most substantive thought(s) as the root cause summary
            best_thought = max(inv.thought_log, key=len)
            summary = best_thought[:600].strip()
            if summary:
                inv.summary = summary
                inv.root_cause = RootCause(
                    summary=summary,
                    component="see investigation details",
                    category="infra_failure",
                    confidence=0.4,
                    evidence=[t[:200] for t in inv.thought_log if len(t) > 50][:3],
                )
                has_root_cause = True
                log.info(f"Investigation {inv.id}: used thought fallback for root cause ({len(summary)} chars)")

        await self._phase(inv, InvestigationPhase.remediation_planning, emit)
        if has_root_cause:
            inv.phase = InvestigationPhase.completed
        else:
            log.warning(f"Investigation {inv.id}: LLM returned no usable root cause — marking failed")
            inv.phase = InvestigationPhase.failed

        emit(StreamEvent(
            investigation_id=inv.id,
            type="result",
            phase=inv.phase,
            data={
                "summary": inv.summary,
                "root_cause": inv.root_cause.dict() if inv.root_cause else None,
                "remediation": [r.dict() for r in inv.remediation],
            },
        ))

        # Store in knowledge base for future learning
        if ks and inv.root_cause:
            try:
                await ks.store_investigation(inv)
            except Exception as e:
                log.warning(f"Failed to store investigation in knowledge base: {e}")

        return inv

    async def _ollama_chat(
        self, messages: list[dict], tools: list[dict] | None = None, json_mode: bool = False
    ) -> dict | None:
        body: dict[str, Any] = {
            "model": OLLAMA_MODEL,
            "messages": messages,
            "stream": False,
            "keep_alive": "10m",
            "options": {
                "temperature": 0.1,
                "num_ctx": 2048,
                "num_predict": 512,
            },
        }
        if tools:
            body["tools"] = tools
        if json_mode:
            body["format"] = "json"

        try:
            async with aiohttp.ClientSession() as session:
                async with session.post(
                    f"{OLLAMA_URL}/api/chat",
                    json=body,
                    timeout=aiohttp.ClientTimeout(total=OLLAMA_TIMEOUT),
                ) as resp:
                    if resp.status != 200:
                        log.error(f"Ollama error {resp.status}: {await resp.text()}")
                        return None
                    return await resp.json()
        except asyncio.TimeoutError:
            log.error("Ollama request timed out")
            return None
        except Exception as e:
            log.error(f"Ollama request failed: {e}")
            return None

    async def _floodgate_chat(
        self, messages: list[dict], model: str, token: str, json_mode: bool = False
    ) -> dict | None:
        """Call Claude via Floodgate proxy with the user-supplied OAuth token."""
        import ssl

        # Map short model names to full Claude model IDs
        model_map = {
            "claude-sonnet-4-6": "claude-sonnet-4-6-20250514",
            "claude-opus-4-7":   "claude-opus-4-7-20251101",
        }
        claude_model = model_map.get(model, model)

        # Convert messages to Anthropic format (extract system, build content blocks)
        system_content = ""
        anthropic_messages = []
        for msg in messages:
            if msg.get("role") == "system":
                system_content = msg.get("content", "")
            else:
                anthropic_messages.append({
                    "role": msg["role"],
                    "content": [{"type": "text", "text": msg.get("content", "")}],
                })

        if not anthropic_messages:
            return None

        body: dict[str, Any] = {
            "model": claude_model,
            "max_tokens": 2048,
            "messages": anthropic_messages,
        }
        if system_content:
            body["system"] = system_content

        ssl_ctx = ssl.create_default_context()
        # Apple's internal CA is not in the default trust store. Allow bypassing
        # certificate verification only when INTERNAL_TLS_INSECURE=true is explicitly
        # set (same flag used by the Go backend for internal service calls).
        if os.getenv("INTERNAL_TLS_INSECURE", "").lower() in ("true", "1", "yes"):
            ssl_ctx.check_hostname = False
            ssl_ctx.verify_mode = ssl.CERT_NONE
        # Load custom CA bundle if provided (e.g. Apple corporate CA)
        elif ca_bundle := os.getenv("SSL_CA_BUNDLE"):
            ssl_ctx.load_verify_locations(ca_bundle)
        connector = aiohttp.TCPConnector(ssl=ssl_ctx)

        try:
            async with aiohttp.ClientSession(connector=connector) as session:
                async with session.post(
                    "https://floodgate.g.apple.com/api/anthropic/v1/messages",
                    json=body,
                    headers={
                        "Authorization": f"Bearer {token}",
                        "anthropic-version": "2023-06-01",
                        "Content-Type": "application/json",
                        "User-Agent": "AlertHub-RCA/1.0",
                    },
                    timeout=aiohttp.ClientTimeout(total=120),
                ) as resp:
                    raw = await resp.text()
                    if resp.status != 200:
                        log.error(f"Floodgate error {resp.status}: {raw[:500]}")
                        return None
                    data = await resp.json(content_type=None)
                    # Normalise to Ollama-style response so the rest of run_agent_loop is unchanged
                    text = ""
                    for block in data.get("content", []):
                        if block.get("type") == "text":
                            text += block.get("text", "")
                    return {"message": {"role": "assistant", "content": text, "tool_calls": []}}
        except asyncio.TimeoutError:
            log.error("Floodgate request timed out")
            return None
        except Exception as e:
            log.error(f"Floodgate request failed: {e}")
            return None

    def _totp_now(self, secret_b32: str, step: int = 30, digits: int = 6) -> str:
        key = base64.b32decode(secret_b32.upper(), casefold=True)
        counter = int(time.time() // step)
        msg = struct.pack(">Q", counter)
        digest = hmac.new(key, msg=msg, digestmod=sha1).digest()
        offset = digest[-1] & 0x0F
        code = (
            ((digest[offset] & 0x7F) << 24)
            | ((digest[offset + 1] & 0xFF) << 16)
            | ((digest[offset + 2] & 0xFF) << 8)
            | (digest[offset + 3] & 0xFF)
        )
        return f"{code % (10 ** digits):0{digits}d}"

    async def _endor_chat(self, messages: list[dict], model: str | None = None) -> dict | None:
        """Call a model via Endor (Apple internal LLM gateway) using system account + TOTP auth."""
        if not _ENDOR_TOTP_SECRET:
            log.error("Endor: ENDOR_TOTP_SECRET not configured")
            return None
        if not _ENDOR_CLIENT_ID or not _ENDOR_CLIENT_SECRET:
            log.error("Endor: ENDOR_CLIENT_ID / ENDOR_CLIENT_SECRET not configured")
            return None

        model_id = model or _ENDOR_DEFAULT_MODEL

        # Endor uses a completion (not chat) API — serialise the message list into a prompt
        prompt_parts = []
        for msg in messages:
            role = msg.get("role", "user")
            content = msg.get("content", "")
            if role == "system":
                prompt_parts.append(f"<system>\n{content}\n</system>")
            elif role == "user":
                prompt_parts.append(f"<user>\n{content}\n</user>")
            elif role == "assistant":
                prompt_parts.append(f"<assistant>\n{content}\n</assistant>")
        prompt = "\n\n".join(prompt_parts)

        def _call_sync() -> str:
            from interlinked.core.clients.endorclient import EndorClient
            totp_code = self._totp_now(_ENDOR_TOTP_SECRET)
            client = EndorClient(
                model_name="endor-text-mixtral-8x7b-latest",
                client_id=_ENDOR_CLIENT_ID,
                client_secret=_ENDOR_CLIENT_SECRET,
                system_account_totp_secret=totp_code,
                app_id_key=_ENDOR_APP_ID_KEY,
                system_account_account_name=_ENDOR_ACCOUNT_NAME,
                system_account_account_password=_ENDOR_ACCOUNT_PASSWORD,
                oidc_app_id=_ENDOR_OIDC_APP_ID,
                system_account_device_id=_ENDOR_DEVICE_ID,
                endor_resource_server=_ENDOR_RESOURCE_SERVER,
            )
            response = client.completions.generate_completions(
                model_id=model_id,
                prompt=prompt,
                generation_config={"temperature": 0.1, "top_p": 0.9},
            )
            return response.completions[0].text if response.completions else ""

        try:
            loop = asyncio.get_event_loop()
            text = await loop.run_in_executor(None, _call_sync)
            return {"message": {"role": "assistant", "content": text, "tool_calls": []}}
        except Exception as e:
            log.error(f"Endor request failed: {e}")
            return None

    async def forecast(self, alert_title: str, frequency_data: list[dict]) -> str:
        """Forecast incident escalation based on alert frequency trends."""
        from agent.prompts import FORECAST_SYSTEM_PROMPT
        messages = [
            {"role": "system", "content": FORECAST_SYSTEM_PROMPT},
            {
                "role": "user",
                "content": f"Alert: {alert_title}\n\nFrequency trend (most recent first):\n{json.dumps(frequency_data, indent=2, default=str)}\n\nProvide escalation forecast.",
            },
        ]
        response = await self._ollama_chat(messages)
        return response.get("message", {}).get("content", "") if response else ""
