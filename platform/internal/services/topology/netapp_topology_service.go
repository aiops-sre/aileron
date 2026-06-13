package topology

// netapp_topology_service.go — NetApp ONTAP REST API Neo4j topology integration
//
// Builds the storage layer of the infrastructure graph:
//
//   netapp_cluster
//     HAS_NODE> netapp_node      (AFF-A400 storage controller, HA pair)
//         OWNS_AGGREGATE> netapp_aggregate (aggr1_node001 … space usage)
//             HOSTS_VOLUME> netapp_volume  (NFS/iSCSI volumes, Trident PVCs)
//                 BACKS_PVC> k8s_pvc      (K8s PersistentVolumeClaim, UID-keyed)
//                     USED_BY_POD> k8s_pod (already in graph from K8s topology)
//
// Configuration (env vars):
//   NETAPP_CLUSTERS  JSON array: [{"cluster":"<ip>","name":"<cluster-name>","region":"<dc>"},…]
//   NETAPP_USER      ONTAP API username  (harvest-user)
//   NETAPP_PASSWORD  ONTAP API password  (from Secret)
//
// Both MDN and RNO share the same credentials; each cluster entry only has IP+name+region.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// configuration 

// NetAppClusterConfig identifies one ONTAP cluster to scrape.
type NetAppClusterConfig struct {
	ClusterIP   string `json:"cluster"`    // e.g. "100.67.87.8"
	ClusterName string `json:"name"`       // e.g. "netapp-mdn-cluster001"
	Region      string `json:"region"`     // e.g. "mdn" or "rno"
	Datacenter  string `json:"datacenter"` // optional override
}

// LoadNetAppClustersFromEnv reads NETAPP_CLUSTERS (JSON array), NETAPP_USER, NETAPP_PASSWORD.
func LoadNetAppClustersFromEnv() (clusters []NetAppClusterConfig, user, password string) {
	user = os.Getenv("NETAPP_USER")
	password = os.Getenv("NETAPP_PASSWORD")
	raw := os.Getenv("NETAPP_CLUSTERS")
	if raw == "" || user == "" || password == "" {
		return nil, user, password
	}
	if err := json.Unmarshal([]byte(raw), &clusters); err != nil {
		log.Printf("netapp: NETAPP_CLUSTERS parse error: %v", err)
		return nil, user, password
	}
	return clusters, user, password
}

// ONTAP REST client 

type ontapClient struct {
	baseURL    string
	user       string
	password   string
	httpClient *http.Client
}

func newOntapClient(clusterIP, user, password string) *ontapClient {
	return &ontapClient{
		baseURL:  "https://" + clusterIP,
		user:     user,
		password: password,
		httpClient: &http.Client{
			Timeout: 45 * time.Second,
			Transport: &http.Transport{
				// internal CA not in container trust stores.
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
				MaxIdleConnsPerHost: 4,
			},
		},
	}
}

func (c *ontapClient) get(ctx context.Context, path string) ([]byte, error) {
	url := path
	if !strings.HasPrefix(path, "https://") {
		url = c.baseURL + path
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.password)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ONTAP %s: HTTP %d: %s", path, resp.StatusCode, string(body[:min(200, len(body))]))
	}
	return body, nil
}

// getAll paginates through a collection endpoint, following _links.next.href.
func (c *ontapClient) getAll(ctx context.Context, path, fields string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	next := fmt.Sprintf("%s?fields=%s&max_records=500&return_timeout=30", path, fields)
	for next != "" {
		body, err := c.get(ctx, next)
		if err != nil {
			return all, err
		}
		var page struct {
			Records []json.RawMessage `json:"records"`
			Links   struct {
				Next struct {
					Href string `json:"href"`
				} `json:"next"`
			} `json:"_links"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return all, fmt.Errorf("JSON unmarshal %s: %w", path, err)
		}
		all = append(all, page.Records...)
		next = ""
		if page.Links.Next.Href != "" {
			next = c.baseURL + page.Links.Next.Href
		}
	}
	return all, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// topology refresh 

// SetNetAppClusters configures NetApp cluster endpoints on the fetcher.
func (f *LiveTopologyFetcher) SetNetAppClusters(clusters []NetAppClusterConfig, user, password string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.netappClusters = clusters
	f.netappUser = user
	f.netappPassword = password
}

// refreshNetAppTopology fetches topology from all configured ONTAP clusters and
// writes it into Neo4j, linking storage volumes to K8s PVCs where possible.
// Clusters are refreshed in parallel so a large cluster doesn't starve others.
func (f *LiveTopologyFetcher) refreshNetAppTopology(ctx context.Context) error {
	f.mu.RLock()
	clusters := f.netappClusters
	user := f.netappUser
	password := f.netappPassword
	f.mu.RUnlock()

	if len(clusters) == 0 {
		return nil // not configured — skip silently
	}

	var wg sync.WaitGroup
	for _, cfg := range clusters {
		wg.Add(1)
		go func(c NetAppClusterConfig) {
			defer wg.Done()
			// Give each cluster 90% of the parent deadline so one slow cluster
			// cannot starve others, but we always leave the parent in control.
			// Minimum floor of 8 minutes handles clusters with ~2500 volumes.
			clusterTimeout := 8 * time.Minute
			if dl, ok := ctx.Deadline(); ok {
				if remaining := time.Until(dl) * 9 / 10; remaining > clusterTimeout {
					clusterTimeout = remaining
				}
			}
			clusterCtx, cancel := context.WithTimeout(ctx, clusterTimeout)
			defer cancel()
			if err := f.refreshOneNetAppCluster(clusterCtx, c, user, password); err != nil {
				log.Printf("netapp: cluster %s (%s) refresh failed: %v", c.ClusterName, c.ClusterIP, err)
			}
		}(cfg)
	}
	wg.Wait()
	return nil
}

func (f *LiveTopologyFetcher) refreshOneNetAppCluster(
	ctx context.Context,
	cfg NetAppClusterConfig,
	user, password string,
) error {
	client := newOntapClient(cfg.ClusterIP, user, password)
	dc := cfg.Datacenter
	if dc == "" {
		dc = cfg.Region
	}
	clusterEntityID := "netapp-cluster-" + cfg.ClusterName

	start := time.Now()

	// 1. Cluster node 
	{
		body, err := client.get(ctx, "/api/cluster?fields=name,version,location")
		if err != nil {
			return fmt.Errorf("cluster info: %w", err)
		}
		var info struct {
			Name     string `json:"name"`
			Location string `json:"location"`
			Version  struct {
				Full string `json:"full"`
			} `json:"version"`
		}
		if err := json.Unmarshal(body, &info); err != nil {
			return fmt.Errorf("cluster info unmarshal: %w", err)
		}
		if err := f.upsertNodeWithProps(ctx, topoNode{
			EntityID:    clusterEntityID,
			Name:        cfg.ClusterName,
			Type:        "netapp_cluster",
			Environment: "production",
			Region:      cfg.Region,
		}, map[string]interface{}{
			"ontap_version": info.Version.Full,
			"location":      info.Location,
			"datacenter":    dc,
			"cluster_ip":    cfg.ClusterIP,
		}); err != nil {
			return fmt.Errorf("upsert cluster node: %w", err)
		}
	}

	// 2. Storage controller nodes 
	// node entity_id entity_id map for aggregate wiring
	nodeEntityByName := make(map[string]string)
	{
		records, err := client.getAll(ctx, "/api/cluster/nodes",
			"name,state,model,serial_number,location,uptime")
		if err != nil {
			log.Printf("netapp %s: nodes fetch: %v", cfg.ClusterName, err)
		}
		for _, raw := range records {
			var n struct {
				Name         string `json:"name"`
				State        string `json:"state"`
				Model        string `json:"model"`
				SerialNumber string `json:"serial_number"`
				Location     string `json:"location"`
				Uptime       int64  `json:"uptime"` // seconds
			}
			if err := json.Unmarshal(raw, &n); err != nil {
				continue
			}
			nodeEntityID := fmt.Sprintf("netapp-node-%s-%s", cfg.ClusterName, n.Name)
			nodeEntityByName[n.Name] = nodeEntityID

			if err := f.upsertNodeWithProps(ctx, topoNode{
				EntityID:    nodeEntityID,
				Name:        n.Name,
				Type:        "netapp_node",
				Environment: "production",
				Region:      cfg.Region,
			}, map[string]interface{}{
				"model":         n.Model,
				"serial_number": n.SerialNumber,
				"state":         n.State,
				"location":      n.Location,
				"cluster":       cfg.ClusterName,
				"datacenter":    dc,
			}); err != nil {
				log.Printf("netapp upsert node %s: %v", n.Name, err)
				continue
			}
			// cluster HAS_NODE> node
			_ = f.upsertRelationship(ctx, clusterEntityID, nodeEntityID, "HAS_NODE", 1.0)
		}
		log.Printf("   netapp %s: upserted %d storage nodes", cfg.ClusterName, len(records))
	}

	// 3. Aggregates 
	// aggr entity_id entity_id map for volume wiring
	aggrEntityByName := make(map[string]string)
	{
		records, err := client.getAll(ctx, "/api/storage/aggregates",
			"name,state,node.name,space.block_storage")
		if err != nil {
			log.Printf("netapp %s: aggregates fetch: %v", cfg.ClusterName, err)
		}
		for _, raw := range records {
			var a struct {
				Name  string `json:"name"`
				State string `json:"state"`
				Node  struct {
					Name string `json:"name"`
				} `json:"node"`
				Space struct {
					BlockStorage struct {
						Size      int64 `json:"size"`
						Used      int64 `json:"used"`
						Available int64 `json:"available"`
					} `json:"block_storage"`
				} `json:"space"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				continue
			}
			aggrEntityID := fmt.Sprintf("netapp-aggr-%s-%s", cfg.ClusterName, a.Name)
			aggrEntityByName[a.Name] = aggrEntityID

			totalGB := float64(a.Space.BlockStorage.Size) / (1 << 30)
			usedGB := float64(a.Space.BlockStorage.Used) / (1 << 30)
			pct := 0.0
			if totalGB > 0 {
				pct = usedGB / totalGB * 100
			}

			if err := f.upsertNodeWithProps(ctx, topoNode{
				EntityID:    aggrEntityID,
				Name:        a.Name,
				Type:        "netapp_aggregate",
				Environment: "production",
				Region:      cfg.Region,
			}, map[string]interface{}{
				"state":           a.State,
				"node":            a.Node.Name,
				"cluster":         cfg.ClusterName,
				"datacenter":      dc,
				"space_total_gb":  int64(totalGB),
				"space_used_gb":   int64(usedGB),
				"space_percent":   int64(pct),
			}); err != nil {
				log.Printf("netapp upsert aggr %s: %v", a.Name, err)
				continue
			}

			// node OWNS_AGGREGATE> aggregate
			if nodeEntityID, ok := nodeEntityByName[a.Node.Name]; ok {
				_ = f.upsertRelationship(ctx, nodeEntityID, aggrEntityID, "OWNS_AGGREGATE", 1.0)
			}
			// cluster HAS_AGGREGATE> aggregate (direct shortcut for queries)
			_ = f.upsertRelationship(ctx, clusterEntityID, aggrEntityID, "HAS_AGGREGATE", 0.9)
		}
		log.Printf("   netapp %s: upserted %d aggregates", cfg.ClusterName, len(records))
	}

	// 4. SVMs 
	svmEntityByName := make(map[string]string)
	{
		records, err := client.getAll(ctx, "/api/svm/svms", "name,state,subtype")
		if err != nil {
			log.Printf("netapp %s: SVM fetch: %v", cfg.ClusterName, err)
		}
		for _, raw := range records {
			var s struct {
				Name    string `json:"name"`
				State   string `json:"state"`
				Subtype string `json:"subtype"`
			}
			if err := json.Unmarshal(raw, &s); err != nil {
				continue
			}
			svmEntityID := fmt.Sprintf("netapp-svm-%s-%s", cfg.ClusterName, s.Name)
			svmEntityByName[s.Name] = svmEntityID

			if err := f.upsertNodeWithProps(ctx, topoNode{
				EntityID:    svmEntityID,
				Name:        s.Name,
				Type:        "netapp_svm",
				Environment: "production",
				Region:      cfg.Region,
			}, map[string]interface{}{
				"state":      s.State,
				"svm_type":   s.Subtype,
				"cluster":    cfg.ClusterName,
				"datacenter": dc,
			}); err != nil {
				log.Printf("netapp upsert svm %s: %v", s.Name, err)
				continue
			}
			// cluster HAS_SVM> svm
			_ = f.upsertRelationship(ctx, clusterEntityID, svmEntityID, "HAS_SVM", 0.9)
		}
		log.Printf("   netapp %s: upserted %d SVMs", cfg.ClusterName, len(records))
	}

	// 5. Volumes 
	volCount := 0
	pvcLinks := 0
	bucketCount := 0
	{
		records, err := client.getAll(ctx, "/api/storage/volumes",
			"name,state,type,svm.name,aggregates.name,space.size,space.used")
		if err != nil {
			log.Printf("netapp %s: volumes fetch: %v", cfg.ClusterName, err)
		}
		for _, raw := range records {
			var v struct {
				Name  string `json:"name"`
				State string `json:"state"`
				Type  string `json:"type"`
				SVM   struct {
					Name string `json:"name"`
				} `json:"svm"`
				Aggregates []struct {
					Name string `json:"name"`
				} `json:"aggregates"`
				Space struct {
					Size int64 `json:"size"`
					Used int64 `json:"used"`
				} `json:"space"`
			}
			if err := json.Unmarshal(raw, &v); err != nil {
				continue
			}

			volEntityID := fmt.Sprintf("netapp-vol-%s-%s", cfg.ClusterName, v.Name)
			totalGB := float64(v.Space.Size) / (1 << 30)
			usedGB := float64(v.Space.Used) / (1 << 30)

			extra := map[string]interface{}{
				"state":          v.State,
				"volume_type":    v.Type,
				"svm":            v.SVM.Name,
				"cluster":        cfg.ClusterName,
				"datacenter":     dc,
				"space_total_gb": totalGB,
				"space_used_gb":  usedGB,
			}

			// Extract K8s PVC UID if this is a Trident-provisioned volume.
			// Trident names volumes: trident_pvc_<uuid_with_underscores>
			pvcUID := extractTridentPVCUID(v.Name)
			if pvcUID != "" {
				extra["pvc_uid"] = pvcUID
				extra["is_pvc"] = true
			}

			if err := f.upsertNodeWithProps(ctx, topoNode{
				EntityID:    volEntityID,
				Name:        v.Name,
				Type:        "netapp_volume",
				Environment: "production",
				Region:      cfg.Region,
			}, extra); err != nil {
				log.Printf("netapp upsert vol %s: %v", v.Name, err)
				continue
			}
			volCount++

			// aggregate HOSTS_VOLUME> volume
			for _, ag := range v.Aggregates {
				if aggrEntityID, ok := aggrEntityByName[ag.Name]; ok {
					_ = f.upsertRelationship(ctx, aggrEntityID, volEntityID, "HOSTS_VOLUME", 1.0)
				}
			}

			// svm HAS_VOLUME> volume
			if svmEntityID, ok := svmEntityByName[v.SVM.Name]; ok {
				_ = f.upsertRelationship(ctx, svmEntityID, volEntityID, "HAS_VOLUME", 0.9)
			}

			// volume BACKS_PVC> k8s_pvc  (if Trident PVC)
			if pvcUID != "" {
				pvcEntityID := "k8s-pvc-" + pvcUID
				_ = f.upsertRelationshipByTargetUID(ctx, volEntityID, pvcUID, "BACKS_PVC", 0.95)
				_ = pvcEntityID // silence unused warning — relationship written via UID lookup
				pvcLinks++
			}
		}
		log.Printf("   netapp %s: upserted %d volumes, %d PVC links",
			cfg.ClusterName, volCount, pvcLinks)
	}

	// 6. S3 Buckets 
	{
		records, err := client.getAll(ctx, "/api/protocols/s3/buckets",
			"name,svm.name,uuid,size,logical_used_size,volume.name,type,comment")
		if err != nil {
			log.Printf("netapp %s: S3 buckets fetch: %v", cfg.ClusterName, err)
		}
		for _, raw := range records {
			var b struct {
				Name           string `json:"name"`
				UUID           string `json:"uuid"`
				Type           string `json:"type"`
				Comment        string `json:"comment"`
				Size           int64  `json:"size"`
				LogicalUsed    int64  `json:"logical_used_size"`
				SVM            struct {
					Name string `json:"name"`
				} `json:"svm"`
				Volume struct {
					Name string `json:"name"`
				} `json:"volume"`
			}
			if err := json.Unmarshal(raw, &b); err != nil {
				continue
			}

			bucketEntityID := fmt.Sprintf("netapp-s3bucket-%s-%s", cfg.ClusterName, b.Name)
			sizeGB := float64(b.Size) / (1 << 30)
			usedGB := float64(b.LogicalUsed) / (1 << 30)
			pct := 0.0
			if sizeGB > 0 {
				pct = usedGB / sizeGB * 100
			}

			if err := f.upsertNodeWithProps(ctx, topoNode{
				EntityID:    bucketEntityID,
				Name:        b.Name,
				Type:        "netapp_s3_bucket",
				Environment: "production",
				Region:      cfg.Region,
			}, map[string]interface{}{
				"bucket_uuid":    b.UUID,
				"svm":            b.SVM.Name,
				"backing_volume": b.Volume.Name,
				"comment":        b.Comment,
				"cluster":        cfg.ClusterName,
				"datacenter":     dc,
				"space_total_gb": sizeGB,
				"space_used_gb":  usedGB,
				"space_percent":  int64(pct),
			}); err != nil {
				log.Printf("netapp upsert s3 bucket %s: %v", b.Name, err)
				continue
			}
			bucketCount++

			// svm HAS_BUCKET> bucket
			if svmEntityID, ok := svmEntityByName[b.SVM.Name]; ok {
				_ = f.upsertRelationship(ctx, svmEntityID, bucketEntityID, "HAS_BUCKET", 0.9)
			}

			// bucket BACKED_BY_VOLUME> volume
			if b.Volume.Name != "" {
				volEntityID := fmt.Sprintf("netapp-vol-%s-%s", cfg.ClusterName, b.Volume.Name)
				_ = f.upsertRelationship(ctx, bucketEntityID, volEntityID, "BACKED_BY_VOLUME", 0.9)
			}
		}
		log.Printf("   netapp %s: upserted %d S3 buckets", cfg.ClusterName, bucketCount)
	}

	log.Printf("netapp %s: topology refresh done in %s (nodes=%d aggrs=%d vols=%d pvc_links=%d buckets=%d)",
		cfg.ClusterName, time.Since(start).Round(time.Millisecond),
		len(nodeEntityByName), len(aggrEntityByName), volCount, pvcLinks, bucketCount)
	return nil
}

// helpers 

// extractTridentPVCUID converts a Trident volume name to a K8s PVC UID.
// "trident_pvc_96af5f7f_cc59_4bfb_a91e_8263244a6ad7" "96af5f7f-cc59-4bfb-a91e-8263244a6ad7"
func extractTridentPVCUID(volName string) string {
	const prefix = "trident_pvc_"
	if !strings.HasPrefix(volName, prefix) {
		return ""
	}
	raw := strings.TrimPrefix(volName, prefix)
	// UUID has form 8-4-4-4-12 characters
	uid := strings.ReplaceAll(raw, "_", "-")
	if len(uid) != 36 {
		return ""
	}
	return uid
}

// upsertNodeWithProps writes a node to Neo4j, also setting extra key/value pairs.
func (f *LiveTopologyFetcher) upsertNodeWithProps(ctx context.Context, node topoNode, extra map[string]interface{}) error {
	session := f.neo4jDriver.NewSession(ctx, neo4j.SessionConfig{})
	defer session.Close(ctx)

	params := map[string]interface{}{
		"entityId":    node.EntityID,
		"name":        node.Name,
		"type":        node.Type,
		"environment": node.Environment,
		"region":      node.Region,
		"infraLevel":  infraLevelForType(node.Type),
		"extra":       extra,
	}
	_, err := session.Run(ctx, `
		MERGE (s:Service {entity_id: $entityId})
		SET s.name        = $name,
		    s.type        = $type,
		    s.entity_type = $type,
		    s.label       = $name,
		    s.cluster     = $region,
		    s.environment = $environment,
		    s.region      = $region,
		    s.infra_level = $infraLevel,
		    s.last_seen   = datetime()
		SET s += $extra
	`, params)
	return err
}

// upsertRelationshipByTargetUID creates a relationship from fromEntityID to a node
// whose pvc_uid property equals targetUID (used to link NetApp volumes to PVCs
// that were created during the K8s topology phase without knowing the entity_id).
func (f *LiveTopologyFetcher) upsertRelationshipByTargetUID(
	ctx context.Context,
	fromEntityID, targetUID, relType string,
	strength float64,
) error {
	session := f.neo4jDriver.NewSession(ctx, neo4j.SessionConfig{})
	defer session.Close(ctx)

	query := fmt.Sprintf(`
		MATCH (from:Service {entity_id: $fromEntity})
		MATCH (to:Service   {pvc_uid: $targetUID})
		MERGE (from)-[r:%s]->(to)
		SET r.weight       = $strength,
		    r.strength     = $strength,
		    r.last_updated = datetime()
	`, relType)
	_, err := session.Run(ctx, query, map[string]interface{}{
		"fromEntity": fromEntityID,
		"targetUID":  targetUID,
		"strength":   strength,
	})
	return err
}
