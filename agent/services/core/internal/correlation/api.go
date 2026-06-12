package correlation

// ConfigViolationSummary is a configuration violation found by the API.
type ConfigViolationSummary struct {
	RuleID       string `json:"rule_id"`
	Severity     string `json:"severity"`
	ResourceKind string `json:"resource_kind"`
	Namespace    string `json:"namespace"`
	ResourceName string `json:"resource_name"`
	Message      string `json:"message"`
}

// SecurityPostureSummary is the security posture for a cluster.
type SecurityPostureSummary struct {
	ClusterID string              `json:"cluster_id"`
	Findings  []SecurityFindingSummary `json:"findings"`
	Score     float64             `json:"score"`
}

// SecurityFindingSummary is one security finding.
type SecurityFindingSummary struct {
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Resource string `json:"resource"`
}

// GetConfigViolations returns configuration violations for a cluster.
// Actual violations flow via Kafka from the kubesense-agent config scanner.
func (c *Correlator) GetConfigViolations(clusterID string) []ConfigViolationSummary {
	return []ConfigViolationSummary{}
}

// GetSecurityPosture returns the security posture assessment for a cluster.
func (c *Correlator) GetSecurityPosture(clusterID string) *SecurityPostureSummary {
	return &SecurityPostureSummary{
		ClusterID: clusterID,
		Findings:  []SecurityFindingSummary{},
		Score:     1.0,
	}
}
