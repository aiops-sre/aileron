from __future__ import annotations
from dataclasses import dataclass, field
from enum import Enum
from typing import Any


class InfraDomain(str, Enum):
    STORAGE = "STORAGE"
    NETWORK = "NETWORK"
    COMPUTE = "COMPUTE"
    KUBERNETES = "KUBERNETES"
    VIRTUALIZATION = "VIRTUALIZATION"
    DATABASE = "DATABASE"
    UNKNOWN = "UNKNOWN"


# Signal-based domain detection: keywords that strongly suggest a domain.
_DOMAIN_SIGNALS: dict[InfraDomain, list[str]] = {
    InfraDomain.STORAGE: [
        "disk", "volume", "pvc", "pv", "storage", "iops", "throughput",
        "filesystem", "mount", "nfs", "ceph", "s3", "bucket", "object_store",
        "read_latency", "write_latency", "io_wait", "disk_full", "out_of_space",
        "ioerror", "disk_pressure", "eviction",
    ],
    InfraDomain.NETWORK: [
        "network", "latency", "packet", "tcp", "udp", "dns", "connection",
        "timeout", "refused", "unreachable", "bandwidth", "throughput",
        "ingress", "egress", "loadbalancer", "lb", "proxy", "tls", "ssl",
        "certificate", "firewall", "npe", "network_policy", "cni",
        "interface", "nic", "vlan", "route",
    ],
    InfraDomain.COMPUTE: [
        "cpu", "memory", "oom", "oomkill", "throttl", "limit", "request",
        "resource", "process", "thread", "heap", "gc", "garbage_collect",
        "load_avg", "high_load", "spike", "burst",
    ],
    InfraDomain.KUBERNETES: [
        "pod", "container", "node", "namespace", "deployment", "replicaset",
        "daemonset", "statefulset", "job", "cronjob", "crashloop", "imagepull",
        "pending", "not_ready", "notready", "evict", "unschedul", "taint",
        "kube", "k8s", "kubelet", "apiserver", "etcd", "controller",
    ],
    InfraDomain.VIRTUALIZATION: [
        "vm", "virtual_machine", "hypervisor", "vmware", "vsphere", "vcenter",
        "esxi", "kvm", "qemu", "xen", "openstack", "cloudstack", "vlan",
        "instance", "snapshot", "migration", "live_migrate",
    ],
    InfraDomain.DATABASE: [
        "postgres", "mysql", "mariadb", "oracle", "mssql", "sqlserver",
        "mongo", "cassandra", "redis", "elasticsearch", "neo4j",
        "query", "slow_query", "deadlock", "replication", "replica",
        "primary", "secondary", "failover", "connection_pool", "max_conn",
        "table_lock", "index", "vacuum", "bloat",
    ],
}

# Upstream investigation ordering: which domain to traverse UP the stack from.
UPSTREAM_INVESTIGATION_CHAIN: dict[InfraDomain, list[InfraDomain]] = {
    InfraDomain.KUBERNETES: [InfraDomain.KUBERNETES, InfraDomain.COMPUTE, InfraDomain.NETWORK, InfraDomain.STORAGE],
    InfraDomain.STORAGE:    [InfraDomain.STORAGE, InfraDomain.NETWORK, InfraDomain.COMPUTE],
    InfraDomain.NETWORK:    [InfraDomain.NETWORK, InfraDomain.COMPUTE, InfraDomain.VIRTUALIZATION],
    InfraDomain.COMPUTE:    [InfraDomain.COMPUTE, InfraDomain.VIRTUALIZATION, InfraDomain.STORAGE],
    InfraDomain.DATABASE:   [InfraDomain.DATABASE, InfraDomain.COMPUTE, InfraDomain.STORAGE, InfraDomain.NETWORK],
    InfraDomain.VIRTUALIZATION: [InfraDomain.VIRTUALIZATION, InfraDomain.COMPUTE, InfraDomain.NETWORK],
    InfraDomain.UNKNOWN:    [InfraDomain.KUBERNETES, InfraDomain.COMPUTE, InfraDomain.NETWORK],
}


@dataclass
class DomainClassification:
    domain: InfraDomain
    confidence: float
    matched_signals: list[str]
    investigation_chain: list[InfraDomain]
    go_domain_used: bool = False  # True when we deferred to the Go engine result


class DomainClassifier:
    """Signal-based infrastructure domain classifier.

    Priority order:
    1. Go engine result (if domain != UNKNOWN and confidence is high)
    2. Alert body + labels signal matching
    3. Fallback: KUBERNETES (most common in k8s-native environments)
    """

    def classify(self, alert_body: dict[str, Any], go_context: dict[str, Any] | None) -> DomainClassification:
        # 1. Trust the Go engine if it produced a confident result.
        go_domain_str = (go_context or {}).get("domain", "")
        go_confidence = (go_context or {}).get("ontology_confidence", 0.0)
        if go_domain_str and go_domain_str.upper() not in ("", "UNKNOWN") and go_confidence >= 0.6:
            try:
                go_domain = InfraDomain(go_domain_str.upper())
                return DomainClassification(
                    domain=go_domain,
                    confidence=go_confidence,
                    matched_signals=[],
                    investigation_chain=UPSTREAM_INVESTIGATION_CHAIN[go_domain],
                    go_domain_used=True,
                )
            except ValueError:
                pass

        # 2. Signal matching on alert text.
        text = self._extract_text(alert_body).lower()
        scores: dict[InfraDomain, tuple[float, list[str]]] = {}

        for domain, signals in _DOMAIN_SIGNALS.items():
            matched = [s for s in signals if s in text]
            if matched:
                # Score: fraction of signals matched, boosted by total match count.
                base_score = len(matched) / len(signals)
                boost = min(len(matched) * 0.05, 0.3)
                scores[domain] = (base_score + boost, matched)

        if scores:
            best_domain = max(scores, key=lambda d: scores[d][0])
            score, matched = scores[best_domain]
            return DomainClassification(
                domain=best_domain,
                confidence=min(score, 0.95),
                matched_signals=matched,
                investigation_chain=UPSTREAM_INVESTIGATION_CHAIN[best_domain],
            )

        # 3. Fallback.
        return DomainClassification(
            domain=InfraDomain.KUBERNETES,
            confidence=0.3,
            matched_signals=[],
            investigation_chain=UPSTREAM_INVESTIGATION_CHAIN[InfraDomain.KUBERNETES],
        )

    def _extract_text(self, alert_body: dict[str, Any]) -> str:
        parts = []
        for key in ("title", "description", "message", "summary", "alertname"):
            val = alert_body.get(key, "")
            if val:
                parts.append(str(val))
        labels = alert_body.get("labels") or {}
        if isinstance(labels, dict):
            parts.extend(f"{k} {v}" for k, v in labels.items())
        return " ".join(parts)
