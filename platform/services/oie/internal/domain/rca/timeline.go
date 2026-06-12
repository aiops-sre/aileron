package rca

import "time"

// TimelineEvent is one chronological entry in the pre-incident timeline.
// Combines changes from OKG, K8s events, and evidence observations.
type TimelineEvent struct {
	OccurredAt  time.Time `json:"occurred_at"`
	// "change" | "k8s_event" | "evidence_observation" | "incident_start"
	Category    string    `json:"category"`
	// Machine-readable type (e.g. "deployment_event", "k8s_node_event", "oom_kill")
	EventType   string    `json:"event_type"`
	Source      string    `json:"source"`
	Description string    `json:"description"`
	// DeltaMinutes: negative = before incident, positive = after incident start.
	DeltaMinutes int      `json:"delta_minutes"`
	// Significance: "high" | "medium" | "low"
	Significance string   `json:"significance"`
	// EntityName is the affected entity, if applicable.
	EntityName  string    `json:"entity_name,omitempty"`
}

// BuildTimeline merges multiple event slices into a sorted, deduplicated timeline.
// The incidentStartAt anchor is used to compute DeltaMinutes for each event.
func BuildTimeline(events []TimelineEvent, incidentStartAt time.Time) []TimelineEvent {
	// Insert the incident start marker.
	events = append(events, TimelineEvent{
		OccurredAt:   incidentStartAt,
		Category:     "incident_start",
		EventType:    "incident_created",
		Source:       "alerthub",
		Description:  "Incident created by AlertHub",
		DeltaMinutes: 0,
		Significance: "high",
	})

	// Compute delta minutes for every event.
	for i := range events {
		delta := events[i].OccurredAt.Sub(incidentStartAt).Minutes()
		events[i].DeltaMinutes = int(delta)
	}

	// Sort chronologically.
	sortTimeline(events)

	// Deduplicate: if two events have the same description and are within 30s, keep one.
	return deduplicateTimeline(events)
}

func sortTimeline(events []TimelineEvent) {
	n := len(events)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && events[j].OccurredAt.Before(events[j-1].OccurredAt); j-- {
			events[j], events[j-1] = events[j-1], events[j]
		}
	}
}

func deduplicateTimeline(events []TimelineEvent) []TimelineEvent {
	if len(events) == 0 {
		return events
	}
	result := []TimelineEvent{events[0]}
	for i := 1; i < len(events); i++ {
		prev := result[len(result)-1]
		curr := events[i]
		gap := curr.OccurredAt.Sub(prev.OccurredAt).Abs()
		if gap < 30*time.Second && curr.Description == prev.Description {
			continue // duplicate
		}
		result = append(result, curr)
	}
	return result
}
