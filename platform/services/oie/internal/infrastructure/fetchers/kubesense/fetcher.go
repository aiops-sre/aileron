package kubesense

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
)

// KubeSenseFetcher queries the kubesense_* tables (written by the AlertHub Kafka consumer)
// to surface pre-computed intelligence signals as OIE evidence:
//
//   - kubesense_health_events     → TypeKubeSenseHealthEvent (pod/node health)
//   - kubesense_config_violations → TypeKubeSenseConfigViolation (rule violations)
//   - kubesense_forecasts         → TypeKubeSenseForecast (exhaustion predictions)
//   - kubesense_apm_signals       → TypeKubeSenseAPMRegression (golden-signal regressions)
type KubeSenseFetcher struct {
	db *sql.DB
}

func NewKubeSenseFetcher(db *sql.DB) *KubeSenseFetcher {
	return &KubeSenseFetcher{db: db}
}

func (f *KubeSenseFetcher) ID() fetchers.FetcherID        { return "kubesense_signals" }
func (f *KubeSenseFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *KubeSenseFetcher) SourceName() string             { return "kubesense" }

func (f *KubeSenseFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	if f.db == nil {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	var evidence []*domain.Evidence
	now := time.Now().UTC()

	namespace := ""
	resourceName := ""
	clusterID := ""
	if req.EntityProfile != nil {
		namespace = req.EntityProfile.K8sNamespace
		resourceName = req.EntityProfile.ResourceName
		clusterID = req.EntityProfile.ClusterRef
	}
	if namespace == "" && req.Investigation != nil && req.Investigation.TopologyPath != "" {
		parts := strings.Split(req.Investigation.TopologyPath, "/")
		if len(parts) >= 2 {
			namespace = parts[0]
			if resourceName == "" && len(parts) >= 3 {
				resourceName = parts[2]
			}
		}
	}

	// ── Health events (last 2 hours) ────────────────────────────────────────────
	healthRows, err := f.db.QueryContext(ctx, `
		SELECT event_type, severity, resource_kind, namespace, resource_name, occurred_at
		FROM kubesense_health_events
		WHERE occurred_at > $1
		  AND ($2 = '' OR namespace = $2)
		  AND ($3 = '' OR resource_name = $3 OR resource_name LIKE $4)
		  AND ($5 = '' OR cluster_id = $5)
		ORDER BY occurred_at DESC
		LIMIT 20
	`, req.IncidentStartAt.Add(-2*time.Hour),
		namespace, resourceName, resourceName+"%", clusterID)
	if err == nil {
		defer healthRows.Close()
		for healthRows.Next() {
			var evType, severity, kind, ns, name string
			var occurredAt time.Time
			if err := healthRows.Scan(&evType, &severity, &kind, &ns, &name, &occurredAt); err != nil {
				continue
			}
			gapSecs := int(req.IncidentStartAt.Sub(occurredAt).Seconds())
			weight := 0.85
			if gapSecs > 1800 {
				weight = 0.65
			}
			evidence = append(evidence, &domain.Evidence{
				EvidenceType:       domain.TypeKubeSenseHealthEvent,
				Source:             "kubesense",
				Role:               domain.RoleSupports,
				Weight:             weight,
				EvidenceConfidence: 0.90,
				Description: fmt.Sprintf("KubeSense %s on %s/%s/%s (severity=%s, %ds before incident)",
					evType, kind, ns, name, severity, gapSecs),
				FetchStatus: domain.FetchSuccess,
				OccurredAt:  &occurredAt,
				GatheredAt:  now,
				CreatedAt:   now,
			})
		}
	}

	// ── Config violations (last 24 hours) ──────────────────────────────────────
	violRows, err := f.db.QueryContext(ctx, `
		SELECT rule_id, severity, resource_kind, namespace, resource_name, message, remediation, occurred_at
		FROM kubesense_config_violations
		WHERE occurred_at > $1
		  AND ($2 = '' OR namespace = $2)
		  AND ($3 = '' OR resource_name = $3 OR resource_name LIKE $4)
		  AND ($5 = '' OR cluster_id = $5)
		ORDER BY occurred_at DESC
		LIMIT 10
	`, req.IncidentStartAt.Add(-24*time.Hour),
		namespace, resourceName, resourceName+"%", clusterID)
	if err == nil {
		defer violRows.Close()
		for violRows.Next() {
			var ruleID, severity, kind, ns, name, message, remediation string
			var occurredAt time.Time
			if err := violRows.Scan(&ruleID, &severity, &kind, &ns, &name, &message, &remediation, &occurredAt); err != nil {
				continue
			}
			evidence = append(evidence, &domain.Evidence{
				EvidenceType:       domain.TypeKubeSenseConfigViolation,
				Source:             "kubesense",
				Role:               domain.RoleSupports,
				Weight:             0.88,
				EvidenceConfidence: 0.95,
				Description: fmt.Sprintf("Config violation [%s] on %s/%s/%s: %s. Remediation: %s",
					ruleID, kind, ns, name, message, remediation),
				FetchStatus: domain.FetchSuccess,
				OccurredAt:  &occurredAt,
				GatheredAt:  now,
				CreatedAt:   now,
			})
		}
	}

	// ── Forecasts (breach within ±6h of incident) ──────────────────────────────
	forecastRows, err := f.db.QueryContext(ctx, `
		SELECT target, resource_kind, namespace, resource_name, current_value, threshold,
		       predicted_breach, trend_per_day, model_confidence
		FROM kubesense_forecasts
		WHERE predicted_breach BETWEEN $1 AND $2
		  AND ($3 = '' OR namespace = $3)
		  AND ($4 = '' OR resource_name = $4 OR resource_name LIKE $5)
		  AND ($6 = '' OR cluster_id = $6)
		ORDER BY predicted_breach ASC
		LIMIT 5
	`, req.IncidentStartAt.Add(-6*time.Hour), req.IncidentStartAt.Add(2*time.Hour),
		namespace, resourceName, resourceName+"%", clusterID)
	if err == nil {
		defer forecastRows.Close()
		for forecastRows.Next() {
			var target, kind, ns, name string
			var currentVal, threshold, trendPerDay, modelConf float64
			var predictedBreach time.Time
			if err := forecastRows.Scan(&target, &kind, &ns, &name, &currentVal, &threshold,
				&predictedBreach, &trendPerDay, &modelConf); err != nil {
				continue
			}
			evidence = append(evidence, &domain.Evidence{
				EvidenceType:       domain.TypeKubeSenseForecast,
				Source:             "kubesense",
				Role:               domain.RoleSupports,
				Weight:             0.85,
				EvidenceConfidence: modelConf,
				Description: fmt.Sprintf("KubeSense forecast: %s/%s/%s %s at %.1f%% (threshold %.1f%%), predicted breach at %s (trend %.2f/day)",
					kind, ns, name, target, currentVal*100, threshold*100,
					predictedBreach.Format("15:04"), trendPerDay),
				FetchStatus: domain.FetchSuccess,
				OccurredAt:  &predictedBreach,
				GatheredAt:  now,
				CreatedAt:   now,
			})
		}
	}

	// ── APM regressions (golden-signal EWMA anomalies, ±30min of incident) ─────
	if namespace != "" && resourceName != "" {
		apmRows, err := f.db.QueryContext(ctx, `
			SELECT event_type, severity, namespace, resource_name, occurred_at
			FROM kubesense_health_events
			WHERE event_type LIKE 'apm.regression.%'
			  AND occurred_at BETWEEN $1 AND $2
			  AND namespace = $3
			  AND (resource_name = $4 OR resource_name LIKE $5)
			ORDER BY occurred_at DESC
			LIMIT 10
		`, req.IncidentStartAt.Add(-30*time.Minute), req.IncidentStartAt.Add(5*time.Minute),
			namespace, resourceName, resourceName+"%")
		if err == nil {
			defer apmRows.Close()
			for apmRows.Next() {
				var evType, severity, ns, name string
				var occurredAt time.Time
				if err := apmRows.Scan(&evType, &severity, &ns, &name, &occurredAt); err != nil {
					continue
				}
				dimension := strings.TrimPrefix(evType, "apm.regression.")
				gapSecs := int(req.IncidentStartAt.Sub(occurredAt).Seconds())
				evidence = append(evidence, &domain.Evidence{
					EvidenceType:       domain.TypeKubeSenseAPMRegression,
					Source:             "kubesense",
					Role:               domain.RoleSupports,
					Weight:             0.75,
					EvidenceConfidence: 0.85,
					Description: fmt.Sprintf("APM regression: %s/%s %s degradation detected %ds before incident (severity=%s)",
						ns, name, dimension, gapSecs, severity),
					FetchStatus: domain.FetchSuccess,
					OccurredAt:  &occurredAt,
					GatheredAt:  now,
					CreatedAt:   now,
				})
			}
		}
	}

	// ── Chaos readiness scores (last 2h — low score = SPOF risk) ──────────────
	chaosRows, err := f.db.QueryContext(ctx, `
		SELECT cluster_id, severity, occurred_at
		FROM kubesense_health_events
		WHERE event_type = 'chaos.score'
		  AND occurred_at > $1
		  AND ($2 = '' OR cluster_id = $2)
		  AND severity IN ('critical', 'high')
		ORDER BY occurred_at DESC
		LIMIT 3
	`, req.IncidentStartAt.Add(-2*time.Hour), clusterID)
	if err == nil {
		defer chaosRows.Close()
		for chaosRows.Next() {
			var cCluster, severity string
			var occurredAt time.Time
			if err := chaosRows.Scan(&cCluster, &severity, &occurredAt); err != nil {
				continue
			}
			evidence = append(evidence, &domain.Evidence{
				EvidenceType:       domain.TypeKubeSenseChaosScore,
				Source:             "kubesense",
				Role:               domain.RoleSupports,
				Weight:             0.70,
				EvidenceConfidence: 0.88,
				Description: fmt.Sprintf("Cluster %s chaos readiness is %s — missing PDB, single replicas, or no resource limits detected before incident",
					cCluster, severity),
				FetchStatus: domain.FetchSuccess,
				OccurredAt:  &occurredAt,
				GatheredAt:  now,
				CreatedAt:   now,
			})
		}
	}

	// ── GitOps drift events (last 6h — drift = probable root cause) ───────────
	driftRows, err := f.db.QueryContext(ctx, `
		SELECT event_type, severity, resource_kind, namespace, resource_name, occurred_at
		FROM kubesense_health_events
		WHERE event_type LIKE 'drift.%'
		  AND occurred_at BETWEEN $1 AND $2
		  AND ($3 = '' OR namespace = $3)
		  AND ($4 = '' OR resource_name = $4 OR resource_name LIKE $5)
		ORDER BY occurred_at DESC
		LIMIT 5
	`, req.IncidentStartAt.Add(-6*time.Hour), req.IncidentStartAt.Add(10*time.Minute),
		namespace, resourceName, resourceName+"%")
	if err == nil {
		defer driftRows.Close()
		for driftRows.Next() {
			var evType, severity, kind, ns, name string
			var occurredAt time.Time
			if err := driftRows.Scan(&evType, &severity, &kind, &ns, &name, &occurredAt); err != nil {
				continue
			}
			driftType := strings.TrimPrefix(evType, "drift.")
			gapSecs := int(req.IncidentStartAt.Sub(occurredAt).Seconds())
			evidence = append(evidence, &domain.Evidence{
				EvidenceType:       domain.TypeKubeSenseDrift,
				Source:             "kubesense",
				Role:               domain.RoleSupports,
				Weight:             0.88, // drift before incident = very strong causal signal
				EvidenceConfidence: 0.90,
				Description: fmt.Sprintf("GitOps drift detected on %s/%s/%s: %s (%ds before incident, severity=%s)",
					kind, ns, name, driftType, gapSecs, severity),
				FetchStatus: domain.FetchSuccess,
				OccurredAt:  &occurredAt,
				GatheredAt:  now,
				CreatedAt:   now,
			})
		}
	}
	// Return FetchMissing (not FetchSuccess) when the query executed but returned no rows.
	// FetchSuccess with empty evidence misleads the hypothesis scorer into thinking the
	// fetch "worked" and found nothing — identical to FetchSuccess with data. FetchMissing
	// correctly signals "this source has no data for this incident" so the scorer can
	// apply the missing-evidence penalty if this was a required evidence type.
	if len(evidence) == 0 {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}
	return &fetchers.FetchResult{Evidence: evidence, Status: domain.FetchSuccess}, nil
}
