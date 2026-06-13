package rca

import (
	"time"

	domain_ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	domain_inv "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/investigation"
	fetchers "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
)

// ── Universal Investigation Gate ─────────────────────────────────────────────
//
// The InvestigationGate enforces that five mandatory questions are answerable
// from the gathered evidence before Phase 4 synthesis is allowed.
//
// The five questions address the most common causes of inaccurate RCA:
//  Q1 – ExactMetricName: which specific metric triggered this?
//  Q2 – FirstCrossingTime: when did it FIRST exceed the threshold?
//  Q3 – WhatChangedBefore: was there a deployment/config change within 2 h?
//  Q4 – GapBetweenAlertAndUsage: difference between configured limit and actual usage?
//  Q5 – SingleLargestContributor: named entity confirmed by DIRECT evidence?
//
// If any question cannot be answered AND the corresponding fetcher was never
// attempted, BlockSynthesis is set to true.

// GateQuestion labels the five mandatory investigation questions.
type GateQuestion int

const (
	GateQ1ExactMetricName          GateQuestion = 1
	GateQ2FirstCrossingTime        GateQuestion = 2
	GateQ3WhatChangedBefore        GateQuestion = 3
	GateQ4GapBetweenAlertAndUsage  GateQuestion = 4
	GateQ5SingleLargestContributor GateQuestion = 5
)

// gateQuestionLabel maps question number to human-readable name.
var gateQuestionLabel = map[GateQuestion]string{
	GateQ1ExactMetricName:          "ExactMetricName",
	GateQ2FirstCrossingTime:        "FirstCrossingTime",
	GateQ3WhatChangedBefore:        "WhatChangedBefore",
	GateQ4GapBetweenAlertAndUsage:  "GapBetweenAlertAndUsage",
	GateQ5SingleLargestContributor: "SingleLargestContributor",
}

// GateResult is the output of RunInvestigationGate.
type GateResult struct {
	// Passed is true when all five questions are answered.
	Passed bool

	// FailedQuestions lists the question numbers that could not be answered.
	FailedQuestions []GateQuestion

	// QuestionLabels maps each failed question number to its human-readable name.
	QuestionLabels map[GateQuestion]string

	// MissingFetchers lists fetcher IDs that could answer the missing questions
	// but were never attempted.
	MissingFetchers []fetchers.FetcherID

	// BlockSynthesis is true when at least one question failed AND the
	// corresponding fetcher was never run.
	BlockSynthesis bool

	// Answers holds the answer evidence item for each passed question.
	Answers map[GateQuestion]*domain_ev.Evidence
}

// changeWindowDuration is the look-back window for Q3 (deployment/config change
// before first metric crossing).
const changeWindowDuration = 2 * time.Hour

// ── Fetcher associations ──────────────────────────────────────────────────────
//
// Each question maps to the fetcher(s) whose data would answer it.

var questionFetchers = map[GateQuestion][]fetchers.FetcherID{
	GateQ1ExactMetricName:          {"metricsstore", "kubesense"},
	GateQ2FirstCrossingTime:        {"metricsstore", "kubesense"},
	GateQ3WhatChangedBefore:        {"okg", "kubernetes"},
	GateQ4GapBetweenAlertAndUsage:  {"metricsstore", "kubernetes"},
	GateQ5SingleLargestContributor: {"kubernetes", "kubesense", "netapp", "cloudstack"},
}

// RunInvestigationGate evaluates the five mandatory questions against the
// gathered evidence and the incident's creation time.
func RunInvestigationGate(
	evidence []*domain_ev.Evidence,
	incident *domain_inv.Investigation,
) GateResult {
	result := GateResult{
		QuestionLabels: make(map[GateQuestion]string),
		Answers:        make(map[GateQuestion]*domain_ev.Evidence),
	}

	// Track which fetchers were actually attempted (any FetchStatus other than
	// the zero value — the fetcher ran even if it returned FetchMissing).
	attemptedFetchers := make(map[fetchers.FetcherID]bool)
	for _, ev := range evidence {
		fid := fetchers.FetcherID(ev.FetcherID)
		attemptedFetchers[fid] = true
	}

	// ── Q1: ExactMetricName ──────────────────────────────────────────────────
	// Answered if any evidence item has MetricName set.
	q1Passed := false
	for _, ev := range evidence {
		if ev.MetricName != "" {
			q1Passed = true
			result.Answers[GateQ1ExactMetricName] = ev
			break
		}
	}

	// ── Q2: FirstCrossingTime ────────────────────────────────────────────────
	// Answered if at least one evidence item has OccurredAt set and is
	// distinguishable from the incident creation time (temporal evidence).
	q2Passed := false
	for _, ev := range evidence {
		if ev.OccurredAt == nil {
			continue
		}
		// Flag: if OccurredAt equals incident CreatedAt, this is probably the
		// alert timestamp echoed back — NOT the first crossing (see Guard4).
		if ev.OccurredAt.Equal(incident.IncidentStartedAt) {
			continue
		}
		if ev.TemporalMode == domain_ev.TemporalHistorical || ev.FetchStatus == domain_ev.FetchSuccess {
			q2Passed = true
			result.Answers[GateQ2FirstCrossingTime] = ev
			break
		}
	}

	// ── Q3: WhatChangedBefore ────────────────────────────────────────────────
	// Answered if a change/deployment evidence item exists within the 2-hour
	// window before the first crossing (or incident start as proxy).
	q3Passed := false
	windowStart := incident.IncidentStartedAt.Add(-changeWindowDuration)
	for _, ev := range evidence {
		if ev.EvidenceType != domain_ev.TypeChangeDeployment &&
			ev.EvidenceType != domain_ev.TypeChangeConfig &&
			ev.EvidenceType != domain_ev.TypeOKGCausalityScore {
			continue
		}
		if ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		t := ev.OccurredAt
		if t == nil {
			t = ev.AsOfTime
		}
		if t != nil && t.After(windowStart) && !t.After(incident.IncidentStartedAt) {
			q3Passed = true
			result.Answers[GateQ3WhatChangedBefore] = ev
			break
		}
	}

	// ── Q4: GapBetweenAlertAndUsage ─────────────────────────────────────────
	// Answered when both a Tier3 (config/request) evidence item AND a Tier1
	// (live metrics/usage) evidence item are present.  The gap between the two
	// values represents over-provisioning or under-sizing.
	hasTier3Config := false
	hasTier1Metric := false
	var tier3Evidence *domain_ev.Evidence
	var tier1Evidence *domain_ev.Evidence
	for _, ev := range evidence {
		if ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		tier := EvidenceTierFor(ev.EvidenceType)
		if tier == EvidenceTierConfig && tier3Evidence == nil {
			hasTier3Config = true
			tier3Evidence = ev
		}
		if tier == EvidenceTierPrimary && tier1Evidence == nil {
			hasTier1Metric = true
			tier1Evidence = ev
		}
	}
	q4Passed := hasTier3Config && hasTier1Metric
	if q4Passed {
		// Use the Tier1 (metric) evidence as the primary answer.
		result.Answers[GateQ4GapBetweenAlertAndUsage] = tier1Evidence
		_ = tier3Evidence // retained for full-answer rendering in caller
	}

	// ── Q5: SingleLargestContributor ─────────────────────────────────────────
	// Answered when at least one evidence item has:
	//   Role = RoleSupports
	//   FetchStatus = FetchSuccess
	//   EvidenceTag = EvidenceTagDirect
	q5Passed := false
	for _, ev := range evidence {
		if ev.Role != domain_ev.RoleSupports {
			continue
		}
		if ev.FetchStatus != domain_ev.FetchSuccess {
			continue
		}
		if ev.EvidenceTag != domain_ev.EvidenceTagDirect {
			continue
		}
		q5Passed = true
		result.Answers[GateQ5SingleLargestContributor] = ev
		break
	}

	// ── Collate failures ─────────────────────────────────────────────────────
	passed := map[GateQuestion]bool{
		GateQ1ExactMetricName:          q1Passed,
		GateQ2FirstCrossingTime:        q2Passed,
		GateQ3WhatChangedBefore:        q3Passed,
		GateQ4GapBetweenAlertAndUsage:  q4Passed,
		GateQ5SingleLargestContributor: q5Passed,
	}

	allPassed := true
	for q, ok := range passed {
		if !ok {
			allPassed = false
			result.FailedQuestions = append(result.FailedQuestions, q)
			result.QuestionLabels[q] = gateQuestionLabel[q]

			// Check if the responsible fetcher was never attempted.
			for _, fid := range questionFetchers[q] {
				if !attemptedFetchers[fid] {
					result.MissingFetchers = appendUnique(result.MissingFetchers, fid)
					result.BlockSynthesis = true
				}
			}
		}
	}

	result.Passed = allPassed
	return result
}

// appendUnique appends fid to the slice only if it is not already present.
func appendUnique(slice []fetchers.FetcherID, fid fetchers.FetcherID) []fetchers.FetcherID {
	for _, v := range slice {
		if v == fid {
			return slice
		}
	}
	return append(slice, fid)
}
