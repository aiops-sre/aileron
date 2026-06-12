package runbooks

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
)

// RunbookFetcher queries the investigation_runbooks table for matching runbooks
// and injects them as TypeContext evidence at investigation start.
// HolmesGPT SkillCatalog pattern: the LLM receives domain-specific runbooks
// alongside the evidence so it can reference the team's known remediation steps.
type RunbookFetcher struct {
	db *sql.DB
}

func NewRunbookFetcher(db *sql.DB) *RunbookFetcher {
	return &RunbookFetcher{db: db}
}

func (f *RunbookFetcher) ID() fetchers.FetcherID          { return "investigation_runbooks" }
func (f *RunbookFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *RunbookFetcher) SourceName() string              { return "runbooks" }

func (f *RunbookFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	if f.db == nil {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	domainStr := ""
	entityType := ""
	failureClass := ""

	if req.Investigation != nil {
		if req.Investigation.Domain != nil {
			domainStr = *req.Investigation.Domain
		}
		if req.Investigation.RootEntityType != nil {
			entityType = *req.Investigation.RootEntityType
		}
		if req.Investigation.FailureClass != nil {
			failureClass = *req.Investigation.FailureClass
		}
	}
	if req.EntityProfile != nil && entityType == "" {
		entityType = req.EntityProfile.EntityType
	}

	if domainStr == "" && entityType == "" && failureClass == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	// Match runbooks by domain + entity_type + failure_class.
	// A runbook with empty fields matches anything (wildcard).
	rows, err := f.db.QueryContext(ctx, `
		SELECT name, content, source
		FROM investigation_runbooks
		WHERE enabled = true
		  AND (domain = '' OR $1 = '' OR domain = $1)
		  AND (entity_type = '' OR $2 = '' OR entity_type = $2)
		  AND (failure_class = '' OR $3 = '' OR failure_class = $3)
		ORDER BY
			-- Prefer exact match: all three fields match
			(CASE WHEN domain = $1 AND entity_type = $2 AND failure_class = $3 THEN 0
			      WHEN domain = $1 AND entity_type = $2 THEN 1
			      WHEN entity_type = $2 AND failure_class = $3 THEN 1
			      WHEN domain = $1 THEN 2
			      ELSE 3 END),
			created_at DESC
		LIMIT 3
	`, domainStr, entityType, failureClass)
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}
	defer rows.Close()

	var evidence []*domain.Evidence
	now := time.Now().UTC()

	for rows.Next() {
		var name, content, source string
		if err := rows.Scan(&name, &content, &source); err != nil {
			continue
		}
		// Truncate runbook content for the prompt — full runbooks can be >10k chars.
		excerpt := truncateRunbook(content, 600)
		evidence = append(evidence, &domain.Evidence{
			EvidenceType:       domain.TypeSimilarIncident, // reuse TypeSimilarIncident as "context" carrier
			Source:             "runbooks",
			Role:               domain.RoleContext,
			Weight:             0.50,
			EvidenceConfidence: 0.95, // runbooks are authoritative
			Description: fmt.Sprintf("Runbook [%s] from %s: %s",
				name, source, excerpt),
			FetchStatus: domain.FetchSuccess,
			GatheredAt:  now,
			CreatedAt:   now,
		})
	}

	if len(evidence) == 0 {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}
	return &fetchers.FetchResult{Evidence: evidence, Status: domain.FetchSuccess}, nil
}

func truncateRunbook(content string, maxChars int) string {
	content = strings.TrimSpace(content)
	if len(content) <= maxChars {
		return content
	}
	// Cut at last sentence boundary within maxChars.
	cut := content[:maxChars]
	if idx := strings.LastIndexAny(cut, ".!?\n"); idx > maxChars/2 {
		cut = cut[:idx+1]
	}
	return cut + " [...]"
}
