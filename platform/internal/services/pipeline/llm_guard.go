package pipeline

import (
	"regexp"
	"strings"
)

// LLMGuard sanitizes text before it reaches an LLM and validates LLM outputs.
// Implements the K8sGPT anonymization pattern + Aurora NeMo Guardrails (simplified):
//   - Input: strips internal IPs, K8s UIDs, Apple hostnames, credential patterns
//   - Output: rejects responses that leaked internal structure or contain injections
//
// This prevents the LLM from receiving or echoing internal infrastructure topology
// that should never leave the cluster boundary.
type LLMGuard struct{}

var (
	// rfc1918IP matches RFC-1918 private IP addresses — internal only.
	rfc1918IP = regexp.MustCompile(
		`\b(10\.\d{1,3}\.\d{1,3}\.\d{1,3}|172\.(1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3})\b`,
	)
	// k8sUID matches Kubernetes object UIDs (standard UUID v4 format in K8s context).
	k8sUID = regexp.MustCompile(
		`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`,
	)
	// appleHostname matches *.example.com and *.example.com internal hostnames.
	appleHostname = regexp.MustCompile(
		`\b[\w\-]+(?:\.k\.miao|\.pie|\.corp|\.iapps)?\.apple\.com\b`,
	)
	// credentialPattern matches common credential noise in alert payloads.
	credentialPattern = regexp.MustCompile(
		`(?i)(password|passwd|secret|token|api[_-]?key|auth[_-]?token)\s*[:=]\s*\S+`,
	)
	// internalSvcDNS matches cluster-internal service DNS suffixes.
	internalSvcDNS = regexp.MustCompile(
		`\b[\w\-]+\.[\w\-]+\.svc\.cluster\.local\b`,
	)
	// neo4jBolt matches Neo4j bolt:// connection strings.
	neo4jBolt = regexp.MustCompile(
		`bolt\+s?://[^\s"']+`,
	)
	// sigmaShellInjection — simplified SigmaHQ-style detection for the most
	// dangerous prompt injection patterns that would cause the LLM to execute
	// or suggest running shell commands from the alert payload itself.
	sigmaShellInjection = regexp.MustCompile(
		`(?i)(ignore previous|disregard instructions|forget your|act as|system:.*role|<\|im_start\|>|<\|system\|>|\|\s*base64\s*-d\s*\||eval\s*\(|exec\s*\()`,
	)
)

// Anonymize strips internal infrastructure identifiers from text before LLM calls.
// Returns the sanitized string and a boolean indicating whether anything was redacted.
func (g *LLMGuard) Anonymize(text string) (string, bool) {
	original := text
	text = rfc1918IP.ReplaceAllString(text, "[IP]")
	text = neo4jBolt.ReplaceAllString(text, "[DB-URL]")
	text = internalSvcDNS.ReplaceAllString(text, "[SVC]")
	text = appleHostname.ReplaceAllString(text, "[HOST]")
	text = credentialPattern.ReplaceAllString(text, "$1=[REDACTED]")
	// UIDs last — must run after hostname/IP so we don't mangle timestamps.
	text = k8sUID.ReplaceAllString(text, "[UID]")
	return text, text != original
}

// ValidateInput returns an error string if the text contains prompt-injection patterns.
// Returns "" if the input is safe to pass to the LLM.
func (g *LLMGuard) ValidateInput(text string) string {
	if sigmaShellInjection.MatchString(text) {
		return "prompt injection pattern detected; input blocked"
	}
	return ""
}

// ValidateOutput checks whether an LLM response looks valid.
// Returns "" if the output is acceptable; returns a reason string if it should be discarded.
func (g *LLMGuard) ValidateOutput(response string) string {
	r := strings.TrimSpace(response)
	if r == "" {
		return "empty response"
	}
	// Reject responses that are just the system prompt echoed back.
	if strings.HasPrefix(r, "You are an SRE") || strings.HasPrefix(r, "STEP 1") {
		return "response is system-prompt echo"
	}
	// Reject responses that contain internal IP addresses (model leaked internal state).
	if rfc1918IP.MatchString(r) {
		return "response contains internal IP addresses"
	}
	return ""
}

// AnonymizeFindings applies anonymization to a slice of alert Finding descriptions.
func (g *LLMGuard) AnonymizeFindings(findings []Finding) []Finding {
	result := make([]Finding, len(findings))
	for i, f := range findings {
		clean, _ := g.Anonymize(f.Description)
		result[i] = f
		result[i].Description = clean
	}
	return result
}

// DefaultLLMGuard is a package-level guard instance for convenience.
var DefaultLLMGuard = &LLMGuard{}
