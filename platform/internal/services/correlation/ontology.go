package correlation

// ontology.go
//
// Operational failure-domain taxonomy for the AlertHub infrastructure.
// Classifies alerts into canonical failure classes so the correlation engine
// can reason about domain-specific propagation rather than relying solely on
// BERT embeddings.

import (
	"math"
	"strings"
	"unicode"
)

// FailureDomain classifies the infrastructure/operational domain of a failure.
type FailureDomain string

const (
	DomainStorage     FailureDomain = "storage"
	DomainNetwork     FailureDomain = "network"
	DomainCompute     FailureDomain = "compute"
	DomainKubernetes  FailureDomain = "kubernetes"
	DomainApplication FailureDomain = "application"
	DomainDatabase    FailureDomain = "database"
	DomainSecurity    FailureDomain = "security"
	DomainUnknown     FailureDomain = "unknown"
)

// CanonicalFailureClass is a standardized failure class within a domain.
type CanonicalFailureClass string

const (
	// Storage
	ClassStorageExhaustion CanonicalFailureClass = "storage.exhaustion"
	ClassStorageLatency    CanonicalFailureClass = "storage.latency"
	ClassStorageIO         CanonicalFailureClass = "storage.io_error"
	ClassStorageMount      CanonicalFailureClass = "storage.mount_failure"
	// Compute
	ClassCPUSaturation  CanonicalFailureClass = "compute.cpu_saturation"
	ClassMemoryPressure CanonicalFailureClass = "compute.memory_pressure"
	ClassOOMKill        CanonicalFailureClass = "compute.oom_kill"
	ClassNodeNotReady   CanonicalFailureClass = "compute.node_not_ready"
	// Network
	ClassNetworkLatency   CanonicalFailureClass = "network.latency"
	ClassNetworkPartition CanonicalFailureClass = "network.partition"
	ClassDNSFailure       CanonicalFailureClass = "network.dns"
	ClassTLSFailure       CanonicalFailureClass = "network.tls"
	// Kubernetes
	ClassPodCrash           CanonicalFailureClass = "kubernetes.pod_crash"
	ClassPodEviction        CanonicalFailureClass = "kubernetes.pod_eviction"
	ClassPodPending         CanonicalFailureClass = "kubernetes.pod_pending"
	ClassDeploymentDegraded CanonicalFailureClass = "kubernetes.deployment_degraded"
	// Application
	ClassServiceDegraded CanonicalFailureClass = "application.service_degraded"
	ClassHighErrorRate   CanonicalFailureClass = "application.high_error_rate"
	ClassTimeoutStorm    CanonicalFailureClass = "application.timeout_storm"
	// Database
	ClassDBConnExhausted CanonicalFailureClass = "database.connection_exhausted"
	ClassDBReplication   CanonicalFailureClass = "database.replication_lag"
)

// OntologyRule maps text patterns to canonical classes and domains.
type OntologyRule struct {
	Domain     FailureDomain
	Class      CanonicalFailureClass
	Keywords   []string // any keyword match triggers this rule
	Sources    []string // only apply for these sources (empty = all)
	Confidence float64  // base confidence for this mapping
}

// OperationalOntology — production rules for AlertHub's target infrastructure.
// Ordered by specificity (more specific rules first).
var OperationalOntology = []OntologyRule{
	// Storage 
	{DomainStorage, ClassStorageExhaustion, []string{"pvc full", "disk full", "disk pressure", "filesystem readonly", "storage exhausted", "volume full", "no space left", "aggregate nearly full", "aggregate full", "volume nearly full", "netapp space", "svm quota"}, nil, 0.92},
	{DomainStorage, ClassStorageLatency, []string{"netapp latency", "storage latency", "i/o latency", "slow disk", "io wait", "write latency", "read latency", "netapp ontap", "netapp aggregate", "netapp svm", "netapp volume", "netapp cluster", "aggregate latency", "volume latency", "svm throughput", "ontap"}, nil, 0.90},
	{DomainStorage, ClassStorageIO, []string{"i/o error", "io error", "disk error", "storage error", "eio", "read error", "write error", "netapp io", "volume io"}, nil, 0.88},
	{DomainStorage, ClassStorageMount, []string{"mount failed", "pvc not bound", "persistentvolumeclaim", "storageclass", "volume mount error", "unable to mount", "nfs mount", "nfs latency", "nfs error"}, nil, 0.88},
	// Compute 
	{DomainCompute, ClassOOMKill, []string{"oomkilled", "out of memory", "oom kill", "memory limit exceeded", "container exceeded memory", "killed due to oom"}, nil, 0.95},
	{DomainCompute, ClassMemoryPressure, []string{"memory pressure", "node memory", "high memory", "swap usage", "memory saturation"}, nil, 0.88},
	{DomainCompute, ClassCPUSaturation, []string{"cpu throttled", "cpu saturation", "high cpu", "cpu pressure", "cpu limit", "cpu usage", "load average"}, nil, 0.85},
	{DomainCompute, ClassNodeNotReady, []string{"node not ready", "node unreachable", "node down", "kubenode", "node status", "node condition"}, nil, 0.92},
	// Network 
	{DomainNetwork, ClassNetworkLatency, []string{"network latency", "high latency", "latency spike", "rtt", "response time", "p99 latency", "slow response"}, nil, 0.82},
	{DomainNetwork, ClassNetworkPartition, []string{"network partition", "network split", "connection refused", "unreachable", "connection timeout", "network failure"}, nil, 0.88},
	{DomainNetwork, ClassDNSFailure, []string{"dns failure", "dns error", "name resolution", "nxdomain", "dns timeout", "lookup failed"}, nil, 0.90},
	{DomainNetwork, ClassTLSFailure, []string{"tls error", "ssl error", "certificate error", "x509", "handshake failed", "certificate expired"}, nil, 0.90},
	// Kubernetes 
	{DomainKubernetes, ClassPodCrash, []string{"crashloopbackoff", "pod crash", "container exit", "pod restarting", "restart count", "back-off restarting"}, nil, 0.93},
	{DomainKubernetes, ClassPodEviction, []string{"pod evict", "evicted", "pod eviction", "node eviction", "disk pressure eviction", "memory pressure eviction"}, nil, 0.92},
	{DomainKubernetes, ClassPodPending, []string{"pod pending", "insufficient memory", "insufficient cpu", "unschedulable", "no nodes available", "pending pods"}, nil, 0.88},
	{DomainKubernetes, ClassDeploymentDegraded, []string{"deployment degraded", "replica unavailable", "replicaset", "desired replicas", "available replicas"}, nil, 0.85},
	// Application 
	{DomainApplication, ClassHighErrorRate, []string{"error rate", "5xx", "error budget", "failure rate", "error spike", "failed requests"}, nil, 0.82},
	{DomainApplication, ClassTimeoutStorm, []string{"timeout storm", "request timeout", "gateway timeout", "upstream timeout", "circuit open", "deadline exceeded"}, nil, 0.85},
	{DomainApplication, ClassServiceDegraded, []string{"service degraded", "service unavailable", "degraded performance", "response time degraded", "availability drop"}, nil, 0.80},
	// Database 
	{DomainDatabase, ClassDBConnExhausted, []string{"connection pool exhausted", "max connections", "too many connections", "pgbouncer"}, nil, 0.88},
	{DomainDatabase, ClassDBReplication, []string{"replication lag", "replica behind", "replication delay", "wal lag", "slave lag", "standby lag"}, nil, 0.85},
}

// DomainPropagationMatrix[cause][effect] = propagation_probability.
// Defines which failure domains propagate into which others.
var DomainPropagationMatrix = map[FailureDomain]map[FailureDomain]float64{
	DomainStorage: {
		DomainKubernetes:  0.88,
		DomainApplication: 0.75,
		DomainDatabase:    0.85,
		DomainCompute:     0.40,
	},
	DomainNetwork: {
		DomainKubernetes:  0.80,
		DomainApplication: 0.85,
		DomainDatabase:    0.70,
		DomainStorage:     0.45,
	},
	DomainCompute: {
		DomainKubernetes:  0.90,
		DomainApplication: 0.70,
		DomainStorage:     0.35,
	},
	DomainKubernetes: {
		DomainApplication: 0.85,
		DomainDatabase:    0.60,
	},
}

// OntologyEngine 

// OntologyEngine classifies alerts and enriches them with canonical domain info.
type OntologyEngine struct {
	rules []OntologyRule
}

func NewOntologyEngine() *OntologyEngine {
	return &OntologyEngine{rules: OperationalOntology}
}

// OntologyResult holds the classification output for a single alert.
type OntologyResult struct {
	Domain          FailureDomain
	Class           CanonicalFailureClass
	Confidence      float64
	MatchedKeywords []string
}

// Classify determines the failure domain and canonical class for an alert.
func (o *OntologyEngine) Classify(alert *Alert) *OntologyResult {
	text := strings.ToLower(alert.Title + " " + alert.Description)
	text = strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) && r != '/' && r != '-' {
			return ' '
		}
		return r
	}, text)

	best := &OntologyResult{Domain: DomainUnknown, Confidence: 0}

	for _, rule := range o.rules {
		if len(rule.Sources) > 0 {
			found := false
			for _, s := range rule.Sources {
				if strings.EqualFold(s, alert.Source) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		var matched []string
		for _, kw := range rule.Keywords {
			if strings.Contains(text, kw) {
				matched = append(matched, kw)
			}
		}
		if len(matched) == 0 {
			continue
		}

		boost := float64(len(matched)-1) * 0.03
		score := math.Min(1.0, rule.Confidence+boost)

		if score > best.Confidence {
			best = &OntologyResult{
				Domain:          rule.Domain,
				Class:           rule.Class,
				Confidence:      score,
				MatchedKeywords: matched,
			}
		}
	}
	return best
}

// DomainCorrelationBoost returns extra score weight when two alerts share domain + class.
func DomainCorrelationBoost(a, b *OntologyResult) float64 {
	if a.Domain == DomainUnknown || b.Domain == DomainUnknown {
		return 0
	}
	if a.Domain == b.Domain && a.Class == b.Class {
		return 0.25
	}
	if a.Domain == b.Domain {
		return 0.12
	}
	return 0
}

// AlertDomain classifies an alert's failure domain via the operational ontology.
func AlertDomain(a *Alert) FailureDomain {
	return NewOntologyEngine().Classify(a).Domain
}
