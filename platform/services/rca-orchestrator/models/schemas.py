from __future__ import annotations
from enum import Enum
from typing import Any, Optional
from datetime import datetime
from pydantic import BaseModel, Field
import uuid


class Severity(str, Enum):
    critical = "critical"
    high = "high"
    medium = "medium"
    low = "low"


class InvestigationPhase(str, Enum):
    queued = "queued"
    context_gathering = "context_gathering"
    hypothesis_formation = "hypothesis_formation"
    evidence_collection = "evidence_collection"
    root_cause_analysis = "root_cause_analysis"
    remediation_planning = "remediation_planning"
    completed = "completed"
    failed = "failed"


class ToolCall(BaseModel):
    tool: str
    args: dict[str, Any]
    result: Optional[str] = None
    duration_ms: Optional[int] = None
    error: Optional[str] = None


class Hypothesis(BaseModel):
    id: str = Field(default_factory=lambda: str(uuid.uuid4()))
    description: str
    confidence: float  # 0.0 - 1.0
    supporting_evidence: list[str] = []
    contradicting_evidence: list[str] = []


class RootCause(BaseModel):
    summary: str
    component: str
    category: str  # e.g. "memory_leak", "network_partition", "config_error"
    confidence: float
    evidence: list[str]
    timeline: list[dict[str, str]] = []


class RemediationStep(BaseModel):
    step: int
    action: str
    command: Optional[str] = None
    automated: bool = False
    risk: str = "low"  # low / medium / high


class Investigation(BaseModel):
    id: str = Field(default_factory=lambda: str(uuid.uuid4()))
    alert_id: str
    alert_title: str
    alert_body: dict[str, Any]
    severity: Severity
    incident_id: Optional[str] = None
    namespace: Optional[str] = None
    cluster: Optional[str] = None
    service: Optional[str] = None
    phase: InvestigationPhase = InvestigationPhase.queued
    started_at: datetime = Field(default_factory=datetime.utcnow)
    completed_at: Optional[datetime] = None
    tool_calls: list[ToolCall] = []
    hypotheses: list[Hypothesis] = []
    root_cause: Optional[RootCause] = None
    remediation: list[RemediationStep] = []
    similar_incidents: list[dict[str, Any]] = []
    thought_log: list[str] = []
    summary: Optional[str] = None
    confirmed: bool = False
    feedback_score: Optional[int] = None  # 1-5
    # LLM provider config (per-investigation, not persisted beyond active session)
    llm_provider: Optional[str] = None   # "local" | "oidc"
    llm_model: Optional[str] = None      # e.g. "claude-sonnet-4-6"
    llm_token: Optional[str] = None      # OIDC Provider OAuth token (not persisted to DB)
    # V2 orchestrator metadata
    orchestrator_version: Optional[str] = None   # "v1" | "v2"
    v2_domain: Optional[str] = None
    v2_confidence: Optional[float] = None


class StreamEvent(BaseModel):
    investigation_id: str
    type: str  # "thought" | "tool_call" | "phase_change" | "result" | "error"
    phase: InvestigationPhase
    data: Any
    timestamp: datetime = Field(default_factory=datetime.utcnow)


class FeedbackRequest(BaseModel):
    investigation_id: str
    score: int  # 1-5
    correct_root_cause: Optional[str] = None
    correct_component: Optional[str] = None
    notes: Optional[str] = None
    confirmed: bool = False


class KnowledgeEntry(BaseModel):
    id: str = Field(default_factory=lambda: str(uuid.uuid4()))
    title: str
    content: str
    category: str  # e.g. "runbook", "known_issue", "architecture"
    tags: list[str] = []
    created_by: str = "system"
    created_at: datetime = Field(default_factory=datetime.utcnow)


class StartInvestigationRequest(BaseModel):
    alert_id: str
    alert_title: str
    alert_body: dict[str, Any]
    severity: Severity = Severity.high
    incident_id: Optional[str] = None
    namespace: Optional[str] = None
    cluster: Optional[str] = None
    service: Optional[str] = None
    # Optional LLM override — when omitted the orchestrator uses its configured default (Ollama)
    llm_provider: Optional[str] = None   # "local" | "oidc"
    llm_model: Optional[str] = None      # e.g. "claude-sonnet-4-6"
    llm_token: Optional[str] = None      # OIDC Provider OAuth token
    # V2: Go engine deterministic context — populated by the Go alert pipeline
    go_context: Optional[dict[str, Any]] = None
