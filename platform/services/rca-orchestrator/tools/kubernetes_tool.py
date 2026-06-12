from __future__ import annotations
import os
import json
import asyncio
from typing import Any
from kubernetes import client as k8s_client, config as k8s_config
from kubernetes.client.rest import ApiException
from .base import BaseTool


KUBECONFIG_DIR = os.getenv("KUBECONFIG_DIR", "/etc/kubeconfigs")
DEFAULT_CLUSTER = os.getenv("DEFAULT_CLUSTER", "mps-dev-rno")


def _load_client(cluster: str) -> tuple[k8s_client.CoreV1Api, k8s_client.AppsV1Api, k8s_client.EventsV1Api]:
    kubeconfig_path = os.path.join(KUBECONFIG_DIR, f"{cluster}.yaml")
    if os.path.exists(kubeconfig_path):
        cfg = k8s_client.Configuration()
        k8s_config.load_kube_config(config_file=kubeconfig_path, client_configuration=cfg)
        api_client = k8s_client.ApiClient(configuration=cfg)
    else:
        k8s_config.load_incluster_config()
        api_client = k8s_client.ApiClient()
    return (
        k8s_client.CoreV1Api(api_client),
        k8s_client.AppsV1Api(api_client),
        k8s_client.EventsV1Api(api_client),
    )


class GetK8sEventsTool(BaseTool):
    name = "get_k8s_events"
    description = "Get recent Kubernetes events for a namespace. Use this to find pod crashes, OOMKills, scheduling failures, and other cluster events."
    parameters = {
        "type": "object",
        "properties": {
            "namespace": {"type": "string", "description": "Kubernetes namespace"},
            "cluster": {"type": "string", "description": "Cluster name, defaults to mps-dev-rno"},
            "limit": {"type": "integer", "description": "Max events to return, default 50"},
        },
        "required": ["namespace"],
    }

    async def execute(self, namespace: str, cluster: str = DEFAULT_CLUSTER, limit: int = 50) -> str:
        core, _, _ = _load_client(cluster)
        loop = asyncio.get_event_loop()
        events = await loop.run_in_executor(
            None,
            lambda: core.list_namespaced_event(namespace, limit=limit, _request_timeout=15)
        )
        results = []
        for e in sorted(events.items, key=lambda x: str(x.last_timestamp) if x.last_timestamp else "", reverse=True)[:limit]:
            results.append({
                "time": str(e.last_timestamp),
                "type": e.type,
                "reason": e.reason,
                "object": f"{e.involved_object.kind}/{e.involved_object.name}",
                "message": e.message,
                "count": e.count,
            })
        return json.dumps(results, default=str)


class GetPodStatusTool(BaseTool):
    name = "get_pod_status"
    description = "Get current status of pods in a namespace. Returns pod phase, restart counts, container states, and resource usage."
    parameters = {
        "type": "object",
        "properties": {
            "namespace": {"type": "string"},
            "cluster": {"type": "string"},
            "label_selector": {"type": "string", "description": "e.g. 'app=myservice'"},
        },
        "required": ["namespace"],
    }

    async def execute(self, namespace: str, cluster: str = DEFAULT_CLUSTER, label_selector: str = "") -> str:
        core, _, _ = _load_client(cluster)
        loop = asyncio.get_event_loop()
        kwargs = {"_request_timeout": 15}
        if label_selector:
            kwargs["label_selector"] = label_selector
        pods = await loop.run_in_executor(None, lambda: core.list_namespaced_pod(namespace, **kwargs))
        results = []
        for pod in pods.items:
            containers = []
            if pod.status.container_statuses:
                for cs in pod.status.container_statuses:
                    state = "running"
                    reason = ""
                    if cs.state.waiting:
                        state = "waiting"
                        reason = cs.state.waiting.reason or ""
                    elif cs.state.terminated:
                        state = "terminated"
                        reason = cs.state.terminated.reason or ""
                    containers.append({
                        "name": cs.name,
                        "ready": cs.ready,
                        "restarts": cs.restart_count,
                        "state": state,
                        "reason": reason,
                    })
            results.append({
                "name": pod.metadata.name,
                "phase": pod.status.phase,
                "node": pod.spec.node_name,
                "containers": containers,
                "conditions": [
                    {"type": c.type, "status": c.status, "reason": c.reason}
                    for c in (pod.status.conditions or [])
                ],
            })
        return json.dumps(results, default=str)


class GetPodLogsTool(BaseTool):
    name = "get_pod_logs"
    description = "Get recent logs from a specific pod. Use when investigating errors, exceptions, or abnormal behavior."
    parameters = {
        "type": "object",
        "properties": {
            "namespace": {"type": "string"},
            "pod_name": {"type": "string"},
            "cluster": {"type": "string"},
            "container": {"type": "string", "description": "Container name if pod has multiple"},
            "tail_lines": {"type": "integer", "description": "Last N lines, default 100"},
            "previous": {"type": "boolean", "description": "Get logs from crashed previous container"},
        },
        "required": ["namespace", "pod_name"],
    }

    async def execute(self, namespace: str, pod_name: str, cluster: str = DEFAULT_CLUSTER,
                      container: str = "", tail_lines: int = 100, previous: bool = False) -> str:
        core, _, _ = _load_client(cluster)
        loop = asyncio.get_event_loop()
        kwargs: dict[str, Any] = {"tail_lines": tail_lines, "_request_timeout": 20, "previous": previous}
        if container:
            kwargs["container"] = container
        try:
            logs = await loop.run_in_executor(
                None, lambda: core.read_namespaced_pod_log(pod_name, namespace, **kwargs)
            )
            return logs[-8000:] if len(logs) > 8000 else logs
        except ApiException as e:
            return f"Could not fetch logs: {e.status} {e.reason}"


class GetDeploymentStatusTool(BaseTool):
    name = "get_deployment_status"
    description = "Get deployment rollout status, replica counts, and recent rollout history."
    parameters = {
        "type": "object",
        "properties": {
            "namespace": {"type": "string"},
            "cluster": {"type": "string"},
            "deployment_name": {"type": "string", "description": "Specific deployment name, or empty for all"},
        },
        "required": ["namespace"],
    }

    async def execute(self, namespace: str, cluster: str = DEFAULT_CLUSTER, deployment_name: str = "") -> str:
        _, apps, _ = _load_client(cluster)
        loop = asyncio.get_event_loop()
        if deployment_name:
            deps = await loop.run_in_executor(
                None, lambda: [apps.read_namespaced_deployment(deployment_name, namespace, _request_timeout=15)]
            )
        else:
            resp = await loop.run_in_executor(
                None, lambda: apps.list_namespaced_deployment(namespace, _request_timeout=15)
            )
            deps = resp.items
        results = []
        for d in deps:
            results.append({
                "name": d.metadata.name,
                "desired": d.spec.replicas,
                "ready": d.status.ready_replicas,
                "available": d.status.available_replicas,
                "updated": d.status.updated_replicas,
                "conditions": [
                    {"type": c.type, "status": c.status, "message": c.message}
                    for c in (d.status.conditions or [])
                ],
            })
        return json.dumps(results, default=str)


class GetNodeStatusTool(BaseTool):
    name = "get_node_status"
    description = "Get cluster node health, resource pressure, taints, and capacity."
    parameters = {
        "type": "object",
        "properties": {
            "cluster": {"type": "string"},
            "node_name": {"type": "string", "description": "Specific node or empty for all"},
        },
        "required": [],
    }

    async def execute(self, cluster: str = DEFAULT_CLUSTER, node_name: str = "") -> str:
        core, _, _ = _load_client(cluster)
        loop = asyncio.get_event_loop()
        if node_name:
            nodes = [await loop.run_in_executor(None, lambda: core.read_node(node_name, _request_timeout=15))]
        else:
            resp = await loop.run_in_executor(None, lambda: core.list_node(_request_timeout=15))
            nodes = resp.items
        results = []
        for node in nodes:
            conditions = {c.type: c.status for c in (node.status.conditions or [])}
            results.append({
                "name": node.metadata.name,
                "ready": conditions.get("Ready", "Unknown"),
                "memory_pressure": conditions.get("MemoryPressure", "Unknown"),
                "disk_pressure": conditions.get("DiskPressure", "Unknown"),
                "cpu": node.status.allocatable.get("cpu"),
                "memory": node.status.allocatable.get("memory"),
                "taints": [{"key": t.key, "effect": t.effect} for t in (node.spec.taints or [])],
            })
        return json.dumps(results, default=str)


class DescribePodTool(BaseTool):
    name = "describe_pod"
    description = (
        "Deep-dive diagnostic on a specific pod: container states with exit codes and messages, "
        "liveness/readiness probe configs and failure counts, volume mount status, "
        "resource requests/limits, and all pod events. Use this as the primary tool "
        "for any pod-not-ready or CrashLoopBackOff investigation."
    )
    parameters = {
        "type": "object",
        "properties": {
            "namespace": {"type": "string"},
            "pod_name": {"type": "string"},
            "cluster": {"type": "string"},
        },
        "required": ["namespace", "pod_name"],
    }

    async def execute(self, namespace: str, pod_name: str, cluster: str = DEFAULT_CLUSTER) -> str:
        core, _, _ = _load_client(cluster)
        loop = asyncio.get_event_loop()
        try:
            pod = await loop.run_in_executor(
                None, lambda: core.read_namespaced_pod(pod_name, namespace, _request_timeout=15)
            )
        except ApiException as e:
            return f"Pod not found: {e.status} {e.reason}"

        result: dict[str, Any] = {
            "name": pod.metadata.name,
            "namespace": pod.metadata.namespace,
            "node": pod.spec.node_name,
            "phase": pod.status.phase,
            "start_time": str(pod.status.start_time),
            "conditions": [],
            "init_containers": [],
            "containers": [],
            "volumes": [],
        }

        for c in (pod.status.conditions or []):
            result["conditions"].append({
                "type": c.type,
                "status": c.status,
                "reason": c.reason,
                "message": c.message,
            })

        def _container_detail(spec_containers, status_containers):
            details = []
            status_map = {cs.name: cs for cs in (status_containers or [])}
            for spec in (spec_containers or []):
                cs = status_map.get(spec.name)
                entry: dict[str, Any] = {
                    "name": spec.name,
                    "image": spec.image,
                    "resources": {
                        "requests": {
                            k: v for k, v in (spec.resources.requests or {}).items()
                        } if spec.resources else {},
                        "limits": {
                            k: v for k, v in (spec.resources.limits or {}).items()
                        } if spec.resources else {},
                    },
                }
                if spec.liveness_probe:
                    lp = spec.liveness_probe
                    entry["liveness_probe"] = {
                        "failure_threshold": lp.failure_threshold,
                        "period_seconds": lp.period_seconds,
                        "initial_delay_seconds": lp.initial_delay_seconds,
                        "http_get": f"{lp.http_get.path}:{lp.http_get.port}" if lp.http_get else None,
                        "exec": lp.exec.command if lp.exec else None,
                    }
                if spec.readiness_probe:
                    rp = spec.readiness_probe
                    entry["readiness_probe"] = {
                        "failure_threshold": rp.failure_threshold,
                        "period_seconds": rp.period_seconds,
                        "initial_delay_seconds": rp.initial_delay_seconds,
                        "http_get": f"{rp.http_get.path}:{rp.http_get.port}" if rp.http_get else None,
                        "exec": rp.exec.command if rp.exec else None,
                    }
                if cs:
                    entry["ready"] = cs.ready
                    entry["restart_count"] = cs.restart_count
                    if cs.state.waiting:
                        entry["state"] = "waiting"
                        entry["reason"] = cs.state.waiting.reason
                        entry["message"] = cs.state.waiting.message
                    elif cs.state.terminated:
                        entry["state"] = "terminated"
                        entry["reason"] = cs.state.terminated.reason
                        entry["exit_code"] = cs.state.terminated.exit_code
                        entry["message"] = cs.state.terminated.message
                        entry["finished_at"] = str(cs.state.terminated.finished_at)
                    else:
                        entry["state"] = "running"
                    if cs.last_state and cs.last_state.terminated:
                        lt = cs.last_state.terminated
                        entry["last_termination"] = {
                            "reason": lt.reason,
                            "exit_code": lt.exit_code,
                            "message": lt.message,
                            "finished_at": str(lt.finished_at),
                        }
                details.append(entry)
            return details

        result["init_containers"] = _container_detail(
            pod.spec.init_containers, pod.status.init_container_statuses
        )
        result["containers"] = _container_detail(
            pod.spec.containers, pod.status.container_statuses
        )

        for vol in (pod.spec.volumes or []):
            vol_info: dict[str, Any] = {"name": vol.name}
            if vol.config_map:
                vol_info["type"] = "configMap"
                vol_info["ref"] = vol.config_map.name
            elif vol.secret:
                vol_info["type"] = "secret"
                vol_info["ref"] = vol.secret.secret_name
            elif vol.persistent_volume_claim:
                vol_info["type"] = "pvc"
                vol_info["ref"] = vol.persistent_volume_claim.claim_name
            elif vol.empty_dir is not None:
                vol_info["type"] = "emptyDir"
            result["volumes"].append(vol_info)

        # Fetch pod-specific events
        events_resp = await loop.run_in_executor(
            None,
            lambda: core.list_namespaced_event(
                namespace,
                field_selector=f"involvedObject.name={pod_name}",
                _request_timeout=15,
            )
        )
        result["events"] = [
            {
                "time": str(e.last_timestamp),
                "type": e.type,
                "reason": e.reason,
                "message": e.message,
                "count": e.count,
            }
            for e in sorted(events_resp.items, key=lambda x: str(x.last_timestamp) if x.last_timestamp else "", reverse=True)[:20]
        ]

        return json.dumps(result, default=str)


class GetPVCStatusTool(BaseTool):
    name = "get_pvc_status"
    description = (
        "Get PersistentVolumeClaim status in a namespace. Use when pods are stuck in Pending "
        "or have volume mount errors — unbound PVCs prevent pod scheduling entirely."
    )
    parameters = {
        "type": "object",
        "properties": {
            "namespace": {"type": "string"},
            "cluster": {"type": "string"},
            "pvc_name": {"type": "string", "description": "Specific PVC name, or empty for all"},
        },
        "required": ["namespace"],
    }

    async def execute(self, namespace: str, cluster: str = DEFAULT_CLUSTER, pvc_name: str = "") -> str:
        core, _, _ = _load_client(cluster)
        loop = asyncio.get_event_loop()
        if pvc_name:
            pvcs = [await loop.run_in_executor(
                None, lambda: core.read_namespaced_persistent_volume_claim(pvc_name, namespace, _request_timeout=15)
            )]
        else:
            resp = await loop.run_in_executor(
                None, lambda: core.list_namespaced_persistent_volume_claim(namespace, _request_timeout=15)
            )
            pvcs = resp.items
        results = []
        for pvc in pvcs:
            results.append({
                "name": pvc.metadata.name,
                "phase": pvc.status.phase,
                "access_modes": pvc.spec.access_modes,
                "storage_class": pvc.spec.storage_class_name,
                "capacity": pvc.status.capacity.get("storage") if pvc.status.capacity else None,
                "volume_name": pvc.spec.volume_name,
                "conditions": [
                    {"type": c.type, "status": c.status, "message": c.message}
                    for c in (pvc.status.conditions or [])
                ],
            })
        return json.dumps(results, default=str)


class GetResourceQuotaTool(BaseTool):
    name = "get_resource_quota"
    description = (
        "Check namespace resource quotas and current usage. Use when pods are stuck in Pending "
        "with 'exceeded quota' events — quota exhaustion silently blocks new pod scheduling."
    )
    parameters = {
        "type": "object",
        "properties": {
            "namespace": {"type": "string"},
            "cluster": {"type": "string"},
        },
        "required": ["namespace"],
    }

    async def execute(self, namespace: str, cluster: str = DEFAULT_CLUSTER) -> str:
        core, _, _ = _load_client(cluster)
        loop = asyncio.get_event_loop()
        quotas = await loop.run_in_executor(
            None, lambda: core.list_namespaced_resource_quota(namespace, _request_timeout=15)
        )
        if not quotas.items:
            return json.dumps({"message": "No resource quotas defined in this namespace"})
        results = []
        for q in quotas.items:
            hard = q.status.hard or {}
            used = q.status.used or {}
            utilization = {}
            for resource in hard:
                h = hard[resource]
                u = used.get(resource, "0")
                utilization[resource] = {"hard": h, "used": u}
            results.append({"name": q.metadata.name, "utilization": utilization})
        return json.dumps(results, default=str)
