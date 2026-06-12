from __future__ import annotations
import os
import json
import hmac
import hashlib
import base64
import aiohttp
from .base import BaseTool

CS_ENDPOINTS = {
    "rno": os.getenv("CLOUDSTACK_RNO_URL", "https://interactive-cs-rno-prod.apple.com/client/api"),
    "mdn": os.getenv("CLOUDSTACK_MDN_URL", "https://interactive-cs-mdn-prod.apple.com/client/api"),
}
CS_API_KEY = os.getenv("CLOUDSTACK_API_KEY", "")
CS_SECRET_KEY = os.getenv("CLOUDSTACK_SECRET_KEY", "")

_PLACEHOLDER_KEY = "cloudstack_api_key_placeholder"
_PLACEHOLDER_SECRET = "cloudstack_secret_key_placeholder"


def _creds_ok() -> bool:
    return (
        bool(CS_API_KEY) and CS_API_KEY != _PLACEHOLDER_KEY and
        bool(CS_SECRET_KEY) and CS_SECRET_KEY != _PLACEHOLDER_SECRET
    )


def _sign(params: dict) -> str:
    sorted_params = "&".join(f"{k.lower()}={str(v).lower()}" for k, v in sorted(params.items()))
    sig = hmac.new(CS_SECRET_KEY.encode(), sorted_params.encode(), hashlib.sha1).digest()
    return base64.b64encode(sig).decode()


async def _cs_request(region: str, command: str, extra: dict = {}) -> dict:
    params = {"command": command, "apiKey": CS_API_KEY, "response": "json", **extra}
    params["signature"] = _sign(params)
    url = CS_ENDPOINTS.get(region, CS_ENDPOINTS["rno"])
    async with aiohttp.ClientSession() as session:
        async with session.get(url, params=params, ssl=False, timeout=aiohttp.ClientTimeout(total=20)) as resp:
            text = await resp.text()
            if resp.status == 401 or resp.status == 403:
                raise PermissionError(f"CloudStack auth failed (HTTP {resp.status}). Check CLOUDSTACK_API_KEY/SECRET_KEY.")
            if resp.status >= 400:
                raise RuntimeError(f"CloudStack API error {resp.status}: {text[:200]}")
            try:
                return json.loads(text)
            except Exception:
                raise RuntimeError(f"CloudStack returned non-JSON: {text[:200]}")


class GetCloudStackVMsTool(BaseTool):
    name = "get_cloudstack_vms"
    description = "List CloudStack VMs in a zone/account. Use to check VM state, health, and resource utilization for infrastructure issues."
    parameters = {
        "type": "object",
        "properties": {
            "region": {"type": "string", "description": "rno or mdn"},
            "zone": {"type": "string", "description": "Zone name filter"},
            "keyword": {"type": "string", "description": "Search keyword (hostname, service name)"},
            "state": {"type": "string", "description": "Filter by state: Running, Stopped, Error"},
        },
        "required": ["region"],
    }

    async def execute(self, region: str = "rno", zone: str = "", keyword: str = "", state: str = "") -> str:
        if not _creds_ok():
            return json.dumps({"error": "CloudStack credentials not configured (placeholder values). Set CLOUDSTACK_API_KEY and CLOUDSTACK_SECRET_KEY.", "vms": []})
        extra = {}
        if zone: extra["zoneid"] = zone
        if keyword: extra["keyword"] = keyword
        if state: extra["state"] = state
        data = await _cs_request(region, "listVirtualMachines", extra)
        vms = data.get("listvirtualmachinesresponse", {}).get("virtualmachine", [])
        results = [
            {
                "name": vm.get("name"),
                "state": vm.get("state"),
                "zone": vm.get("zonename"),
                "hostname": vm.get("hostname"),
                "cpu": vm.get("cpunumber"),
                "memory_mb": vm.get("memory"),
                "cpu_used": vm.get("cpuused"),
                "template": vm.get("templatename"),
                "created": vm.get("created"),
            }
            for vm in vms[:20]
        ]
        return json.dumps(results, default=str)


class GetCloudStackAlertsTool(BaseTool):
    name = "get_cloudstack_alerts"
    description = "Get recent CloudStack infrastructure alerts (host failures, storage issues, network problems)."
    parameters = {
        "type": "object",
        "properties": {
            "region": {"type": "string", "description": "rno or mdn"},
            "alert_type": {"type": "integer", "description": "0=CPU, 1=Memory, 2=Storage, 3=Network"},
        },
        "required": ["region"],
    }

    async def execute(self, region: str = "rno", alert_type: int = None) -> str:
        if not _creds_ok():
            return json.dumps({"error": "CloudStack credentials not configured (placeholder values).", "alerts": []})
        extra = {}
        if alert_type is not None:
            extra["type"] = str(alert_type)
        data = await _cs_request(region, "listAlerts", extra)
        alerts = data.get("listalertsresponse", {}).get("alert", [])
        results = [
            {
                "type": a.get("type"),
                "name": a.get("name"),
                "description": a.get("description"),
                "sent": a.get("sent"),
            }
            for a in alerts[:20]
        ]
        return json.dumps(results, default=str)


class GetCloudStackHostsTool(BaseTool):
    name = "get_cloudstack_hosts"
    description = "Get hypervisor host status. Use to find host failures or degraded hosts causing VM instability."
    parameters = {
        "type": "object",
        "properties": {
            "region": {"type": "string", "description": "rno or mdn"},
            "state": {"type": "string", "description": "Up, Down, Alert, Disconnected"},
        },
        "required": ["region"],
    }

    async def execute(self, region: str = "rno", state: str = "") -> str:
        if not _creds_ok():
            return json.dumps({"error": "CloudStack credentials not configured (placeholder values).", "hosts": []})
        extra = {"type": "Routing"}
        if state:
            extra["state"] = state
        data = await _cs_request(region, "listHosts", extra)
        hosts = data.get("listhostsresponse", {}).get("host", [])
        results = [
            {
                "name": h.get("name"),
                "state": h.get("state"),
                "status": h.get("resourcestate"),
                "zone": h.get("zonename"),
                "cpu_allocated": h.get("cpuallocated"),
                "memory_allocated": h.get("memoryallocated"),
                "vms": h.get("virtualmachines"),
            }
            for h in hosts[:20]
        ]
        return json.dumps(results, default=str)
