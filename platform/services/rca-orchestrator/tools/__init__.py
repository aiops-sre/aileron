from .kubernetes_tool import (
    GetK8sEventsTool, GetPodStatusTool, GetPodLogsTool,
    GetDeploymentStatusTool, GetNodeStatusTool,
    DescribePodTool, GetPVCStatusTool, GetResourceQuotaTool,
)
from .dynatrace_tool import (
    GetDynatraceProblems, GetDynatraceMetrics,
    GetDynatraceEvents, GetDynatraceTraces,
)
from .cloudstack_tool import GetCloudStackVMsTool, GetCloudStackAlertsTool, GetCloudStackHostsTool
from .neo4j_tool import GetTopologyTool, GetBlastRadiusTool, GetRecentChangesTool
from .neo4j_v2_tool import GetTopologyRecursiveTool, GetBlastRadiusDeepTool
from .postgres_tool import GetHistoricalAlertsTool, GetAlertFrequencyTool, GetResolvedIncidentsTool
from .temporal_tool import QueryTemporalPropagationTool
from .base import BaseTool

ALL_TOOLS: list[BaseTool] = [
    GetK8sEventsTool(),
    GetPodStatusTool(),
    GetPodLogsTool(),
    GetDeploymentStatusTool(),
    GetNodeStatusTool(),
    DescribePodTool(),
    GetPVCStatusTool(),
    GetResourceQuotaTool(),
    GetDynatraceProblems(),
    GetDynatraceMetrics(),
    GetDynatraceEvents(),
    GetDynatraceTraces(),
    GetCloudStackVMsTool(),
    GetCloudStackAlertsTool(),
    GetCloudStackHostsTool(),
    GetTopologyTool(),
    GetBlastRadiusTool(),
    GetRecentChangesTool(),
    GetTopologyRecursiveTool(),
    GetBlastRadiusDeepTool(),
    GetHistoricalAlertsTool(),
    GetAlertFrequencyTool(),
    GetResolvedIncidentsTool(),
    QueryTemporalPropagationTool(),
]

# Reduced tool set for small local models (qwen2.5:3b).
# Sending all 21 tool schemas blows past the 180s timeout on prefill alone.
# These 6 cover ~90% of incidents; TOOL_MAP still handles execution of any tool name.
CORE_TOOLS: list[BaseTool] = [
    GetPodStatusTool(),
    DescribePodTool(),
    GetPodLogsTool(),
    GetK8sEventsTool(),
    GetDynatraceProblems(),
    GetRecentChangesTool(),
]

TOOL_MAP: dict[str, BaseTool] = {t.name: t for t in ALL_TOOLS}
