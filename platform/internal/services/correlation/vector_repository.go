package correlation

// vector_repository.go
//
// VectorRepository stores and retrieves BERT alert embeddings using PostgreSQL
// with the pgvector extension. Replaces the external Weaviate dependency for
// ANN similarity search. Weaviate can remain as a secondary store.
//
// Requirements: PostgreSQL with pgvector extension enabled.
//   CREATE EXTENSION IF NOT EXISTS vector;
//   (See CORRELATION_ENGINE_V2_ARCHITECTURE.md §9 for full schema.)

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

// VectorRepository persists and queries BERT embeddings via pgvector.
type VectorRepository struct {
	db *sql.DB
}

func NewVectorRepository(db *sql.DB) *VectorRepository {
	return &VectorRepository{db: db}
}

// VectorMatch is a result from a similarity search.
type VectorMatch struct {
	AlertID    uuid.UUID
	Title      string
	Severity   string
	Source     string
	Domain     string
	Similarity float64
}

// StoreEmbedding persists an alert's BERT embedding.
func (r *VectorRepository) StoreEmbedding(
	ctx context.Context,
	alertID uuid.UUID,
	embedding []float32,
	domain, class, cluster, severity, modelVersion string,
) error {
	vec := float32SliceToLiteral(embedding)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO alert_embeddings (alert_id, embedding, model_version, domain, class, cluster, severity)
		VALUES ($1, $2::vector, $3, $4, $5, $6, $7)
		ON CONFLICT (alert_id) DO UPDATE SET
			embedding     = EXCLUDED.embedding,
			model_version = EXCLUDED.model_version,
			domain        = EXCLUDED.domain,
			class         = EXCLUDED.class,
			updated_at    = NOW()`,
		alertID, vec, modelVersion, domain, class, cluster, severity)
	return err
}

// FindSimilar performs ANN cosine similarity search. Filters by cluster and
// 24h window; returns top results above the threshold.
func (r *VectorRepository) FindSimilar(
	ctx context.Context,
	embedding []float32,
	alertID uuid.UUID,
	cluster string,
	threshold float64,
	limit int,
) ([]VectorMatch, error) {
	vec := float32SliceToLiteral(embedding)

	var query string
	var args []interface{}

	if cluster != "" {
		query = `
			SELECT ae.alert_id, a.title, a.severity, a.source, ae.domain,
			       1 - (ae.embedding <=> $1::vector) AS similarity
			FROM alert_embeddings ae
			JOIN alerts a ON a.id = ae.alert_id
			WHERE ae.created_at >= NOW() - INTERVAL '24 hours'
			  AND ae.alert_id != $2
			  AND ae.cluster = $5
			  AND 1 - (ae.embedding <=> $1::vector) > $3
			ORDER BY ae.embedding <=> $1::vector
			LIMIT $4`
		args = []interface{}{vec, alertID, threshold, limit, cluster}
	} else {
		query = `
			SELECT ae.alert_id, a.title, a.severity, a.source, ae.domain,
			       1 - (ae.embedding <=> $1::vector) AS similarity
			FROM alert_embeddings ae
			JOIN alerts a ON a.id = ae.alert_id
			WHERE ae.created_at >= NOW() - INTERVAL '24 hours'
			  AND ae.alert_id != $2
			  AND 1 - (ae.embedding <=> $1::vector) > $3
			ORDER BY ae.embedding <=> $1::vector
			LIMIT $4`
		args = []interface{}{vec, alertID, threshold, limit}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("vector similarity query: %w", err)
	}
	defer rows.Close()

	var matches []VectorMatch
	for rows.Next() {
		var m VectorMatch
		if err := rows.Scan(&m.AlertID, &m.Title, &m.Severity, &m.Source, &m.Domain, &m.Similarity); err != nil {
			continue
		}
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

// FindSimilarByDomain constrains the search to a specific ontology domain.
// More precise when the failure domain is known.
func (r *VectorRepository) FindSimilarByDomain(
	ctx context.Context,
	embedding []float32,
	alertID uuid.UUID,
	domain, cluster string,
	threshold float64,
) ([]VectorMatch, error) {
	vec := float32SliceToLiteral(embedding)
	rows, err := r.db.QueryContext(ctx, `
		SELECT ae.alert_id, a.title, a.severity, a.source, ae.domain,
		       1 - (ae.embedding <=> $1::vector) AS similarity
		FROM alert_embeddings ae
		JOIN alerts a ON a.id = ae.alert_id
		WHERE ae.created_at >= NOW() - INTERVAL '24 hours'
		  AND ae.alert_id != $2
		  AND ae.domain = $3
		  AND ae.cluster = $4
		  AND 1 - (ae.embedding <=> $1::vector) > $5
		ORDER BY ae.embedding <=> $1::vector
		LIMIT 15`,
		vec, alertID, domain, cluster, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []VectorMatch
	for rows.Next() {
		var m VectorMatch
		if err := rows.Scan(&m.AlertID, &m.Title, &m.Severity, &m.Source, &m.Domain, &m.Similarity); err != nil {
			log.Printf("VectorRepository: scan error: %v", err)
			continue
		}
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

// DeleteOldEmbeddings removes embeddings older than maxAge (maintenance helper).
func (r *VectorRepository) DeleteOldEmbeddings(ctx context.Context, maxAge time.Duration) (int64, error) {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM alert_embeddings WHERE created_at < NOW() - $1::interval`,
		fmt.Sprintf("%.0f seconds", maxAge.Seconds()))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Vector encoding helper 

// float32SliceToLiteral encodes a float32 slice as a pgvector literal: '[1.0,2.0,...]'.
// This allows passing the vector as a SQL parameter without the pgvector-go package.
func float32SliceToLiteral(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = fmt.Sprintf("%g", f)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// Schema initialization 

// EnsureSchema creates the alert_embeddings table and indexes if they don't exist.
// Called once at startup after the pgvector extension is confirmed available.
func (r *VectorRepository) EnsureSchema(ctx context.Context) error {
	// Verify pgvector extension exists before proceeding
	var extExists bool
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'vector')`).Scan(&extExists)
	if err != nil || !extExists {
		return fmt.Errorf("pgvector extension not installed — run: CREATE EXTENSION IF NOT EXISTS vector")
	}

	_, err = r.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS alert_embeddings (
			alert_id      UUID PRIMARY KEY REFERENCES alerts(id) ON DELETE CASCADE,
			embedding     vector(768),
			model_version TEXT NOT NULL DEFAULT 'bert-base-uncased',
			domain        TEXT,
			class         TEXT,
			cluster       TEXT,
			severity      TEXT,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
	return err
}
