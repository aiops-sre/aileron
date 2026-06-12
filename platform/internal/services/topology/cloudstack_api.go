package topology

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// ============================================================================
// CLOUDSTACK API CLIENT FOR REAL TOPOLOGY DISCOVERY
// Queries CloudStack to get KVM hosts and VM mappings
// ============================================================================

type CloudStackClient struct {
	Endpoint   string
	APIKey     string
	SecretKey  string
	HTTPClient *http.Client
}

// CloudStackVM represents a VM from CloudStack API
type CloudStackVM struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DisplayName     string `json:"displayname"`
	State           string `json:"state"`
	HostID          string `json:"hostid"`
	HostName        string `json:"hostname"`
	ZoneName        string `json:"zonename"`
	TemplateID      string `json:"templateid"`
	TemplateName    string `json:"templatename"`
	ServiceOffering string `json:"serviceofferingname"`
	CPUNumber       int    `json:"cpunumber"`
	Memory          int    `json:"memory"`
	NICs            []struct {
		IPAddress  string `json:"ipaddress"`
		MACAddress string `json:"macaddress"`
	} `json:"nic"`
}

// CloudStackHost represents a KVM host from CloudStack API
type CloudStackHost struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	IPAddress       string `json:"ipaddress"`
	ClusterName     string `json:"clustername"`
	ZoneName        string `json:"zonename"`
	Hypervisor      string `json:"hypervisor"`
	State           string `json:"state"`
	CPUNumber       int    `json:"cpunumber"`
	CPUSpeed        int    `json:"cpuspeed"`
	MemoryTotal     int64  `json:"memorytotal"`
	MemoryAllocated int64  `json:"memoryallocated"`
}

// NewCloudStackClient creates a CloudStack API client
func NewCloudStackClient(endpoint, apiKey, secretKey string) *CloudStackClient {
	return &CloudStackClient{
		Endpoint:  endpoint,
		APIKey:    apiKey,
		SecretKey: secretKey,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: os.Getenv("INTERNAL_TLS_INSECURE") == "true", //nolint:gosec
				},
			},
		},
	}
}

// ListVirtualMachines gets ALL VMs from CloudStack (all states), handling pagination.
func (c *CloudStackClient) ListVirtualMachines(ctx context.Context) ([]CloudStackVM, error) {
	var all []CloudStackVM
	page := 1
	pageSize := 500

	for {
		params := map[string]string{
			"command":  "listVirtualMachines",
			"response": "json",
			"listall":  "true",
			"page":     fmt.Sprintf("%d", page),
			"pagesize": fmt.Sprintf("%d", pageSize),
		}

		response, err := c.executeRequest(ctx, params)
		if err != nil {
			if len(all) > 0 {
				log.Printf("CloudStack ListVMs pagination error on page %d: %v (returning %d so far)", page, err, len(all))
				return all, nil
			}
			return nil, err
		}

		var result struct {
			ListVirtualMachinesResponse struct {
				Count          int            `json:"count"`
				VirtualMachine []CloudStackVM `json:"virtualmachine"`
			} `json:"listvirtualmachinesresponse"`
		}
		if err := json.Unmarshal(response, &result); err != nil {
			return nil, err
		}

		batch := result.ListVirtualMachinesResponse.VirtualMachine
		all = append(all, batch...)

		// Stop when we've fetched all VMs.
		if len(all) >= result.ListVirtualMachinesResponse.Count || len(batch) < pageSize {
			break
		}
		page++
	}

	log.Printf("CloudStack ListVMs: fetched %d VMs total", len(all))
	return all, nil
}

// ListHosts gets all KVM hosts from CloudStack with pagination.
func (c *CloudStackClient) ListHosts(ctx context.Context) ([]CloudStackHost, error) {
	var all []CloudStackHost
	page := 1
	pageSize := 500

	for {
		params := map[string]string{
			"command":  "listHosts",
			"response": "json",
			"type":     "Routing", // KVM hosts only
			"page":     fmt.Sprintf("%d", page),
			"pagesize": fmt.Sprintf("%d", pageSize),
		}

		response, err := c.executeRequest(ctx, params)
		if err != nil {
			if len(all) > 0 {
				return all, nil
			}
			return nil, err
		}

		var result struct {
			ListHostsResponse struct {
				Count int              `json:"count"`
				Host  []CloudStackHost `json:"host"`
			} `json:"listhostsresponse"`
		}
		if err := json.Unmarshal(response, &result); err != nil {
			return nil, err
		}

		batch := result.ListHostsResponse.Host
		all = append(all, batch...)

		if len(all) >= result.ListHostsResponse.Count || len(batch) < pageSize {
			break
		}
		page++
	}
	return all, nil
}

// executeRequest executes CloudStack API request with HMAC-SHA1 signature
func (c *CloudStackClient) executeRequest(ctx context.Context, params map[string]string) ([]byte, error) {
	// Add API key
	params["apiKey"] = c.APIKey

	// Sort parameters by key (required for signature)
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build query string with original case parameters
	queryParts := make([]string, 0, len(keys))
	for _, k := range keys {
		queryParts = append(queryParts, fmt.Sprintf("%s=%s",
			k, // Keep original case
			url.QueryEscape(params[k])))
	}
	queryString := strings.Join(queryParts, "&")

	// CRITICAL FIX: Lowercase the ENTIRE query string before signing
	// CloudStack requires lowercasing both parameter names AND values
	signatureString := strings.ToLower(queryString)

	// Generate HMAC-SHA1 signature using lowercased string
	mac := hmac.New(sha1.New, []byte(c.SecretKey))
	mac.Write([]byte(signatureString))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	// Build final URL with original case query string and signature
	finalURL := fmt.Sprintf("%s?%s&signature=%s",
		c.Endpoint,
		queryString, // Use original case query string in URL
		url.QueryEscape(signature))

	log.Printf("CloudStack API call: %s (command=%s)", c.Endpoint, params["command"])

	req, err := http.NewRequestWithContext(ctx, "GET", finalURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		log.Printf("CloudStack API error: %s", string(body))
		return nil, fmt.Errorf("CloudStack API returned %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("CloudStack API success")
	return body, nil
}

// ============================================================================
// UPDATE CLOUDSTACK DISCOVERER TO USE REAL API
// ============================================================================

// RealCloudStackDiscoverer discovers VMs and KVMs from CloudStack API
type RealCloudStackDiscoverer struct {
	client *CloudStackClient
}

func NewRealCloudStackDiscoverer(endpoint, apiKey, secretKey string) *RealCloudStackDiscoverer {
	return &RealCloudStackDiscoverer{
		client: NewCloudStackClient(endpoint, apiKey, secretKey),
	}
}

func (d *RealCloudStackDiscoverer) GetName() string {
	return "Real CloudStack API Discoverer"
}

func (d *RealCloudStackDiscoverer) GetLayer() InfrastructureLayer {
	return LayerCloudStack
}

func (d *RealCloudStackDiscoverer) Discover(ctx context.Context, region string) ([]InfraNode, error) {
	nodes := make([]InfraNode, 0)

	// Get KVM hosts first
	hosts, err := d.client.ListHosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("CloudStack ListHosts failed: %w", err)
	}

	log.Printf("Discovered %d KVM hosts from CloudStack API", len(hosts))

	// Create nodes for KVM hosts
	for _, host := range hosts {
		node := InfraNode{
			ID:           host.ID,
			Name:         host.Name,
			Type:         "kvm_host",
			Layer:        "bare_metal", // KVMs are physical hosts
			Region:       region,
			Status:       host.State,
			HealthStatus: getHealthFromState(host.State),
			Properties: map[string]interface{}{
				"ip_address":      host.IPAddress,
				"cluster":         host.ClusterName,
				"zone":            host.ZoneName,
				"hypervisor":      host.Hypervisor,
				"cpu_cores":       host.CPUNumber,
				"cpu_speed_mhz":   host.CPUSpeed,
				"memory_total_mb": host.MemoryTotal / 1024 / 1024,
				"memory_used_mb":  host.MemoryAllocated / 1024 / 1024,
			},
			Labels: map[string]string{
				"type":    "kvm",
				"cluster": host.ClusterName,
				"zone":    host.ZoneName,
			},
			Dependencies:  []string{},
			Dependents:    []string{}, // Will be populated with VMs
			LastDiscovery: time.Now(),
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		nodes = append(nodes, node)
	}

	// Get VMs and map to KVM hosts
	vms, err := d.client.ListVirtualMachines(ctx)
	if err != nil {
		log.Printf("CloudStack ListVMs failed: %v", err)
		return nodes, nil // Return KVMs at least
	}

	log.Printf("Discovered %d VMs from CloudStack API", len(vms))

	// Create nodes for VMs and link to KVMs
	for _, vm := range vms {
		vmNode := InfraNode{
			ID:           vm.ID,
			Name:         vm.Name,
			Type:         "cloudstack_vm",
			Layer:        LayerCloudStack,
			Region:       region,
			ParentID:     vm.HostID, // Links VM to KVM!
			Status:       vm.State,
			HealthStatus: getHealthFromState(vm.State),
			Properties: map[string]interface{}{
				"display_name":     vm.DisplayName,
				"kvm_host":         vm.HostName,
				"kvm_host_id":      vm.HostID,
				"zone":             vm.ZoneName,
				"template":         vm.TemplateName,
				"service_offering": vm.ServiceOffering,
				"vcpu":             vm.CPUNumber,
				"memory_mb":        vm.Memory,
			},
			Labels: map[string]string{
				"kvm_host": vm.HostName,
				"zone":     vm.ZoneName,
				"template": vm.TemplateName,
			},
			Dependencies:  []string{vm.HostID}, // VM depends on KVM
			Dependents:    []string{},
			LastDiscovery: time.Now(),
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		// Add IP addresses if available
		if len(vm.NICs) > 0 {
			vmNode.Properties["ip_address"] = vm.NICs[0].IPAddress
			vmNode.Properties["mac_address"] = vm.NICs[0].MACAddress
		}

		nodes = append(nodes, vmNode)

		// Update KVM's dependents list
		for i := range nodes {
			if nodes[i].ID == vm.HostID {
				nodes[i].Dependents = append(nodes[i].Dependents, vm.ID)
			}
		}
	}

	log.Printf("CloudStack topology: %d KVMs hosting %d VMs", len(hosts), len(vms))

	return nodes, nil
}

func getHealthFromState(state string) string {
	switch strings.ToLower(state) {
	case "running", "up":
		return "healthy"
	case "stopped", "down":
		return "unhealthy"
	default:
		return "degraded"
	}
}
