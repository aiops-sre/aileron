// Package netapp implements OIE evidence fetchers for NetApp ONTAP clusters.
// It queries the ONTAP REST API (v9.6+) using harvest-user read-only credentials
// to gather volume utilization, aggregate state, SVM state, and node health —
// the root cause for cascading PVC/pod failures.
//
// Credentials come from two sources (tried in order):
//  1. Environment variables: OIE_NETAPP_<N>_HOST, OIE_NETAPP_<N>_USER, OIE_NETAPP_<N>_PASSWORD
//  2. alerthub-config NETAPP_CLUSTERS + NETAPP_USER env vars + OIE_NETAPP_PASSWORD secret
package netapp

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
)

// ClusterConfig holds credentials for one NetApp ONTAP cluster.
type ClusterConfig struct {
	// Name is a human-readable identifier (e.g. "netapp-mdn-cluster001").
	Name string
	// Host is the management IP or hostname (e.g. "100.67.87.8").
	Host string
	// User is the ONTAP username (harvest-user).
	User string
	// Password is the ONTAP password.
	Password string
	// Region is used to correlate with K8s cluster refs (e.g. "mdn", "rno").
	Region string
}

// client wraps the ONTAP REST API.
type client struct {
	baseURL    string
	user       string
	password   string
	httpClient *http.Client
}

func newClient(cfg ClusterConfig) *client {
	return &client{
		baseURL:  fmt.Sprintf("https://%s/api", cfg.Host),
		user:     cfg.User,
		password: cfg.Password,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // internal CA
			},
		},
	}
}

func (c *client) get(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.SetBasicAuth(c.user, c.password)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("netapp api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return fmt.Errorf("netapp auth failed (401) — check harvest-user credentials")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("netapp api %s: %d %s", path, resp.StatusCode, string(body)[:min(200, len(body))])
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

// ── ONTAP REST response types ─────────────────────────────────────────────────

type ontapListResponse struct {
	Records  json.RawMessage `json:"records"`
	NumRecords int           `json:"num_records"`
}

type ontapVolume struct {
	Name  string `json:"name"`
	UUID  string `json:"uuid"`
	State string `json:"state"` // online | offline | restricted | mixed
	SVM   struct {
		Name string `json:"name"`
		UUID string `json:"uuid"`
	} `json:"svm"`
	Space struct {
		Size        int64 `json:"size"`
		Used        int64 `json:"used"`
		PercentUsed int   `json:"percent_used"`
	} `json:"space"`
	Aggregates []struct {
		Name string `json:"name"`
	} `json:"aggregates"`
}

type ontapAggregate struct {
	Name  string `json:"name"`
	UUID  string `json:"uuid"`
	State string `json:"state"` // online | offline | restricted | degraded | unknown
	Node  struct {
		Name string `json:"name"`
	} `json:"node"`
	Space struct {
		BlockStorage struct {
			Size        int64 `json:"size"`
			Used        int64 `json:"used"`
			UsedPercent int   `json:"used_percent"`
		} `json:"block_storage"`
	} `json:"space"`
	DraggerStatus struct {
		State string `json:"state"` // normal | degraded | failed
	} `json:"block_storage_inactive_user_data_percent"`
}

type ontapSVM struct {
	Name    string `json:"name"`
	UUID    string `json:"uuid"`
	State   string `json:"state"`   // running | stopped | starting | stopping
	Subtype string `json:"subtype"` // default | dp_destination | sync_source
}

type ontapNode struct {
	Name    string `json:"name"`
	UUID    string `json:"uuid"`
	State   string `json:"state"`   // up | booting | down | failed
	Uptime  int64  `json:"uptime"`
	Version struct {
		Full string `json:"full"`
	} `json:"version"`
}

// ── NetApp Volume Fetcher ─────────────────────────────────────────────────────

// VolumeFetcher gathers per-volume utilization and state for all ONTAP clusters.
// It emits TypeNetAppVolumeFull (>85% used) and TypeNetAppVolumeState (offline/restricted).
type VolumeFetcher struct {
	clusters []ClusterConfig
}

func NewVolumeFetcher(clusters []ClusterConfig) *VolumeFetcher {
	return &VolumeFetcher{clusters: clusters}
}

func (f *VolumeFetcher) ID() fetchers.FetcherID          { return "netapp_volume_state" }
func (f *VolumeFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *VolumeFetcher) SourceName() string              { return "netapp" }

func (f *VolumeFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	profile := req.EntityProfile
	// We can fetch storage evidence even without a full entity profile —
	// volumes are correlated by SVM name, which maps to K8s namespace via PVC StorageClass.
	// Without a profile we gather all high-utilization volumes across all clusters.

	var evidence []*domain.Evidence
	gapSecs := int(time.Since(req.IncidentStartAt).Seconds())

	for _, cluster := range f.clusters {
		// Skip clusters that don't match the incident's region when we have a topology_path.
		if profile != nil && profile.ClusterRef != "" {
			if !regionMatches(profile.ClusterRef, cluster.Region) {
				continue
			}
		}

		cli := newClient(cluster)
		clusterEvidence, err := f.fetchCluster(ctx, cli, cluster, profile, gapSecs)
		if err != nil {
			// Non-fatal: emit a source-unavailable evidence piece and continue.
			evidence = append(evidence, sourceUnavailableEvidence(cluster.Name, err))
			continue
		}
		evidence = append(evidence, clusterEvidence...)
	}

	status := domain.FetchSuccess
	if len(evidence) == 0 {
		status = domain.FetchMissing
	}
	return &fetchers.FetchResult{Evidence: evidence, Status: status}, nil
}

func (f *VolumeFetcher) fetchCluster(
	ctx context.Context, cli *client, cluster ClusterConfig,
	profile *fetchers.EntityProfile, gapSecs int,
) ([]*domain.Evidence, error) {
	var response ontapListResponse
	path := "/storage/volumes?fields=name,state,space,svm,aggregates&max_records=500&return_timeout=10"
	if err := cli.get(ctx, path, &response); err != nil {
		return nil, err
	}

	var volumes []ontapVolume
	if err := json.Unmarshal(response.Records, &volumes); err != nil {
		return nil, fmt.Errorf("parsing volumes: %w", err)
	}

	var evidence []*domain.Evidence
	now := time.Now().UTC()

	for _, vol := range volumes {
		// Filter to relevant volumes when we have a namespace hint.
		if profile != nil && profile.K8sNamespace != "" {
			if !volumeRelevantToNamespace(vol.Name, vol.SVM.Name, profile.K8sNamespace) {
				continue
			}
		}

		// Emit TypeNetAppVolumeState for offline/restricted volumes.
		if vol.State != "online" && vol.State != "" {
			payload, _ := json.Marshal(domain.NetAppVolumePayload{
				ClusterName: cluster.Name,
				VolumeName:  vol.Name,
				SVMName:     vol.SVM.Name,
				State:       vol.State,
				SizeBytes:   vol.Space.Size,
				UsedBytes:   vol.Space.Used,
				PercentUsed: vol.Space.PercentUsed,
			})
			evidence = append(evidence, &domain.Evidence{
				EvidenceType:       domain.TypeNetAppVolumeState,
				Source:             "netapp",
				TemporalMode:       domain.TemporalCurrent,
				TemporalGapSecs:    &gapSecs,
				Role:               domain.RoleSupports,
				Weight:             0.92,
				Description:        fmt.Sprintf("NetApp volume %s/%s is %s on cluster %s — I/O will fail for pods using this PVC", vol.SVM.Name, vol.Name, vol.State, cluster.Name),
				Payload:            payload,
				GatheredAt:         now,
				EvidenceConfidence: temporallyDamped(0.96, gapSecs),
				FetchStatus:        domain.FetchSuccess,
				CreatedAt:          now,
			})
		}

		// Emit TypeNetAppVolumeFull for volumes ≥85% utilization.
		if vol.Space.PercentUsed >= 85 {
			weight := 0.75 + 0.20*float64(vol.Space.PercentUsed-85)/15.0 // 0.75 at 85%, 0.95 at 100%
			if weight > 0.95 {
				weight = 0.95
			}
			payload, _ := json.Marshal(domain.NetAppVolumePayload{
				ClusterName: cluster.Name,
				VolumeName:  vol.Name,
				SVMName:     vol.SVM.Name,
				State:       vol.State,
				SizeBytes:   vol.Space.Size,
				UsedBytes:   vol.Space.Used,
				PercentUsed: vol.Space.PercentUsed,
			})
			evidence = append(evidence, &domain.Evidence{
				EvidenceType:       domain.TypeNetAppVolumeFull,
				Source:             "netapp",
				TemporalMode:       domain.TemporalCurrent,
				TemporalGapSecs:    &gapSecs,
				Role:               domain.RoleSupports,
				Weight:             weight,
				Description:        fmt.Sprintf("NetApp volume %s/%s is %d%% full on cluster %s — pods writing to this PVC will get ENOSPC errors", vol.SVM.Name, vol.Name, vol.Space.PercentUsed, cluster.Name),
				Payload:            payload,
				GatheredAt:         now,
				EvidenceConfidence: temporallyDamped(0.95, gapSecs),
				FetchStatus:        domain.FetchSuccess,
				CreatedAt:          now,
			})
		}
	}

	return evidence, nil
}

// ── NetApp Aggregate Fetcher ──────────────────────────────────────────────────

// AggregateFetcher gathers ONTAP aggregate state — the root cause for cascading PVC failures.
// A degraded aggregate blocks all volume operations for PVCs backed by it.
// This is the root cause identified for the Jaeger PVC alerts (aggr1_node001..004 MDN).
type AggregateFetcher struct {
	clusters []ClusterConfig
}

func NewAggregateFetcher(clusters []ClusterConfig) *AggregateFetcher {
	return &AggregateFetcher{clusters: clusters}
}

func (f *AggregateFetcher) ID() fetchers.FetcherID          { return "netapp_aggregate_state" }
func (f *AggregateFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *AggregateFetcher) SourceName() string              { return "netapp" }

func (f *AggregateFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	profile := req.EntityProfile
	var evidence []*domain.Evidence
	gapSecs := int(time.Since(req.IncidentStartAt).Seconds())

	for _, cluster := range f.clusters {
		if profile != nil && profile.ClusterRef != "" {
			if !regionMatches(profile.ClusterRef, cluster.Region) {
				continue
			}
		}

		cli := newClient(cluster)
		clusterEvidence, err := f.fetchCluster(ctx, cli, cluster, gapSecs)
		if err != nil {
			evidence = append(evidence, sourceUnavailableEvidence(cluster.Name, err))
			continue
		}
		evidence = append(evidence, clusterEvidence...)
	}

	status := domain.FetchSuccess
	if len(evidence) == 0 {
		status = domain.FetchMissing
	}
	return &fetchers.FetchResult{Evidence: evidence, Status: status}, nil
}

func (f *AggregateFetcher) fetchCluster(
	ctx context.Context, cli *client, cluster ClusterConfig, gapSecs int,
) ([]*domain.Evidence, error) {
	var response ontapListResponse
	path := "/storage/aggregates?fields=name,state,node,space&return_timeout=10"
	if err := cli.get(ctx, path, &response); err != nil {
		return nil, err
	}

	var aggregates []ontapAggregate
	if err := json.Unmarshal(response.Records, &aggregates); err != nil {
		return nil, fmt.Errorf("parsing aggregates: %w", err)
	}

	var evidence []*domain.Evidence
	now := time.Now().UTC()

	for _, aggr := range aggregates {
		if aggr.State == "online" {
			// Check for high utilization even on healthy aggregates.
			usedPct := aggr.Space.BlockStorage.UsedPercent
			if usedPct >= 80 {
				payload, _ := json.Marshal(domain.NetAppAggregatePayload{
					ClusterName: cluster.Name,
					AggrName:    aggr.Name,
					NodeName:    aggr.Node.Name,
					State:       aggr.State,
					UsedPercent: usedPct,
					SizeBytes:   aggr.Space.BlockStorage.Size,
					UsedBytes:   aggr.Space.BlockStorage.Used,
				})
				evidence = append(evidence, &domain.Evidence{
					EvidenceType:       domain.TypeNetAppAggregateState,
					Source:             "netapp",
					TemporalMode:       domain.TemporalCurrent,
					TemporalGapSecs:    &gapSecs,
					Role:               domain.RoleContext,
					Weight:             0.40,
					Description:        fmt.Sprintf("NetApp aggregate %s on %s/%s is %d%% full (healthy but approaching limit)", aggr.Name, cluster.Name, aggr.Node.Name, usedPct),
					Payload:            payload,
					GatheredAt:         now,
					EvidenceConfidence: temporallyDamped(0.94, gapSecs),
					FetchStatus:        domain.FetchSuccess,
					CreatedAt:          now,
				})
			}
			continue
		}

		// Degraded, restricted, offline, or unknown state — this IS the root cause.
		weight := map[string]float64{
			"offline":    0.95,
			"restricted": 0.90,
			"degraded":   0.85,
		}[aggr.State]
		if weight == 0 {
			weight = 0.80 // unknown / other non-online state
		}

		payload, _ := json.Marshal(domain.NetAppAggregatePayload{
			ClusterName: cluster.Name,
			AggrName:    aggr.Name,
			NodeName:    aggr.Node.Name,
			State:       aggr.State,
			UsedPercent: aggr.Space.BlockStorage.UsedPercent,
			SizeBytes:   aggr.Space.BlockStorage.Size,
			UsedBytes:   aggr.Space.BlockStorage.Used,
		})
		evidence = append(evidence, &domain.Evidence{
			EvidenceType:       domain.TypeNetAppAggregateState,
			Source:             "netapp",
			TemporalMode:       domain.TemporalCurrent,
			TemporalGapSecs:    &gapSecs,
			Role:               domain.RoleSupports,
			Weight:             weight,
			Description:        fmt.Sprintf("NetApp aggregate %s on %s/%s is %s — all PVCs backed by this aggregate will fail I/O", aggr.Name, cluster.Name, aggr.Node.Name, strings.ToUpper(aggr.State)),
			Payload:            payload,
			GatheredAt:         now,
			EvidenceConfidence: temporallyDamped(0.96, gapSecs),
			FetchStatus:        domain.FetchSuccess,
			CreatedAt:          now,
		})
	}

	return evidence, nil
}

// ── NetApp SVM Fetcher ────────────────────────────────────────────────────────

// SVMFetcher gathers Storage Virtual Machine (SVM) state.
// A stopped/restricted SVM blocks all NFS/iSCSI access for PVCs served by it.
type SVMFetcher struct {
	clusters []ClusterConfig
}

func NewSVMFetcher(clusters []ClusterConfig) *SVMFetcher {
	return &SVMFetcher{clusters: clusters}
}

func (f *SVMFetcher) ID() fetchers.FetcherID          { return "netapp_svm_state" }
func (f *SVMFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *SVMFetcher) SourceName() string              { return "netapp" }

func (f *SVMFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	profile := req.EntityProfile
	var evidence []*domain.Evidence
	gapSecs := int(time.Since(req.IncidentStartAt).Seconds())

	for _, cluster := range f.clusters {
		if profile != nil && profile.ClusterRef != "" {
			if !regionMatches(profile.ClusterRef, cluster.Region) {
				continue
			}
		}

		cli := newClient(cluster)
		var response ontapListResponse
		if err := cli.get(ctx, "/svm/svms?fields=name,state,subtype&return_timeout=10", &response); err != nil {
			evidence = append(evidence, sourceUnavailableEvidence(cluster.Name, err))
			continue
		}

		var svms []ontapSVM
		if err := json.Unmarshal(response.Records, &svms); err != nil {
			continue
		}

		now := time.Now().UTC()
		for _, svm := range svms {
			if svm.State == "running" || svm.Subtype == "dp_destination" {
				continue // healthy or DR-destination SVM — not an issue
			}

			weight := map[string]float64{
				"stopped":  0.90,
				"starting": 0.40,
				"stopping": 0.70,
			}[svm.State]
			if weight == 0 {
				weight = 0.60
			}
			role := domain.RoleSupports
			if svm.State == "starting" {
				role = domain.RoleContext
			}

			payload, _ := json.Marshal(domain.NetAppSVMPayload{
				ClusterName: cluster.Name,
				SVMName:     svm.Name,
				State:       svm.State,
				Subtype:     svm.Subtype,
			})
			evidence = append(evidence, &domain.Evidence{
				EvidenceType:       domain.TypeNetAppSVMState,
				Source:             "netapp",
				TemporalMode:       domain.TemporalCurrent,
				TemporalGapSecs:    &gapSecs,
				Role:               role,
				Weight:             weight,
				Description:        fmt.Sprintf("NetApp SVM %s on cluster %s is %s — NFS/iSCSI access blocked for all PVCs on this SVM", svm.Name, cluster.Name, strings.ToUpper(svm.State)),
				Payload:            payload,
				GatheredAt:         now,
				EvidenceConfidence: temporallyDamped(0.94, gapSecs),
				FetchStatus:        domain.FetchSuccess,
				CreatedAt:          now,
			})
		}
	}

	status := domain.FetchSuccess
	if len(evidence) == 0 {
		status = domain.FetchMissing
	}
	return &fetchers.FetchResult{Evidence: evidence, Status: status}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// regionMatches returns true if the K8s clusterRef contains the NetApp region.
// e.g. "mps-monprod-mdn" matches region "mdn"; "mps-prod-rno" matches "rno".
func regionMatches(clusterRef, region string) bool {
	if region == "" {
		return true // no region constraint — try all clusters
	}
	return strings.Contains(strings.ToLower(clusterRef), strings.ToLower(region))
}

// volumeRelevantToNamespace returns true if the volume name or SVM name
// plausibly relates to the K8s namespace. ONTAP volume names for Trident-provisioned
// PVCs contain the PVC UUID; SVM names often contain the cluster/environment name.
// We match loosely — false positives are filtered by the scorer based on evidence weight.
func volumeRelevantToNamespace(volName, svmName, namespace string) bool {
	ns := strings.ToLower(namespace)
	vn := strings.ToLower(volName)
	sn := strings.ToLower(svmName)
	// Direct namespace substring match in volume name (common in some provisioners).
	if strings.Contains(vn, ns) {
		return true
	}
	// Trident PVC volumes: name contains "trident_pvc_" — always potentially relevant.
	if strings.HasPrefix(vn, "trident_pvc_") || strings.HasPrefix(vn, "pvc_") {
		return true
	}
	// SVM name match.
	if strings.Contains(sn, ns) {
		return true
	}
	return false
}

func temporallyDamped(base float64, gapSecs int) float64 {
	if gapSecs <= 60 {
		return base
	}
	if gapSecs >= 300 {
		return base * 0.60
	}
	decay := float64(gapSecs-60) / float64(240)
	return base * (1.0 - decay*0.40)
}

func sourceUnavailableEvidence(clusterName string, err error) *domain.Evidence {
	payload, _ := json.Marshal(map[string]string{
		"cluster": clusterName,
		"error":   err.Error(),
	})
	now := time.Now().UTC()
	errStr := err.Error()
	return &domain.Evidence{
		EvidenceType:       domain.TypeSourceUnavailable,
		Source:             "netapp",
		Role:               domain.RoleContext,
		Weight:             0.0,
		Description:        fmt.Sprintf("NetApp cluster %s unavailable: %s", clusterName, errStr),
		Payload:            payload,
		GatheredAt:         now,
		FetchStatus:        domain.FetchError,
		FetchError:         &errStr,
		EvidenceConfidence: 0.0,
		CreatedAt:          now,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
