package rca

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	domain_ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	domain_hyp "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/hypothesis"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/ollama"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/metrics"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/tracing"
	"go.opentelemetry.io/otel/attribute"
)

const (
	// Raised from 0.65: require LIKELY-band confidence before LLM is called.
	// At 0.65 the scorer had too many BasePrior-adjacent hypotheses passing through.
	llmConfidenceThreshold = 0.75

	// Raised from 2: require 3 verified supporting facts in the prompt.
	// "2" allowed narratives with only 2 pieces of context evidence (not grounding facts).
	llmMinSupportingEvidence = 3

	ensembleAgreementThreshold = 0.40
)

// Narrator generates human-readable RCA narratives from verified evidence.
// When a second (triage) model is wired, it runs both in parallel and uses
// agreement voting to boost or reduce confidence before returning the narrative.
type Narrator struct {
	ollama       *ollama.Client // primary narrative model
	triageOllama *ollama.Client // fast/cheap model for ensemble voting
	logger       *slog.Logger
}

// CitationRef identifies a piece of evidence cited in the narrative.
type CitationRef struct {
	Source       string `json:"source"`
	EvidenceType string `json:"evidence_type"`
	Description  string `json:"description"`
}

// NewNarrator constructs a Narrator. ollama may be nil; narrator falls back to templates.
func NewNarrator(ollamaClient *ollama.Client, logger *slog.Logger) *Narrator {
	return &Narrator{ollama: ollamaClient, logger: logger}
}

// SetTriageClient wires a second fast model for ensemble agreement voting.
// When set and different from the primary model, both run in parallel; agreement
// is measured before returning the final narrative.
func (n *Narrator) SetTriageClient(c *ollama.Client) { n.triageOllama = c }

// Generate produces a narrative and model name.
func (n *Narrator) Generate(
	ctx context.Context,
	winner *domain_hyp.Hypothesis,
	allHypotheses []*domain_hyp.Hypothesis,
	evidence []*domain_ev.Evidence,
	incidentTitle string,
) (narrative string, model string) {
	narrative, model, _ = n.GenerateWithCitations(ctx, winner, allHypotheses, evidence, incidentTitle)
	return
}

// GenerateWithCitations runs the full narrative pipeline with optional ensemble voting
// and returns citation refs for frontend rendering.
func (n *Narrator) GenerateWithCitations(
	ctx context.Context,
	winner *domain_hyp.Hypothesis,
	allHypotheses []*domain_hyp.Hypothesis,
	evidence []*domain_ev.Evidence,
	incidentTitle string,
) (narrative string, model string, citations []CitationRef) {
	ctx, span := tracing.Tracer().Start(ctx, "rca.narrator.generate_with_citations")
	defer span.End()
	span.SetAttributes(
		attribute.Float64("confidence", winner.Confidence),
		attribute.String("hypothesis.type", winner.Type),
	)

	supportingCount := winner.SupportingEvidenceCount
	// Re-count ACTUAL facts that will appear in the LLM prompt after filtering.
	// winner.SupportingEvidenceCount is set at hypothesis-scoring time; by the time
	// the narrator runs, evidence may have been deduped or filtered (empty descriptions,
	// wrong role, FetchError status). Using the stale count could call the LLM with
	// zero grounding facts — the primary cause of hallucinated RCA narratives.
	actualFacts := countGroundingFacts(evidence)

	if winner.Confidence < llmConfidenceThreshold || supportingCount < llmMinSupportingEvidence || actualFacts == 0 {
		if actualFacts == 0 {
			metrics.LLMNarrativeCalls.WithLabelValues("no_grounding_facts").Inc()
			n.logger.InfoContext(ctx, "narrator: no grounding facts — using template",
				"hypothesis", winner.Type, "confidence", winner.Confidence,
				"supporting_count", supportingCount)
		} else {
			metrics.LLMNarrativeCalls.WithLabelValues("below_threshold").Inc()
		}
		return n.uncertaintyTemplate(winner, allHypotheses), "template", nil
	}
	if n.ollama == nil {
		metrics.LLMNarrativeCalls.WithLabelValues("ollama_not_configured").Inc()
		return n.uncertaintyTemplate(winner, allHypotheses), "template", nil
	}

	citations = buildCitations(evidence)
	prompt := n.buildGroundedPrompt(winner, allHypotheses, evidence, incidentTitle)

	// ── Ensemble voting (k8s-ai-debugger pattern) ───────────────────────────
	// When a second model is configured and differs from primary, run both in
	// parallel and check keyword agreement. Agreement → high confidence signal.
	// Disagreement → prepend uncertainty note so operators know to verify.
	if n.triageOllama != nil && n.triageOllama.Model() != n.ollama.Model() {
		primary, triage := n.runEnsemble(ctx, prompt)
		span.SetAttributes(attribute.Bool("ensemble.used", true))
		if primary == "" {
			metrics.LLMNarrativeCalls.WithLabelValues("ollama_error").Inc()
			return n.uncertaintyTemplate(winner, allHypotheses), "template", citations
		}
		agreement := keywordAgreement(primary, triage, winner)
		span.SetAttributes(attribute.Float64("ensemble.agreement", agreement))
		n.logger.InfoContext(ctx, "ensemble vote complete",
			"agreement", fmt.Sprintf("%.2f", agreement),
			"primary_model", n.ollama.Model(),
			"triage_model", n.triageOllama.Model(),
		)
		if agreement >= ensembleAgreementThreshold {
			metrics.LLMNarrativeCalls.WithLabelValues("ensemble_agree").Inc()
			return primary, n.ollama.Model() + "+ensemble", citations
		}
		// Low agreement — both models disagree on the failure class.
		metrics.LLMNarrativeCalls.WithLabelValues("ensemble_disagree").Inc()
		return "[Note: automated analysis models produced divergent outputs — manual verification recommended]\n\n" + primary,
			n.ollama.Model() + "+ensemble-uncertain", citations
	}

	// ── Single model path ────────────────────────────────────────────────────
	llmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	response, err := n.ollama.Generate(llmCtx, prompt)
	metrics.LLMNarrativeDuration.Observe(time.Since(start).Seconds())
	if err != nil || strings.TrimSpace(response) == "" {
		n.logger.WarnContext(ctx, "ollama narrative failed — using template", "error", err)
		metrics.LLMNarrativeCalls.WithLabelValues("ollama_error").Inc()
		return n.uncertaintyTemplate(winner, allHypotheses), "template", citations
	}
	trimmed := strings.TrimSpace(response)
	// If the LLM honoured the "refuse if no evidence" instruction, fall back to template.
	if isLLMRefusal(trimmed) {
		n.logger.InfoContext(ctx, "ollama refused: insufficient evidence per model judgment")
		metrics.LLMNarrativeCalls.WithLabelValues("llm_refused").Inc()
		return n.uncertaintyTemplate(winner, allHypotheses), "template", citations
	}
	metrics.LLMNarrativeCalls.WithLabelValues("success").Inc()
	return trimmed, n.ollama.Model(), citations
}

// runEnsemble calls both primary and triage models in parallel.
func (n *Narrator) runEnsemble(ctx context.Context, prompt string) (primary, triage string) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		c, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		if r, err := n.ollama.Generate(c, prompt); err == nil {
			primary = strings.TrimSpace(r)
		}
	}()
	go func() {
		defer wg.Done()
		c, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		if r, err := n.triageOllama.Generate(c, prompt); err == nil {
			triage = strings.TrimSpace(r)
		}
	}()
	wg.Wait()
	return primary, triage
}

// keywordAgreement measures keyword overlap between two LLM outputs for the winning
// hypothesis. Returns 0.0–1.0; >= ensembleAgreementThreshold means models agree.
func keywordAgreement(a, b string, winner *domain_hyp.Hypothesis) float64 {
	if a == "" || b == "" {
		return 0
	}
	keywords := extractKeywords(winner.Type + " " + winner.Title)
	if len(keywords) == 0 {
		return 0.5
	}
	aLow, bLow := strings.ToLower(a), strings.ToLower(b)
	matches := 0
	for _, kw := range keywords {
		if strings.Contains(aLow, kw) && strings.Contains(bLow, kw) {
			matches++
		}
	}
	return float64(matches) / float64(len(keywords))
}

func extractKeywords(s string) []string {
	raw := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == '_' || r == '-' || r == ' ' || r == '/' || r == '.'
	})
	var out []string
	for _, w := range raw {
		if len(w) >= 4 {
			out = append(out, w)
		}
	}
	return out
}

func buildCitations(evidence []*domain_ev.Evidence) []CitationRef {
	var citations []CitationRef
	seen := make(map[string]bool)
	for _, ev := range evidence {
		if ev.Role != domain_ev.RoleSupports || ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		key := string(ev.EvidenceType) + ":" + ev.Source
		if seen[key] {
			continue
		}
		seen[key] = true
		desc := ev.Description
		if len(desc) > 100 {
			desc = desc[:100] + "…"
		}
		citations = append(citations, CitationRef{
			Source:       ev.Source,
			EvidenceType: string(ev.EvidenceType),
			Description:  desc,
		})
		if len(citations) >= 8 {
			break
		}
	}
	return citations
}

func (n *Narrator) buildGroundedPrompt(
	winner *domain_hyp.Hypothesis,
	allHypotheses []*domain_hyp.Hypothesis,
	evidence []*domain_ev.Evidence,
	incidentTitle string,
) string {
	var facts []string
	for _, ev := range evidence {
		if ev.Role != domain_ev.RoleSupports || ev.FetchStatus != domain_ev.FetchSuccess || ev.Description == "" {
			continue
		}
		facts = append(facts, "• "+sanitizeForPrompt(ev.Description))
	}
	var rejections []string
	for _, h := range allHypotheses {
		if h.ID == winner.ID {
			continue
		}
		if h.Status == domain_hyp.StatusRejected && h.RejectionReason != nil {
			rejections = append(rejections, fmt.Sprintf("• %s: %s", sanitizeForPrompt(h.Title), sanitizeForPrompt(*h.RejectionReason)))
		} else if h.Status == domain_hyp.StatusInsufficientEvidence {
			rejections = append(rejections, fmt.Sprintf("• %s: insufficient evidence (confidence %.0f%%)",
				sanitizeForPrompt(h.Title), h.Confidence*100))
		}
	}
	var sb strings.Builder
	// Tightened system prompt: explicit "refuse if no facts", "no invented names",
	// and a "Five Whys" chain to prevent surface-level wrong conclusions.
	sb.WriteString("You are a senior SRE writing a factual incident root cause summary.\n")
	sb.WriteString("STRICT RULES:\n")
	sb.WriteString("1. Use ONLY the numbered facts listed below. Do not add any other information.\n")
	sb.WriteString("2. Do NOT invent workload names, pod names, node names, IPs, or resource names.\n")
	sb.WriteString("3. Do NOT use the incident title or hypothesis title as a source of facts.\n")
	sb.WriteString("4. If the facts do not explain the root cause clearly, write: 'Insufficient evidence to determine root cause — operator investigation required.'\n")
	sb.WriteString("5. Write exactly 3 sentences. No more, no less.\n\n")

	sb.WriteString(fmt.Sprintf("Hypothesis: %s\n", sanitizeForPrompt(winner.Title)))
	sb.WriteString(fmt.Sprintf("Confidence: %.0f%% (%s)\n\n", winner.Confidence*100, winner.Band))

	if len(facts) > 0 {
		sb.WriteString(fmt.Sprintf("VERIFIED FACTS (%d items — cite by number):\n", len(facts)))
		for i, f := range facts {
			sb.WriteString(fmt.Sprintf("[%d] %s\n", i+1, strings.TrimPrefix(f, "• ")))
		}
		sb.WriteString("\n")
	}

	if len(rejections) > 0 {
		sb.WriteString("Ruled out (do NOT include these in the summary):\n")
		sb.WriteString(strings.Join(rejections, "\n"))
		sb.WriteString("\n\n")
	}

	sb.WriteString("Write the 3-sentence root cause summary now (or the insufficient-evidence sentence if facts are unclear):")
	return sb.String()
}

func (n *Narrator) uncertaintyTemplate(winner *domain_hyp.Hypothesis, allHypotheses []*domain_hyp.Hypothesis) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Investigation confidence is %s (%.0f%%). ", winner.Band, winner.Confidence*100))
	sb.WriteString(fmt.Sprintf("The most consistent explanation based on available evidence is: %s. ", winner.Title))
	if len(winner.MissingRequiredEvidence) > 0 {
		sb.WriteString(fmt.Sprintf("The following evidence could not be gathered: %s. ",
			strings.Join(winner.MissingRequiredEvidence, ", ")))
	}
	var rejected []string
	for _, h := range allHypotheses {
		if h.ID == winner.ID {
			continue
		}
		if h.Status == domain_hyp.StatusRejected {
			if h.RejectionReason != nil {
				rejected = append(rejected, fmt.Sprintf("%s (%s)", h.Title, *h.RejectionReason))
			} else {
				rejected = append(rejected, h.Title)
			}
		}
	}
	if len(rejected) > 0 {
		sb.WriteString(fmt.Sprintf("The following hypotheses were ruled out: %s.", strings.Join(rejected, "; ")))
	} else {
		sb.WriteString("Operator verification is required before taking remediation action.")
	}
	return sb.String()
}

// sanitizeForPrompt strips characters that could be used for prompt injection:
// newlines would let an attacker in evidence descriptions inject new instructions
// into the structured prompt block. We replace them with spaces and limit length.
func sanitizeForPrompt(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	// Collapse multiple spaces introduced by the above
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// countGroundingFacts returns the number of evidence items that will actually appear
// in the LLM prompt as supporting facts. This is the true pre-filter count — NOT the
// hypothesis engine's cached SupportingEvidenceCount which may be stale.
// Called before the LLM gate to prevent calling the model with 0 grounding facts.
func countGroundingFacts(evidence []*domain_ev.Evidence) int {
	n := 0
	for _, ev := range evidence {
		if ev.Role == domain_ev.RoleSupports &&
			ev.FetchStatus == domain_ev.FetchSuccess &&
			ev.Description != "" {
			n++
		}
	}
	return n
}

// isLLMRefusal returns true when the LLM output indicates it chose not to generate
// a narrative (e.g. it honoured the "insufficient evidence" instruction).
// This catches honest refusals so we can fall back to the deterministic template.
func isLLMRefusal(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.HasPrefix(lower, "insufficient evidence") ||
		strings.Contains(lower, "operator investigation required") && len(text) < 120
}
