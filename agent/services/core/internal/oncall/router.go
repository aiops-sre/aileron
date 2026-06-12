// Package oncall implements intelligent incident routing.
//
// When an incident fires, KubeSense doesn't just page whoever is on call.
// It knows who has successfully resolved the same failure mode before, how
// quickly they resolved it, and whether they're currently handling another
// incident. It routes to the most capable available responder and surfaces
// the most relevant past experience to whoever picks up the page.
//
// SRE benefit: eliminates the 10–20 minutes wasted when the wrong person is
// paged for a complex incident they've never seen, and provides the context
// every responder needs to act immediately instead of starting from scratch.
//
// Market gap: PagerDuty routes by schedule. OpsGenie routes by escalation policy.
// Neither routes by EXPERTISE or surfaces WHAT THIS ENGINEER DID LAST TIME.
package oncall

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Engineer is an on-call engineer with tracked incident history.
type Engineer struct {
	ID          string
	Name        string
	Email       string
	Team        string
	Timezone    string
	IsAvailable bool // not currently handling another incident
}

// ExpertiseRecord tracks an engineer's history with a failure mode.
type ExpertiseRecord struct {
	EngineerID    string
	FailureMode   string     // "OOMKilled" | "CrashLoopBackOff" | "NodeNotReady"
	ResourceKind  string     // "Deployment" | "StatefulSet" | etc.
	Namespace     string     // specific namespace or "" for any
	ResolvedCount int        // number of times successfully resolved
	AvgResolutionMins float64 // average time to resolve
	LastResolvedAt time.Time
	SuccessRate   float64    // 0-1
	BestCommand   string     // the kubectl command that worked most often
}

// RoutingRecommendation tells the caller who to page and why.
type RoutingRecommendation struct {
	Primary         *EngineerRecommendation   `json:"primary"`
	Fallbacks       []*EngineerRecommendation `json:"fallbacks"`
	IncidentID      string                    `json:"incident_id"`
	FailureMode     string                    `json:"failure_mode"`
	RoutingBasis    string                    `json:"routing_basis"`
	PastContext     *PastContext              `json:"past_context,omitempty"`
}

// EngineerRecommendation is one candidate responder with context.
type EngineerRecommendation struct {
	Engineer          Engineer    `json:"engineer"`
	Score             float64     `json:"score"`         // 0-1, higher = better match
	ExpertiseLevel    string      `json:"expertise"`     // expert / experienced / familiar / novice
	ResolvedBefore    int         `json:"resolved_before"`
	AvgResolutionMins float64     `json:"avg_resolution_mins"`
	LastResolvedAt    time.Time   `json:"last_resolved_at,omitempty"`
	WhyRecommended    string      `json:"why_recommended"`
	BriefingNotes     []string    `json:"briefing_notes"` // what this engineer should know first
}

// PastContext surfaces what happened last time this failure mode occurred.
type PastContext struct {
	LastOccurredAt   time.Time `json:"last_occurred_at"`
	ResolvedByName   string    `json:"resolved_by"`
	ResolutionMins   float64   `json:"resolution_mins"`
	WhatWorked       string    `json:"what_worked"`      // the command/action that fixed it
	WhatFailed       string    `json:"what_failed"`      // what was tried and didn't work
	SimilarIncidents int       `json:"similar_incidents_30d"`
}

// Router matches incidents to the best available responder.
type Router struct {
	mu         sync.RWMutex
	engineers  map[string]Engineer          // key: engineer ID
	expertise  []ExpertiseRecord
	schedules  []ScheduleEntry              // on-call schedule
	activePages map[string]string           // engineer ID -> incident ID (currently handling)
}

// ScheduleEntry is an on-call rotation entry.
type ScheduleEntry struct {
	EngineerID string
	Team       string
	StartsAt   time.Time
	EndsAt     time.Time
}

// NewRouter creates an incident router.
func NewRouter() *Router {
	return &Router{
		engineers:   make(map[string]Engineer),
		activePages: make(map[string]string),
	}
}

// RegisterEngineer adds or updates an engineer profile.
func (r *Router) RegisterEngineer(eng Engineer) {
	r.mu.Lock()
	r.engineers[eng.ID] = eng
	r.mu.Unlock()
}

// AddScheduleEntry registers an on-call rotation entry.
func (r *Router) AddScheduleEntry(entry ScheduleEntry) {
	r.mu.Lock()
	r.schedules = append(r.schedules, entry)
	r.mu.Unlock()
}

// RecordResolution records that an engineer resolved an incident.
// Call this when an incident closes with a known resolution.
func (r *Router) RecordResolution(
	engineerID, failureMode, resourceKind, namespace, command string,
	resolutionMins float64,
	successful bool,
) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Update existing expertise record if it exists
	for i := range r.expertise {
		e := &r.expertise[i]
		if e.EngineerID == engineerID && e.FailureMode == failureMode &&
			e.ResourceKind == resourceKind {
			e.ResolvedCount++
			// Update running average
			e.AvgResolutionMins = (e.AvgResolutionMins*float64(e.ResolvedCount-1)+resolutionMins) / float64(e.ResolvedCount)
			e.LastResolvedAt = time.Now()
			if successful {
				e.SuccessRate = (e.SuccessRate*float64(e.ResolvedCount-1) + 1.0) / float64(e.ResolvedCount)
			} else {
				e.SuccessRate = (e.SuccessRate * float64(e.ResolvedCount-1)) / float64(e.ResolvedCount)
			}
			if command != "" && successful {
				e.BestCommand = command
			}
			return
		}
	}

	// New expertise record
	successRate := 0.0
	if successful {
		successRate = 1.0
	}
	r.expertise = append(r.expertise, ExpertiseRecord{
		EngineerID:        engineerID,
		FailureMode:       failureMode,
		ResourceKind:      resourceKind,
		Namespace:         namespace,
		ResolvedCount:     1,
		AvgResolutionMins: resolutionMins,
		LastResolvedAt:    time.Now(),
		SuccessRate:       successRate,
		BestCommand:       command,
	})

	// Unmark as active
	delete(r.activePages, engineerID)
}

// MarkActive marks an engineer as actively handling an incident.
func (r *Router) MarkActive(engineerID, incidentID string) {
	r.mu.Lock()
	r.activePages[engineerID] = incidentID
	r.mu.Unlock()
}

// Route recommends the best responder for an incident.
func (r *Router) Route(
	incidentID, failureMode, resourceKind, namespace string,
	availableEngineers []string, // IDs of currently on-call engineers
) *RoutingRecommendation {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(availableEngineers) == 0 {
		// Fall back to all registered engineers
		for id := range r.engineers {
			availableEngineers = append(availableEngineers, id)
		}
	}

	var candidates []*EngineerRecommendation
	for _, engID := range availableEngineers {
		eng, ok := r.engineers[engID]
		if !ok {
			continue
		}
		// Skip engineers currently handling another incident
		if activeInc, busy := r.activePages[engID]; busy && activeInc != incidentID {
			continue
		}

		rec := r.scoreEngineer(eng, failureMode, resourceKind, namespace)
		candidates = append(candidates, rec)
	}

	// Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	rec := &RoutingRecommendation{
		IncidentID:  incidentID,
		FailureMode: failureMode,
	}

	if len(candidates) == 0 {
		return rec
	}

	rec.Primary = candidates[0]
	if len(candidates) > 1 {
		rec.Fallbacks = candidates[1:min3(len(candidates), 4)]
	}

	// Routing basis explanation
	if rec.Primary.ResolvedBefore > 0 {
		rec.RoutingBasis = fmt.Sprintf("expertise: %s has resolved %s %d time(s), avg %.0f min",
			rec.Primary.Engineer.Name, failureMode, rec.Primary.ResolvedBefore, rec.Primary.AvgResolutionMins)
	} else {
		rec.RoutingBasis = fmt.Sprintf("schedule: %s is on call, no prior %s experience",
			rec.Primary.Engineer.Name, failureMode)
	}

	// Past context
	rec.PastContext = r.buildPastContext(failureMode, resourceKind)

	return rec
}

// ─── Scoring ──────────────────────────────────────────────────────────────────

func (r *Router) scoreEngineer(
	eng Engineer,
	failureMode, resourceKind, namespace string,
) *EngineerRecommendation {
	rec := &EngineerRecommendation{
		Engineer: eng,
		Score:    0.30, // base score for being on call
	}

	// Find best matching expertise record
	var bestExp *ExpertiseRecord
	bestScore := 0.0
	for i := range r.expertise {
		e := &r.expertise[i]
		if e.EngineerID != eng.ID {
			continue
		}
		matchScore := expertiseMatchScore(e, failureMode, resourceKind, namespace)
		if matchScore > bestScore {
			bestScore = matchScore
			bestExp = e
		}
	}

	if bestExp != nil {
		// Expertise score: volume + recency + success rate
		volumeScore := float64(min3(bestExp.ResolvedCount, 10)) / 10.0 * 0.40
		recencyDays := time.Since(bestExp.LastResolvedAt).Hours() / 24
		recencyScore := (1.0 - min1(recencyDays/90, 1.0)) * 0.20
		successScore := bestExp.SuccessRate * 0.30

		rec.Score = 0.10 + volumeScore + recencyScore + successScore
		rec.ResolvedBefore = bestExp.ResolvedCount
		rec.AvgResolutionMins = bestExp.AvgResolutionMins
		rec.LastResolvedAt = bestExp.LastResolvedAt

		// Briefing notes
		if bestExp.BestCommand != "" {
			rec.BriefingNotes = append(rec.BriefingNotes,
				fmt.Sprintf("Last time: `%s` resolved it in %.0f min", bestExp.BestCommand, bestExp.AvgResolutionMins))
		}
		rec.BriefingNotes = append(rec.BriefingNotes,
			fmt.Sprintf("You've resolved %s %d times (%.0f%% success rate)", failureMode, bestExp.ResolvedCount, bestExp.SuccessRate*100))
	}

	rec.ExpertiseLevel = expertiseLevel(rec.ResolvedBefore)
	rec.WhyRecommended = buildWhyRecommended(rec)
	return rec
}

func expertiseMatchScore(e *ExpertiseRecord, failureMode, resourceKind, namespace string) float64 {
	score := 0.0
	if strings.EqualFold(e.FailureMode, failureMode) {
		score += 0.60
	}
	if strings.EqualFold(e.ResourceKind, resourceKind) {
		score += 0.25
	}
	if e.Namespace != "" && strings.EqualFold(e.Namespace, namespace) {
		score += 0.15
	}
	return score
}

func (r *Router) buildPastContext(failureMode, resourceKind string) *PastContext {
	var best *ExpertiseRecord
	for i := range r.expertise {
		e := &r.expertise[i]
		if strings.EqualFold(e.FailureMode, failureMode) {
			if best == nil || e.ResolvedCount > best.ResolvedCount {
				best = e
			}
		}
	}
	if best == nil {
		return nil
	}

	// Count similar incidents in last 30 days
	count30d := 0
	cutoff := time.Now().AddDate(0, 0, -30)
	for _, e := range r.expertise {
		if strings.EqualFold(e.FailureMode, failureMode) && e.LastResolvedAt.After(cutoff) {
			count30d += e.ResolvedCount
		}
	}

	eng := r.engineers[best.EngineerID]
	return &PastContext{
		LastOccurredAt:   best.LastResolvedAt,
		ResolvedByName:   eng.Name,
		ResolutionMins:   best.AvgResolutionMins,
		WhatWorked:       best.BestCommand,
		SimilarIncidents: count30d,
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func expertiseLevel(count int) string {
	switch {
	case count >= 10:
		return "expert"
	case count >= 4:
		return "experienced"
	case count >= 1:
		return "familiar"
	default:
		return "novice"
	}
}

func buildWhyRecommended(rec *EngineerRecommendation) string {
	if rec.ResolvedBefore == 0 {
		return fmt.Sprintf("%s is on call and available", rec.Engineer.Name)
	}
	return fmt.Sprintf("%s has resolved this failure mode %d time(s), avg %.0f min",
		rec.Engineer.Name, rec.ResolvedBefore, rec.AvgResolutionMins)
}

func min3(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func min1(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
