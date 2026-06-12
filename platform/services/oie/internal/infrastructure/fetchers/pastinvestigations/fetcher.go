package pastinvestigations

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
)

// PastInvestigationsFetcher queries completed investigations for similar incidents.
// It uses two strategies ranked by quality:
//   1. Semantic similarity via pgvector + nomic-embed-text embeddings (when available)
//   2. Structural matching on domain, entity_type, and namespace prefix (fallback)
//
// Benefit: the hypothesis engine scores higher when the same failure pattern was
// confirmed previously. The narrator references past resolution steps in the narrative.
type PastInvestigationsFetcher struct {
	db         *sql.DB
	ollamaURL  string
	httpClient *http.Client
}

func NewPastInvestigationsFetcher(db *sql.DB) *PastInvestigationsFetcher {
	return &PastInvestigationsFetcher{
		db:         db,
		ollamaURL:  "http://ollama.aileron.svc.cluster.local:11434",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// SetOllamaURL configures the Ollama endpoint for embedding generation.
func (f *PastInvestigationsFetcher) SetOllamaURL(url string) { f.ollamaURL = url }

func (f *PastInvestigationsFetcher) ID() fetchers.FetcherID          { return "past_investigations" }
func (f *PastInvestigationsFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *PastInvestigationsFetcher) SourceName() string              { return "history" }

func (f *PastInvestigationsFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	if f.db == nil {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	domain_, entityType, namespace := extractContext(req)
	if domain_ == "" && entityType == "" && namespace == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	// Build a text summary of the current incident for embedding.
	incidentText := buildIncidentText(req, domain_, entityType, namespace)

	// Try semantic search first (pgvector + nomic-embed-text).
	var evidence []*domain.Evidence
	if emb := f.getEmbedding(ctx, incidentText); len(emb) > 0 {
		evidence = f.semanticSearch(ctx, req, emb)
	}

	// Fall back to structural matching if semantic search found nothing.
	if len(evidence) == 0 {
		evidence = f.structuralSearch(ctx, req, domain_, entityType, namespace)
	}

	if len(evidence) == 0 {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}
	return &fetchers.FetchResult{Evidence: evidence, Status: domain.FetchSuccess}, nil
}

// ─── Semantic search (pgvector) ────────────────────────────────────────────────

func (f *PastInvestigationsFetcher) semanticSearch(ctx context.Context, req *fetchers.FetchRequest, embedding []float64) []*domain.Evidence {
	// Format embedding as PostgreSQL array literal.
	var sb strings.Builder
	sb.WriteByte('[')
	for i, v := range embedding {
		if i > 0 { sb.WriteByte(',') }
		fmt.Fprintf(&sb, "%.6f", v)
	}
	sb.WriteByte(']')
	embStr := sb.String()

	rows, err := f.db.QueryContext(ctx, `
		SELECT
			ri.id::text,
			COALESCE(i.incident_number, 'INC-?'),
			COALESCE(i.title, ri.incident_id::text),
			COALESCE(ri.domain, ''),
			COALESCE(ri.root_cause_summary, ''),
			COALESCE(ri.confidence, 0),
			ri.created_at,
			1 - (ri.embedding <=> $1::vector) AS similarity
		FROM rca_investigations ri
		LEFT JOIN incidents i ON i.id = ri.incident_id
		WHERE ri.created_at > NOW() - INTERVAL '30 days'
		  AND ri.status IN ('completed', 'success')
		  AND ri.incident_id != $2
		  AND ri.embedding IS NOT NULL
		  AND 1 - (ri.embedding <=> $1::vector) > 0.70
		ORDER BY ri.embedding <=> $1::vector
		LIMIT 5
	`, embStr, req.Investigation.IncidentID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	return scanInvestigations(rows, true)
}

// ─── Structural search (text matching fallback) ────────────────────────────────

func (f *PastInvestigationsFetcher) structuralSearch(ctx context.Context, req *fetchers.FetchRequest, domain_, entityType, namespace string) []*domain.Evidence {
	rows, err := f.db.QueryContext(ctx, `
		SELECT
			ri.id::text,
			COALESCE(i.incident_number, 'INC-?'),
			COALESCE(i.title, ri.incident_id::text),
			COALESCE(ri.domain, ''),
			COALESCE(ri.root_cause_summary, ''),
			COALESCE(ri.confidence, 0),
			ri.created_at,
			0.5 AS similarity
		FROM rca_investigations ri
		LEFT JOIN incidents i ON i.id = ri.incident_id
		WHERE ri.created_at > NOW() - INTERVAL '30 days'
		  AND ri.status IN ('completed', 'success')
		  AND ri.incident_id != $1
		  AND (
		    ($2 != '' AND COALESCE(ri.domain, '') = $2)
		    OR ($3 != '' AND COALESCE(ri.data->'go_context'->>'entity_type', '') = $3)
		    OR ($4 != '' AND COALESCE(i.topology_path, '') LIKE $5)
		  )
		ORDER BY ri.confidence DESC, ri.created_at DESC
		LIMIT 5
	`, req.Investigation.IncidentID, domain_, entityType, namespace, namespace+"/%")
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanInvestigations(rows, false)
}

// ─── Embedding generation (nomic-embed-text via Ollama) ───────────────────────

func (f *PastInvestigationsFetcher) getEmbedding(ctx context.Context, text string) []float64 {
	if f.ollamaURL == "" || text == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]string{"model": "nomic-embed-text", "prompt": text})
	req, err := http.NewRequestWithContext(ctx, "POST", f.ollamaURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil { return nil }
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.httpClient.Do(req)
	if err != nil { return nil }
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var result struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.Unmarshal(raw, &result); err != nil { return nil }
	return result.Embedding
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func extractContext(req *fetchers.FetchRequest) (domain_, entityType, namespace string) {
	if req.Investigation != nil {
		if req.Investigation.Domain != nil { domain_ = *req.Investigation.Domain }
		if req.Investigation.RootEntityType != nil { entityType = *req.Investigation.RootEntityType }
		if req.Investigation.TopologyPath != "" {
			parts := strings.Split(req.Investigation.TopologyPath, "/")
			if len(parts) >= 2 { namespace = parts[0] }
		}
	}
	if req.EntityProfile != nil {
		if entityType == "" { entityType = req.EntityProfile.EntityType }
		if namespace == "" { namespace = req.EntityProfile.K8sNamespace }
	}
	return
}

func buildIncidentText(req *fetchers.FetchRequest, domain_, entityType, namespace string) string {
	var parts []string
	if domain_ != "" { parts = append(parts, "domain:"+domain_) }
	if entityType != "" { parts = append(parts, "entity:"+entityType) }
	if namespace != "" { parts = append(parts, "namespace:"+namespace) }
	if req.Investigation != nil {
		if req.Investigation.TopologyPath != "" { parts = append(parts, "path:"+req.Investigation.TopologyPath) }
		if req.Investigation.FailureClass != nil { parts = append(parts, "failure:"+*req.Investigation.FailureClass) }
	}
	return strings.Join(parts, " ")
}

func scanInvestigations(rows *sql.Rows, hasSimilarity bool) []*domain.Evidence {
	var evidence []*domain.Evidence
	now := time.Now().UTC()
	for rows.Next() {
		var id, number, title, dom, rootCause string
		var confidence, similarity float64
		var createdAt time.Time
		if err := rows.Scan(&id, &number, &title, &dom, &rootCause, &confidence, &createdAt, &similarity); err != nil {
			continue
		}
		description := fmt.Sprintf(
			"Past investigation %s (%s, confidence=%.0f%%, similarity=%.0f%%): %s — Root cause: %s",
			number, dom, confidence*100, similarity*100, title, truncateStr(rootCause, 150),
		)
		weight := 0.60
		if confidence > 0.80 { weight = 0.75 }
		if similarity > 0.85 { weight = 0.80 } // high semantic similarity = very strong context

		evidence = append(evidence, &domain.Evidence{
			EvidenceType:       domain.TypeSimilarIncident,
			Source:             "history",
			Role:               domain.RoleContext,
			Weight:             weight,
			EvidenceConfidence: confidence,
			Description:        description,
			FetchStatus:        domain.FetchSuccess,
			OccurredAt:         &createdAt,
			GatheredAt:         now,
			CreatedAt:          now,
		})
	}
	return evidence
}

func truncateStr(s string, max int) string {
	if len(s) <= max { return s }
	return s[:max] + "…"
}

// GenerateAndStoreEmbedding generates a semantic embedding for a completed
// investigation and stores it in the DB. Called by the OIE manager after
// investigation completion so future similar incidents can find this one.
func GenerateAndStoreEmbedding(ctx context.Context, db *sql.DB, ollamaURL string, investigationID string, text string) {
	if db == nil || ollamaURL == "" || text == "" { return }

	client := &http.Client{Timeout: 10 * time.Second}
	body, _ := json.Marshal(map[string]string{"model": "nomic-embed-text", "prompt": text})
	req, err := http.NewRequestWithContext(ctx, "POST", ollamaURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil { return }
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil { return }
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var result struct{ Embedding []float64 `json:"embedding"` }
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Embedding) == 0 { return }

	// Format as PostgreSQL vector literal.
	var sb strings.Builder
	sb.WriteByte('[')
	for i, v := range result.Embedding {
		if i > 0 { sb.WriteByte(',') }
		fmt.Fprintf(&sb, "%.6f", v)
	}
	sb.WriteByte(']')

	_, _ = db.ExecContext(ctx,
		"UPDATE rca_investigations SET embedding = $1::vector WHERE id = $2",
		sb.String(), investigationID)
}
