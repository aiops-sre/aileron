package cloudstack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"

	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
)

// ClientConfig holds the CloudStack API credentials for one cluster.
type ClientConfig struct {
	Endpoint  string // e.g. "https://cs-rno.example.com/client/api"
	APIKey    string
	SecretKey string
	ClusterRef string
}

// Client makes signed CloudStack API calls.
type Client struct {
	endpoint   string
	apiKey     string
	secretKey  string
	httpClient *http.Client
}

func NewClient(cfg ClientConfig) *Client {
	return &Client{
		endpoint:  cfg.Endpoint,
		apiKey:    cfg.APIKey,
		secretKey: cfg.SecretKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				DisableKeepAlives: false,
			},
		},
	}
}

// call makes a signed CloudStack API request and decodes the JSON response.
func (c *Client) call(ctx context.Context, command string, params map[string]string) (map[string]json.RawMessage, error) {
	params["command"] = command
	params["apiKey"] = c.apiKey
	params["response"] = "json"

	// Build the sorted parameter string for HMAC-SHA1 signing.
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, strings.ToLower(k)+"="+strings.ToLower(url.QueryEscape(params[k])))
	}
	queryString := strings.Join(parts, "&")

	mac := hmac.New(sha1.New, []byte(c.secretKey))
	mac.Write([]byte(queryString))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	fullURL := c.endpoint + "?" + queryString + "&signature=" + url.QueryEscape(signature)

	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building cloudstack request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudstack api call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading cloudstack response: %w", err)
	}

	// CloudStack wraps all responses in a top-level key like "listvirtualmachinesresponse".
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("parsing cloudstack response: %w", err)
	}
	// Return the inner object (first value in the wrapper).
	for _, v := range wrapper {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(v, &inner); err == nil {
			return inner, nil
		}
	}
	return wrapper, nil
}

// GetVM fetches a virtual machine by name or ID.
func (c *Client) GetVM(ctx context.Context, nameOrID string) (*VMState, error) {
	resp, err := c.call(ctx, "listVirtualMachines", map[string]string{
		"name":    nameOrID,
		"listall": "true",
	})
	if err != nil {
		return nil, err
	}

	var vms []json.RawMessage
	if raw, ok := resp["virtualmachine"]; ok {
		json.Unmarshal(raw, &vms)
	}
	if len(vms) == 0 {
		return nil, fmt.Errorf("VM %s not found in CloudStack", nameOrID)
	}

	var vm VMState
	json.Unmarshal(vms[0], &vm)
	return &vm, nil
}

// GetHost fetches a hypervisor host by ID.
func (c *Client) GetHost(ctx context.Context, hostID string) (*HostState, error) {
	resp, err := c.call(ctx, "listHosts", map[string]string{"id": hostID})
	if err != nil {
		return nil, err
	}

	var hosts []json.RawMessage
	if raw, ok := resp["host"]; ok {
		json.Unmarshal(raw, &hosts)
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("host %s not found in CloudStack", hostID)
	}

	var host HostState
	json.Unmarshal(hosts[0], &host)
	return &host, nil
}

type VMState struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	State       string `json:"state"`
	HostID      string `json:"hostid"`
	HostName    string `json:"hostname"`
	Created     string `json:"created"`
}

type HostState struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	State         string `json:"state"`
	ResourceState string `json:"resourcestate"`
	ClusterName   string `json:"clustername"`
}

// ── VM State Fetcher ──────────────────────────────────────────────────────────

// VMStateFetcher fetches CloudStack VM state and resolves the host ID
// for the downstream HostStateFetcher.
type VMStateFetcher struct {
	clients map[string]*Client // clusterRef → client
}

func NewVMStateFetcher(configs []ClientConfig) *VMStateFetcher {
	clients := make(map[string]*Client, len(configs))
	for _, cfg := range configs {
		clients[cfg.ClusterRef] = NewClient(cfg)
	}
	return &VMStateFetcher{clients: clients}
}

func (f *VMStateFetcher) ID() fetchers.FetcherID          { return "cloudstack_vm_state" }
func (f *VMStateFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *VMStateFetcher) SourceName() string              { return "cloudstack" }

func (f *VMStateFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	profile := req.EntityProfile
	if profile == nil || profile.ClusterRef == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	client, ok := f.clients[profile.ClusterRef]
	if !ok {
		// Try all clients (cross-cluster lookup).
		for _, c := range f.clients {
			if vm, err := c.GetVM(ctx, profile.ResourceName); err == nil {
				client = c
				profile.CloudStackVMID = vm.ID
				profile.CloudStackVMName = vm.Name
				break
			}
		}
		if client == nil {
			return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
		}
	}

	vmName := profile.CloudStackVMName
	if vmName == "" {
		vmName = profile.K8sNodeName
	}
	if vmName == "" {
		vmName = profile.ResourceName
	}

	vm, err := client.GetVM(ctx, vmName)
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}

	gapSecs := int(time.Since(req.IncidentStartAt).Seconds())

	payload, _ := json.Marshal(domain.CloudStackVMStatePayload{
		VMID:            vm.ID,
		VMName:          vm.Name,
		State:           vm.State,
		HostID:          vm.HostID,
		HostName:        vm.HostName,
		LastStateChange: time.Now().UTC(), // CloudStack doesn't return state-change time directly
		IsFromAuditLog:  false,
	})

	e := &domain.Evidence{
		EvidenceType:       domain.TypeCloudStackVMState,
		Source:             "cloudstack",
		TemporalMode:       domain.TemporalCurrent,
		TemporalGapSecs:    &gapSecs,
		Description:        fmt.Sprintf("CloudStack VM %s state: %s (host: %s)", vm.Name, vm.State, vm.HostName),
		Payload:            payload,
		GatheredAt:         time.Now().UTC(),
		EvidenceConfidence: temporallyDampedConfidence(0.95, gapSecs),
		FetchStatus:        domain.FetchSuccess,
		CreatedAt:          time.Now().UTC(),
	}

	switch vm.State {
	case "Running":
		// VM running — contradicts VM failure. But dampen if gap > 120s (may have auto-recovered).
		if gapSecs < 120 {
			e.Role = domain.RoleContradicts
			e.Weight = 0.88
		} else {
			e.Role = domain.RoleContext
			e.Description += " — NOTE: VM may have auto-recovered since incident"
		}
	case "Stopped", "Error":
		e.Role = domain.RoleSupports
		e.Weight = 0.90
	case "Migrating":
		e.Role = domain.RoleContext
	default:
		e.Role = domain.RoleContext
	}

	return &fetchers.FetchResult{
		Evidence: []*domain.Evidence{e},
		OutputData: map[string]any{
			"host_id":   vm.HostID,
			"vm_state":  vm.State,
			"vm_name":   vm.Name,
		},
		Status: domain.FetchSuccess,
	}, nil
}

// ── Host State Fetcher ────────────────────────────────────────────────────────

// HostStateFetcher fetches the CloudStack hypervisor host state.
// Depends on VMStateFetcher to provide the host_id.
type HostStateFetcher struct {
	clients map[string]*Client
}

func NewHostStateFetcher(configs []ClientConfig) *HostStateFetcher {
	clients := make(map[string]*Client, len(configs))
	for _, cfg := range configs {
		clients[cfg.ClusterRef] = NewClient(cfg)
	}
	return &HostStateFetcher{clients: clients}
}

func (f *HostStateFetcher) ID() fetchers.FetcherID { return "cloudstack_host_state" }
func (f *HostStateFetcher) DependsOn() []fetchers.FetcherID {
	// Must run after VM fetcher to get the host_id.
	return []fetchers.FetcherID{"cloudstack_vm_state"}
}
func (f *HostStateFetcher) SourceName() string { return "cloudstack" }

func (f *HostStateFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	// Get host_id from the upstream VM fetcher.
	hostID := req.UpstreamString("cloudstack_vm_state", "host_id")
	if hostID == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	profile := req.EntityProfile
	var client *Client
	if profile != nil {
		if c, ok := f.clients[profile.ClusterRef]; ok {
			client = c
		}
	}
	if client == nil {
		// Try any available client.
		for _, c := range f.clients {
			client = c
			break
		}
	}
	if client == nil {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	host, err := client.GetHost(ctx, hostID)
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}

	gapSecs := int(time.Since(req.IncidentStartAt).Seconds())

	payload, _ := json.Marshal(domain.CloudStackHostStatePayload{
		HostID:        host.ID,
		HostName:      host.Name,
		State:         host.State,
		ResourceState: host.ResourceState,
		ClusterName:   host.ClusterName,
	})

	e := &domain.Evidence{
		EvidenceType:       domain.TypeCloudStackHostState,
		Source:             "cloudstack",
		TemporalMode:       domain.TemporalCurrent,
		TemporalGapSecs:    &gapSecs,
		Description:        fmt.Sprintf("CloudStack host %s state: %s (resource: %s)", host.Name, host.State, host.ResourceState),
		Payload:            payload,
		GatheredAt:         time.Now().UTC(),
		EvidenceConfidence: temporallyDampedConfidence(0.93, gapSecs),
		FetchStatus:        domain.FetchSuccess,
		CreatedAt:          time.Now().UTC(),
	}

	switch host.State {
	case "Up":
		// Host is up — if gap < 120s, contradicts BM failure hypothesis
		if gapSecs < 120 {
			e.Role = domain.RoleContradicts
			e.Weight = 0.88
		} else {
			e.Role = domain.RoleContext
		}
	case "Down", "Alert", "Error":
		e.Role = domain.RoleSupports
		e.Weight = 0.92
	default:
		e.Role = domain.RoleContext
	}

	return &fetchers.FetchResult{
		Evidence: []*domain.Evidence{e},
		Status:   domain.FetchSuccess,
	}, nil
}

func temporallyDampedConfidence(base float64, gapSeconds int) float64 {
	if gapSeconds <= 60 {
		return base
	}
	if gapSeconds >= 180 {
		return base * 0.50
	}
	decay := float64(gapSeconds-60) / float64(120)
	return base * (1.0 - decay*0.50)
}
