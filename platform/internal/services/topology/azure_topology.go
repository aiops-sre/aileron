package topology

// ============================================================================
// AZURE TOPOLOGY PROVIDER
// Discovers: VMs, VMSS, AKS clusters, SQL Databases, Function Apps,
//            Storage Accounts, VNets/Subnets, App Gateways, Load Balancers
//
// Required env vars:
//   AZURE_CLIENT_ID        – service principal app ID
//   AZURE_CLIENT_SECRET    – service principal secret
//   AZURE_TENANT_ID        – AAD tenant ID
//   AZURE_SUBSCRIPTION_IDS – comma-separated subscription IDs (required)
//   AZURE_RESOURCE_GROUPS  – comma-separated RG filter (optional; empty = all)
//   AILERON_AZURE_ENABLED  – must be "true" to activate the provider
// ============================================================================

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Resource type constants
// ---------------------------------------------------------------------------

const (
	AzureResourceVM          = "azure_vm"
	AzureResourceVMSS        = "azure_vmss"
	AzureResourceAKSNode     = "aks_node"
	AzureResourceSQLDatabase = "azure_sql_database"
	AzureResourceFunctionApp = "azure_function_app"
	AzureResourceStorage     = "azure_storage_account"
	AzureResourceAppGateway  = "azure_app_gateway"
	AzureResourceCosmosDB    = "azure_cosmosdb"
)

// ---------------------------------------------------------------------------
// Internal ARM REST helpers
// ---------------------------------------------------------------------------

const (
	azureARMBaseURL   = "https://management.azure.com"
	azureTokenURL     = "https://login.microsoftonline.com/%s/oauth2/v2.0/token"
	azureARMScope     = "https://management.azure.com/.default"
	azureAPIVersionVM = "2023-09-01"
	azureAPIVersionAKS = "2023-10-01"
	azureAPIVersionSQL = "2023-08-01-preview"
	azureAPIVersionFn  = "2023-12-01"
	azureAPIVersionSt  = "2023-05-01"
	azureAPIVersionNet = "2023-11-01"
	azureAPIVersionAgw = "2023-11-01"
	azureAPIVersionCos = "2024-02-15-preview"
)

// azureToken is the cached OAuth2 bearer token for ARM API calls.
type azureToken struct {
	mu        sync.Mutex
	value     string
	expiresAt time.Time
}

// azureARMPage is the minimal envelope for ARM list responses.
type azureARMPage struct {
	Value    []json.RawMessage `json:"value"`
	NextLink string            `json:"nextLink"`
}

// azureResourceBase holds the fields common to every ARM resource.
type azureResourceBase struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags"`
}

// azureProvisioningState is a minimal helper embedded in ARM properties.
type azureProvisioningState struct {
	ProvisioningState string `json:"provisioningState"`
}

// ---------------------------------------------------------------------------
// AzureTopologyProvider
// ---------------------------------------------------------------------------

// AzureTopologyProvider discovers Azure infrastructure resources across one or
// more subscriptions and returns them as InfraNode objects compatible with the
// existing multi-layer topology system.
type AzureTopologyProvider struct {
	clientID       string
	clientSecret   string
	tenantID       string
	subscriptionIDs []string
	resourceGroups []string // empty = all RGs in each subscription
	token          azureToken
	httpClient     *http.Client
}

// NewAzureTopologyProvider reads credentials from environment variables and
// returns a configured provider.  Returns (nil, nil) when
// AILERON_AZURE_ENABLED != "true" so callers can skip registration cleanly.
func NewAzureTopologyProvider() (*AzureTopologyProvider, error) {
	if strings.ToLower(os.Getenv("AILERON_AZURE_ENABLED")) != "true" {
		log.Println("azure topology: disabled (AILERON_AZURE_ENABLED != true)")
		return nil, nil
	}

	clientID := os.Getenv("AZURE_CLIENT_ID")
	clientSecret := os.Getenv("AZURE_CLIENT_SECRET")
	tenantID := os.Getenv("AZURE_TENANT_ID")
	subIDsRaw := os.Getenv("AZURE_SUBSCRIPTION_IDS")

	if clientID == "" || clientSecret == "" || tenantID == "" || subIDsRaw == "" {
		return nil, fmt.Errorf("azure topology: AZURE_CLIENT_ID, AZURE_CLIENT_SECRET, AZURE_TENANT_ID, and AZURE_SUBSCRIPTION_IDS must all be set")
	}

	subIDs := splitTrim(subIDsRaw, ",")
	if len(subIDs) == 0 {
		return nil, fmt.Errorf("azure topology: AZURE_SUBSCRIPTION_IDS is empty after parsing")
	}

	rgRaw := os.Getenv("AZURE_RESOURCE_GROUPS")
	var rgs []string
	if rgRaw != "" {
		rgs = splitTrim(rgRaw, ",")
	}

	p := &AzureTopologyProvider{
		clientID:        clientID,
		clientSecret:    clientSecret,
		tenantID:        tenantID,
		subscriptionIDs: subIDs,
		resourceGroups:  rgs,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	log.Printf("azure topology: provider initialised — subscriptions=%d rg_filter=%d",
		len(subIDs), len(rgs))
	return p, nil
}

// ---------------------------------------------------------------------------
// Discoverer interface implementation
// ---------------------------------------------------------------------------

// GetName returns the human-readable name used in log output.
func (p *AzureTopologyProvider) GetName() string { return "AzureTopologyProvider" }

// GetLayer places Azure resources at the VM layer (infrastructure tier).
// Individual nodes are typed with AzureResource* constants for finer-grained
// processing by consumers.
func (p *AzureTopologyProvider) GetLayer() InfrastructureLayer { return LayerVM }

// Discover performs parallel discovery across all configured subscriptions
// and resource groups, returning flat InfraNode slices compatible with the
// InfraDiscoveryManager.
func (p *AzureTopologyProvider) Discover(ctx context.Context, region string) ([]InfraNode, error) {
	if err := p.refreshToken(ctx); err != nil {
		return nil, fmt.Errorf("azure: token refresh failed: %w", err)
	}

	type subResult struct {
		nodes []InfraNode
		err   error
	}

	ch := make(chan subResult, len(p.subscriptionIDs))
	var wg sync.WaitGroup

	for _, subID := range p.subscriptionIDs {
		wg.Add(1)
		go func(sid string) {
			defer wg.Done()
			nodes, err := p.discoverSubscription(ctx, sid, region)
			ch <- subResult{nodes: nodes, err: err}
		}(subID)
	}

	wg.Wait()
	close(ch)

	var all []InfraNode
	var errs []string
	for r := range ch {
		if r.err != nil {
			errs = append(errs, r.err.Error())
			continue
		}
		all = append(all, r.nodes...)
	}

	if len(errs) > 0 {
		log.Printf("azure topology: %d subscription error(s): %s", len(errs), strings.Join(errs, "; "))
	}

	log.Printf("azure topology: discovered %d total nodes across %d subscription(s)",
		len(all), len(p.subscriptionIDs))
	return all, nil
}

// ---------------------------------------------------------------------------
// Per-subscription discovery
// ---------------------------------------------------------------------------

func (p *AzureTopologyProvider) discoverSubscription(ctx context.Context, subID, region string) ([]InfraNode, error) {
	rgs, err := p.resolveResourceGroups(ctx, subID)
	if err != nil {
		return nil, fmt.Errorf("sub=%s: list RGs: %w", subID, err)
	}

	type discoverFn func(ctx context.Context, subID, rg, region string) ([]InfraNode, error)

	discoverers := []discoverFn{
		p.discoverVMs,
		p.discoverVMSS,
		p.discoverAKS,
		p.discoverSQLDatabases,
		p.discoverFunctionApps,
		p.discoverStorageAccounts,
		p.discoverVNets,
		p.discoverAppGatewaysAndLBs,
		p.discoverCosmosDB,
	}

	type result struct {
		nodes []InfraNode
		err   error
	}

	ch := make(chan result, len(rgs)*len(discoverers))
	var wg sync.WaitGroup

	for _, rg := range rgs {
		for _, fn := range discoverers {
			wg.Add(1)
			go func(f discoverFn, r string) {
				defer wg.Done()
				nodes, err := f(ctx, subID, r, region)
				ch <- result{nodes: nodes, err: err}
			}(fn, rg)
		}
	}

	wg.Wait()
	close(ch)

	var all []InfraNode
	for r := range ch {
		if r.err != nil {
			log.Printf("azure topology: sub=%s discovery error: %v", subID, r.err)
			continue
		}
		all = append(all, r.nodes...)
	}
	return all, nil
}

// resolveResourceGroups returns the list of resource groups to scan.  When
// p.resourceGroups is non-empty the filter is applied; otherwise all RGs in
// the subscription are listed from the ARM API.
func (p *AzureTopologyProvider) resolveResourceGroups(ctx context.Context, subID string) ([]string, error) {
	if len(p.resourceGroups) > 0 {
		return p.resourceGroups, nil
	}

	// List all RGs via ARM
	path := fmt.Sprintf("/subscriptions/%s/resourcegroups", subID)
	page, err := p.armList(ctx, path, "2022-09-01")
	if err != nil {
		return nil, err
	}

	var rgs []string
	for _, raw := range page {
		var res azureResourceBase
		if err := json.Unmarshal(raw, &res); err != nil {
			continue
		}
		// Extract RG name from the last path segment of .id
		parts := strings.Split(res.ID, "/")
		if len(parts) > 0 {
			rgs = append(rgs, parts[len(parts)-1])
		}
	}

	if len(rgs) == 0 {
		return nil, fmt.Errorf("no resource groups found in subscription %s", subID)
	}
	return rgs, nil
}

// ---------------------------------------------------------------------------
// Resource-specific discoverers
// ---------------------------------------------------------------------------

// discoverVMs lists Azure Virtual Machines in a resource group.
func (p *AzureTopologyProvider) discoverVMs(ctx context.Context, subID, rg, region string) ([]InfraNode, error) {
	path := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines", subID, rg)
	raws, err := p.armList(ctx, path, azureAPIVersionVM)
	if err != nil {
		return nil, fmt.Errorf("vm list rg=%s: %w", rg, err)
	}

	var nodes []InfraNode
	for _, raw := range raws {
		var vm struct {
			azureResourceBase
			Properties struct {
				azureProvisioningState
				HardwareProfile struct {
					VMSize string `json:"vmSize"`
				} `json:"hardwareProfile"`
				StorageProfile struct {
					OSDisk struct {
						OSType string `json:"osType"`
					} `json:"osDisk"`
				} `json:"storageProfile"`
				OSProfile struct {
					ComputerName string `json:"computerName"`
				} `json:"osProfile"`
				NetworkProfile struct {
					NetworkInterfaces []struct {
						ID string `json:"id"`
					} `json:"networkInterfaces"`
				} `json:"networkProfile"`
				PowerState string `json:"powerState"` // populated via instanceView
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &vm); err != nil {
			continue
		}

		status, health := azureProvisioningToStatus(vm.Properties.ProvisioningState)
		nodes = append(nodes, p.buildNode(
			AzureResourceVM,
			vm.ID,
			vm.Name,
			vm.Location,
			subID, rg,
			status, health,
			map[string]interface{}{
				"vm_size":       vm.Properties.HardwareProfile.VMSize,
				"os_type":       vm.Properties.StorageProfile.OSDisk.OSType,
				"computer_name": vm.Properties.OSProfile.ComputerName,
				"nic_count":     len(vm.Properties.NetworkProfile.NetworkInterfaces),
			},
			vm.Tags,
		))
	}
	return nodes, nil
}

// discoverVMSS lists Azure Virtual Machine Scale Sets.
func (p *AzureTopologyProvider) discoverVMSS(ctx context.Context, subID, rg, region string) ([]InfraNode, error) {
	path := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachineScaleSets", subID, rg)
	raws, err := p.armList(ctx, path, azureAPIVersionVM)
	if err != nil {
		return nil, fmt.Errorf("vmss list rg=%s: %w", rg, err)
	}

	var nodes []InfraNode
	for _, raw := range raws {
		var vmss struct {
			azureResourceBase
			SKU struct {
				Name     string `json:"name"`
				Capacity int    `json:"capacity"`
			} `json:"sku"`
			Properties struct {
				azureProvisioningState
				Overprovision  bool   `json:"overprovision"`
				UpgradePolicy  struct{ Mode string `json:"mode"` } `json:"upgradePolicy"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &vmss); err != nil {
			continue
		}

		status, health := azureProvisioningToStatus(vmss.Properties.ProvisioningState)
		nodes = append(nodes, p.buildNode(
			AzureResourceVMSS,
			vmss.ID,
			vmss.Name,
			vmss.Location,
			subID, rg,
			status, health,
			map[string]interface{}{
				"sku":            vmss.SKU.Name,
				"capacity":       vmss.SKU.Capacity,
				"overprovision":  vmss.Properties.Overprovision,
				"upgrade_policy": vmss.Properties.UpgradePolicy.Mode,
			},
			vmss.Tags,
		))
	}
	return nodes, nil
}

// discoverAKS lists Azure Kubernetes Service managed clusters.
func (p *AzureTopologyProvider) discoverAKS(ctx context.Context, subID, rg, region string) ([]InfraNode, error) {
	path := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters", subID, rg)
	raws, err := p.armList(ctx, path, azureAPIVersionAKS)
	if err != nil {
		return nil, fmt.Errorf("aks list rg=%s: %w", rg, err)
	}

	var nodes []InfraNode
	for _, raw := range raws {
		var aks struct {
			azureResourceBase
			Properties struct {
				azureProvisioningState
				KubernetesVersion  string `json:"kubernetesVersion"`
				DNSPrefix          string `json:"dnsPrefix"`
				Fqdn               string `json:"fqdn"`
				NodeResourceGroup  string `json:"nodeResourceGroup"`
				PowerState         struct{ Code string `json:"code"` } `json:"powerState"`
				AgentPoolProfiles  []struct {
					Name  string `json:"name"`
					Count int    `json:"count"`
					VMSize string `json:"vmSize"`
					Mode   string `json:"mode"`
				} `json:"agentPoolProfiles"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &aks); err != nil {
			continue
		}

		totalNodes := 0
		for _, ap := range aks.Properties.AgentPoolProfiles {
			totalNodes += ap.Count
		}

		status, health := azureProvisioningToStatus(aks.Properties.ProvisioningState)
		if aks.Properties.PowerState.Code == "Stopped" {
			status = "stopped"
			health = "degraded"
		}

		nodes = append(nodes, p.buildNode(
			AzureResourceAKSNode,
			aks.ID,
			aks.Name,
			aks.Location,
			subID, rg,
			status, health,
			map[string]interface{}{
				"kubernetes_version":   aks.Properties.KubernetesVersion,
				"fqdn":                 aks.Properties.Fqdn,
				"dns_prefix":           aks.Properties.DNSPrefix,
				"node_resource_group":  aks.Properties.NodeResourceGroup,
				"total_node_count":     totalNodes,
				"agent_pool_count":     len(aks.Properties.AgentPoolProfiles),
			},
			aks.Tags,
		))
	}
	return nodes, nil
}

// discoverSQLDatabases lists Azure SQL Servers and their databases.
func (p *AzureTopologyProvider) discoverSQLDatabases(ctx context.Context, subID, rg, region string) ([]InfraNode, error) {
	// List SQL servers first, then their databases
	serversPath := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Sql/servers", subID, rg)
	serverRaws, err := p.armList(ctx, serversPath, azureAPIVersionSQL)
	if err != nil {
		return nil, fmt.Errorf("sql server list rg=%s: %w", rg, err)
	}

	var nodes []InfraNode
	for _, raw := range serverRaws {
		var srv struct {
			azureResourceBase
			Properties struct {
				azureProvisioningState
				FullyQualifiedDomainName string `json:"fullyQualifiedDomainName"`
				AdministratorLogin       string `json:"administratorLogin"`
				Version                  string `json:"version"`
				State                    string `json:"state"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &srv); err != nil {
			continue
		}

		// List databases for this SQL server
		dbPath := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Sql/servers/%s/databases", subID, rg, srv.Name)
		dbRaws, err := p.armList(ctx, dbPath, azureAPIVersionSQL)
		if err != nil {
			log.Printf("azure topology: sql databases list srv=%s: %v", srv.Name, err)
			dbRaws = nil
		}

		for _, dbRaw := range dbRaws {
			var db struct {
				azureResourceBase
				SKU struct {
					Name    string `json:"name"`
					Tier    string `json:"tier"`
					Capacity int   `json:"capacity"`
				} `json:"sku"`
				Properties struct {
					azureProvisioningState
					Collation          string `json:"collation"`
					Status             string `json:"status"`
					DatabaseID         string `json:"databaseId"`
					Edition            string `json:"edition"`
					MaxSizeBytes       int64  `json:"maxSizeBytes"`
					ZoneRedundant      bool   `json:"zoneRedundant"`
					LicenseType        string `json:"licenseType"`
					ReadScale          string `json:"readScale"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(dbRaw, &db); err != nil {
				continue
			}
			// Skip the master DB
			if strings.EqualFold(db.Name, "master") {
				continue
			}

			status, health := azureProvisioningToStatus(db.Properties.ProvisioningState)
			if db.Properties.Status != "" {
				status, health = azureSQLStatusToInfra(db.Properties.Status)
			}

			props := map[string]interface{}{
				"server":           srv.Name,
				"server_fqdn":      srv.Properties.FullyQualifiedDomainName,
				"collation":        db.Properties.Collation,
				"sku":              db.SKU.Name,
				"sku_tier":         db.SKU.Tier,
				"max_size_bytes":   db.Properties.MaxSizeBytes,
				"zone_redundant":   db.Properties.ZoneRedundant,
				"license_type":     db.Properties.LicenseType,
				"read_scale":       db.Properties.ReadScale,
			}
			// Inherit server tags and merge with DB tags
			mergedTags := mergeTags(srv.Tags, db.Tags)

			nodes = append(nodes, p.buildNode(
				AzureResourceSQLDatabase,
				db.ID,
				fmt.Sprintf("%s/%s", srv.Name, db.Name),
				db.Location,
				subID, rg,
				status, health,
				props,
				mergedTags,
			))
		}
	}
	return nodes, nil
}

// discoverFunctionApps lists Azure Function Apps.
func (p *AzureTopologyProvider) discoverFunctionApps(ctx context.Context, subID, rg, region string) ([]InfraNode, error) {
	path := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/sites", subID, rg)
	raws, err := p.armList(ctx, path, azureAPIVersionFn)
	if err != nil {
		return nil, fmt.Errorf("function app list rg=%s: %w", rg, err)
	}

	var nodes []InfraNode
	for _, raw := range raws {
		var site struct {
			azureResourceBase
			Kind       string `json:"kind"` // "functionapp" or "app"
			Properties struct {
				azureProvisioningState
				State            string `json:"state"`
				DefaultHostName  string `json:"defaultHostName"`
				Enabled          bool   `json:"enabled"`
				HTTPSOnly        bool   `json:"httpsOnly"`
				Kind             string `json:"kind"`
				ServerFarmID     string `json:"serverFarmId"`
				RuntimeAvailability string `json:"runtimeAvailabilityState"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &site); err != nil {
			continue
		}
		// Only emit Function Apps
		if !strings.Contains(strings.ToLower(site.Kind), "functionapp") &&
			!strings.Contains(strings.ToLower(site.Properties.Kind), "functionapp") {
			continue
		}

		status, health := azureWebAppStateToInfra(site.Properties.State)

		nodes = append(nodes, p.buildNode(
			AzureResourceFunctionApp,
			site.ID,
			site.Name,
			site.Location,
			subID, rg,
			status, health,
			map[string]interface{}{
				"default_hostname":  site.Properties.DefaultHostName,
				"https_only":        site.Properties.HTTPSOnly,
				"enabled":           site.Properties.Enabled,
				"kind":              site.Kind,
				"server_farm_id":    site.Properties.ServerFarmID,
			},
			site.Tags,
		))
	}
	return nodes, nil
}

// discoverStorageAccounts lists Azure Storage Accounts.
func (p *AzureTopologyProvider) discoverStorageAccounts(ctx context.Context, subID, rg, region string) ([]InfraNode, error) {
	path := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts", subID, rg)
	raws, err := p.armList(ctx, path, azureAPIVersionSt)
	if err != nil {
		return nil, fmt.Errorf("storage list rg=%s: %w", rg, err)
	}

	var nodes []InfraNode
	for _, raw := range raws {
		var sa struct {
			azureResourceBase
			SKU struct {
				Name string `json:"name"`
				Tier string `json:"tier"`
			} `json:"sku"`
			Kind       string `json:"kind"`
			Properties struct {
				azureProvisioningState
				AccessTier           string `json:"accessTier"`
				AllowBlobPublicAccess bool   `json:"allowBlobPublicAccess"`
				EnableHTTPSTrafficOnly bool  `json:"supportsHttpsTrafficOnly"`
				MinimumTLSVersion    string `json:"minimumTlsVersion"`
				PrimaryLocation      string `json:"primaryLocation"`
				StatusOfPrimary      string `json:"statusOfPrimary"`
				SecondaryLocation    string `json:"secondaryLocation"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &sa); err != nil {
			continue
		}

		status, health := azureProvisioningToStatus(sa.Properties.ProvisioningState)
		if sa.Properties.StatusOfPrimary == "unavailable" {
			status = "degraded"
			health = "degraded"
		}

		nodes = append(nodes, p.buildNode(
			AzureResourceStorage,
			sa.ID,
			sa.Name,
			sa.Location,
			subID, rg,
			status, health,
			map[string]interface{}{
				"sku":                      sa.SKU.Name,
				"sku_tier":                 sa.SKU.Tier,
				"kind":                     sa.Kind,
				"access_tier":              sa.Properties.AccessTier,
				"allow_blob_public_access": sa.Properties.AllowBlobPublicAccess,
				"https_only":               sa.Properties.EnableHTTPSTrafficOnly,
				"min_tls_version":          sa.Properties.MinimumTLSVersion,
				"primary_location":         sa.Properties.PrimaryLocation,
				"secondary_location":       sa.Properties.SecondaryLocation,
			},
			sa.Tags,
		))
	}
	return nodes, nil
}

// discoverVNets lists Virtual Networks and emits one InfraNode per subnet.
func (p *AzureTopologyProvider) discoverVNets(ctx context.Context, subID, rg, region string) ([]InfraNode, error) {
	path := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks", subID, rg)
	raws, err := p.armList(ctx, path, azureAPIVersionNet)
	if err != nil {
		return nil, fmt.Errorf("vnet list rg=%s: %w", rg, err)
	}

	var nodes []InfraNode
	for _, raw := range raws {
		var vnet struct {
			azureResourceBase
			Properties struct {
				azureProvisioningState
				AddressSpace struct {
					AddressPrefixes []string `json:"addressPrefixes"`
				} `json:"addressSpace"`
				Subnets []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
					Properties struct {
						AddressPrefix     string `json:"addressPrefix"`
						ProvisioningState string `json:"provisioningState"`
					} `json:"properties"`
				} `json:"subnets"`
				EnableDdosProtection bool `json:"enableDdosProtection"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &vnet); err != nil {
			continue
		}

		vnetStatus, vnetHealth := azureProvisioningToStatus(vnet.Properties.ProvisioningState)
		vnetNodeID := normaliseAzureID(vnet.ID)

		// One node for the VNet itself
		nodes = append(nodes, p.buildNode(
			"azure_vnet",
			vnet.ID,
			vnet.Name,
			vnet.Location,
			subID, rg,
			vnetStatus, vnetHealth,
			map[string]interface{}{
				"address_prefixes":      vnet.Properties.AddressSpace.AddressPrefixes,
				"subnet_count":          len(vnet.Properties.Subnets),
				"enable_ddos_protection": vnet.Properties.EnableDdosProtection,
			},
			vnet.Tags,
		))

		// One node per subnet, linked to VNet as parent
		for _, sn := range vnet.Properties.Subnets {
			snStatus, snHealth := azureProvisioningToStatus(sn.Properties.ProvisioningState)
			snNode := p.buildNode(
				"azure_subnet",
				sn.ID,
				fmt.Sprintf("%s/%s", vnet.Name, sn.Name),
				vnet.Location,
				subID, rg,
				snStatus, snHealth,
				map[string]interface{}{
					"address_prefix": sn.Properties.AddressPrefix,
					"vnet_name":      vnet.Name,
				},
				vnet.Tags,
			)
			snNode.ParentID = vnetNodeID
			snNode.Dependencies = []string{vnetNodeID}
			nodes = append(nodes, snNode)
		}
	}
	return nodes, nil
}

// discoverAppGatewaysAndLBs discovers Application Gateways and Load Balancers.
func (p *AzureTopologyProvider) discoverAppGatewaysAndLBs(ctx context.Context, subID, rg, region string) ([]InfraNode, error) {
	var nodes []InfraNode

	// Application Gateways
	agwPath := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/applicationGateways", subID, rg)
	agwRaws, err := p.armList(ctx, agwPath, azureAPIVersionAgw)
	if err != nil {
		log.Printf("azure topology: agw list rg=%s: %v", rg, err)
	} else {
		for _, raw := range agwRaws {
			var agw struct {
				azureResourceBase
				SKU struct {
					Name     string `json:"name"`
					Tier     string `json:"tier"`
					Capacity int    `json:"capacity"`
				} `json:"sku"`
				Properties struct {
					azureProvisioningState
					OperationalState        string `json:"operationalState"`
					BackendAddressPools     []json.RawMessage `json:"backendAddressPools"`
					BackendHTTPSettingsCollection []json.RawMessage `json:"backendHttpSettingsCollection"`
					HTTPListeners           []json.RawMessage `json:"httpListeners"`
					RequestRoutingRules     []json.RawMessage `json:"requestRoutingRules"`
					EnableHTTP2             bool   `json:"enableHttp2"`
					WebApplicationFirewallConfiguration *struct {
						Enabled bool `json:"enabled"`
					} `json:"webApplicationFirewallConfiguration"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(raw, &agw); err != nil {
				continue
			}

			status, health := azureAGWStateToInfra(agw.Properties.OperationalState)
			wafEnabled := agw.Properties.WebApplicationFirewallConfiguration != nil &&
				agw.Properties.WebApplicationFirewallConfiguration.Enabled

			nodes = append(nodes, p.buildNode(
				AzureResourceAppGateway,
				agw.ID,
				agw.Name,
				agw.Location,
				subID, rg,
				status, health,
				map[string]interface{}{
					"sku":                      agw.SKU.Name,
					"sku_tier":                 agw.SKU.Tier,
					"capacity":                 agw.SKU.Capacity,
					"operational_state":        agw.Properties.OperationalState,
					"enable_http2":             agw.Properties.EnableHTTP2,
					"waf_enabled":              wafEnabled,
					"backend_pool_count":       len(agw.Properties.BackendAddressPools),
					"listener_count":           len(agw.Properties.HTTPListeners),
					"routing_rule_count":       len(agw.Properties.RequestRoutingRules),
				},
				agw.Tags,
			))
		}
	}

	// Load Balancers
	lbPath := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/loadBalancers", subID, rg)
	lbRaws, err := p.armList(ctx, lbPath, azureAPIVersionNet)
	if err != nil {
		log.Printf("azure topology: lb list rg=%s: %v", rg, err)
	} else {
		for _, raw := range lbRaws {
			var lb struct {
				azureResourceBase
				SKU struct {
					Name string `json:"name"`
					Tier string `json:"tier"`
				} `json:"sku"`
				Properties struct {
					azureProvisioningState
					FrontendIPConfigurations []json.RawMessage `json:"frontendIPConfigurations"`
					BackendAddressPools      []json.RawMessage `json:"backendAddressPools"`
					LoadBalancingRules       []json.RawMessage `json:"loadBalancingRules"`
					Probes                   []json.RawMessage `json:"probes"`
					InboundNatRules          []json.RawMessage `json:"inboundNatRules"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(raw, &lb); err != nil {
				continue
			}

			status, health := azureProvisioningToStatus(lb.Properties.ProvisioningState)
			nodes = append(nodes, p.buildNode(
				"azure_load_balancer",
				lb.ID,
				lb.Name,
				lb.Location,
				subID, rg,
				status, health,
				map[string]interface{}{
					"sku":                     lb.SKU.Name,
					"sku_tier":                lb.SKU.Tier,
					"frontend_ip_count":       len(lb.Properties.FrontendIPConfigurations),
					"backend_pool_count":      len(lb.Properties.BackendAddressPools),
					"lb_rule_count":           len(lb.Properties.LoadBalancingRules),
					"probe_count":             len(lb.Properties.Probes),
					"inbound_nat_rule_count":  len(lb.Properties.InboundNatRules),
				},
				lb.Tags,
			))
		}
	}

	return nodes, nil
}

// discoverCosmosDB lists Azure Cosmos DB accounts.
func (p *AzureTopologyProvider) discoverCosmosDB(ctx context.Context, subID, rg, region string) ([]InfraNode, error) {
	path := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DocumentDB/databaseAccounts", subID, rg)
	raws, err := p.armList(ctx, path, azureAPIVersionCos)
	if err != nil {
		return nil, fmt.Errorf("cosmosdb list rg=%s: %w", rg, err)
	}

	var nodes []InfraNode
	for _, raw := range raws {
		var cosmos struct {
			azureResourceBase
			Kind string `json:"kind"` // "GlobalDocumentDB", "MongoDB", "Parse"
			Properties struct {
				azureProvisioningState
				DocumentEndpoint         string   `json:"documentEndpoint"`
				DatabaseAccountOfferType string   `json:"databaseAccountOfferType"`
				Locations                []struct {
					LocationName     string `json:"locationName"`
					FailoverPriority int    `json:"failoverPriority"`
					IsZoneRedundant  bool   `json:"isZoneRedundant"`
				} `json:"locations"`
				Capabilities []struct {
					Name string `json:"name"`
				} `json:"capabilities"`
				EnableAutomaticFailover       bool `json:"enableAutomaticFailover"`
				EnableMultipleWriteLocations  bool `json:"enableMultipleWriteLocations"`
				EnableFreeTier                bool `json:"enableFreeTier"`
				BackupPolicy                  struct {
					Type string `json:"type"` // "Continuous" or "Periodic"
				} `json:"backupPolicy"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &cosmos); err != nil {
			continue
		}

		caps := make([]string, 0, len(cosmos.Properties.Capabilities))
		for _, c := range cosmos.Properties.Capabilities {
			caps = append(caps, c.Name)
		}

		regions := make([]string, 0, len(cosmos.Properties.Locations))
		for _, loc := range cosmos.Properties.Locations {
			regions = append(regions, loc.LocationName)
		}

		status, health := azureProvisioningToStatus(cosmos.Properties.ProvisioningState)
		nodes = append(nodes, p.buildNode(
			AzureResourceCosmosDB,
			cosmos.ID,
			cosmos.Name,
			cosmos.Location,
			subID, rg,
			status, health,
			map[string]interface{}{
				"kind":                           cosmos.Kind,
				"document_endpoint":              cosmos.Properties.DocumentEndpoint,
				"offer_type":                     cosmos.Properties.DatabaseAccountOfferType,
				"replication_regions":            regions,
				"capabilities":                   caps,
				"enable_automatic_failover":      cosmos.Properties.EnableAutomaticFailover,
				"enable_multi_write_locations":   cosmos.Properties.EnableMultipleWriteLocations,
				"enable_free_tier":               cosmos.Properties.EnableFreeTier,
				"backup_policy_type":             cosmos.Properties.BackupPolicy.Type,
			},
			cosmos.Tags,
		))
	}
	return nodes, nil
}

// ---------------------------------------------------------------------------
// ARM REST client
// ---------------------------------------------------------------------------

// refreshToken acquires or renews the OAuth2 bearer token from AAD.
func (p *AzureTopologyProvider) refreshToken(ctx context.Context) error {
	p.token.mu.Lock()
	defer p.token.mu.Unlock()

	if p.token.value != "" && time.Now().Before(p.token.expiresAt.Add(-60*time.Second)) {
		return nil // still valid
	}

	tokenURL := fmt.Sprintf(azureTokenURL, p.tenantID)
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {p.clientID},
		"client_secret": {p.clientSecret},
		"scope":         {azureARMScope},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token request status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return fmt.Errorf("token parse: %w", err)
	}

	p.token.value = tokenResp.AccessToken
	p.token.expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	return nil
}

// armList performs a paginated ARM GET list and returns all raw JSON items.
func (p *AzureTopologyProvider) armList(ctx context.Context, resourcePath, apiVersion string) ([]json.RawMessage, error) {
	nextURL := fmt.Sprintf("%s%s?api-version=%s", azureARMBaseURL, resourcePath, apiVersion)

	var all []json.RawMessage
	for nextURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL, nil)
		if err != nil {
			return all, err
		}

		p.token.mu.Lock()
		token := p.token.value
		p.token.mu.Unlock()
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")

		resp, err := p.httpClient.Do(req)
		if err != nil {
			return all, fmt.Errorf("GET %s: %w", nextURL, err)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			// Resource type not present in this RG — treat as empty
			return all, nil
		}
		if resp.StatusCode != http.StatusOK {
			// On auth/quota errors stop immediately
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				return all, fmt.Errorf("ARM %s status %d (access denied)", resourcePath, resp.StatusCode)
			}
			// Other errors (e.g., 400 from unsupported API preview in region) — skip silently
			log.Printf("azure topology: ARM %s status %d — skipping", resourcePath, resp.StatusCode)
			return all, nil
		}

		var page azureARMPage
		if err := json.Unmarshal(body, &page); err != nil {
			return all, fmt.Errorf("parse page for %s: %w", resourcePath, err)
		}
		all = append(all, page.Value...)
		nextURL = page.NextLink
	}

	return all, nil
}

// ---------------------------------------------------------------------------
// Node builder
// ---------------------------------------------------------------------------

// buildNode constructs a canonical InfraNode from Azure resource fields.
func (p *AzureTopologyProvider) buildNode(
	resourceType, resourceID, name, location string,
	subID, rg string,
	status, health string,
	properties map[string]interface{},
	tags map[string]string,
) InfraNode {
	now := time.Now()

	// Enrich properties with Azure envelope fields
	properties["azure_resource_id"] = resourceID
	properties["subscription_id"] = subID
	properties["resource_group"] = rg
	properties["azure_location"] = location

	labels := map[string]string{
		"provider":          "azure",
		"resource_type":     resourceType,
		"subscription_id":   subID,
		"resource_group":    rg,
		"azure_location":    location,
	}
	for k, v := range tags {
		// Prefix Azure tags to avoid collisions with Aileron labels
		labels["az_tag_"+k] = v
	}

	return InfraNode{
		ID:            normaliseAzureID(resourceID),
		Name:          name,
		Type:          resourceType,
		Layer:         LayerVM, // Azure resources sit at the VM/infrastructure layer
		Region:        location,
		Status:        status,
		HealthStatus:  health,
		Properties:    properties,
		Labels:        labels,
		Dependencies:  []string{},
		Dependents:    []string{},
		LastDiscovery: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// ---------------------------------------------------------------------------
// Mapping helpers
// ---------------------------------------------------------------------------

// azureProvisioningToStatus maps ARM provisioningState to Aileron status/health.
func azureProvisioningToStatus(ps string) (status, health string) {
	switch strings.ToLower(ps) {
	case "succeeded":
		return "running", "healthy"
	case "creating", "updating", "provisioning":
		return "pending", "degraded"
	case "deleting":
		return "terminating", "degraded"
	case "failed":
		return "error", "unhealthy"
	case "canceled":
		return "stopped", "unknown"
	default:
		if ps == "" {
			return "unknown", "unknown"
		}
		return strings.ToLower(ps), "unknown"
	}
}

// azureSQLStatusToInfra maps Azure SQL database status strings.
func azureSQLStatusToInfra(sqlStatus string) (status, health string) {
	switch strings.ToLower(sqlStatus) {
	case "online":
		return "running", "healthy"
	case "creating", "recovering", "restoring", "scaling":
		return "pending", "degraded"
	case "paused", "pausing":
		return "stopped", "degraded"
	case "offline", "disabled":
		return "stopped", "unhealthy"
	case "errorstate":
		return "error", "unhealthy"
	default:
		return strings.ToLower(sqlStatus), "unknown"
	}
}

// azureWebAppStateToInfra maps App Service / Function App state strings.
func azureWebAppStateToInfra(state string) (status, health string) {
	switch strings.ToLower(state) {
	case "running":
		return "running", "healthy"
	case "stopped":
		return "stopped", "degraded"
	default:
		return strings.ToLower(state), "unknown"
	}
}

// azureAGWStateToInfra maps Application Gateway operational state.
func azureAGWStateToInfra(state string) (status, health string) {
	switch strings.ToLower(state) {
	case "running":
		return "running", "healthy"
	case "stopped":
		return "stopped", "degraded"
	case "starting":
		return "pending", "degraded"
	case "stopping":
		return "terminating", "degraded"
	default:
		if state == "" {
			return "unknown", "unknown"
		}
		return strings.ToLower(state), "unknown"
	}
}

// ---------------------------------------------------------------------------
// Utility helpers
// ---------------------------------------------------------------------------

// normaliseAzureID converts a full ARM resource ID to a compact node ID by
// lower-casing and replacing "/" with ":".
func normaliseAzureID(id string) string {
	return strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(id, "/", ":"), ":"))
}

// splitTrim splits s by sep and trims whitespace from each element.
func splitTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	var out []string
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// mergeTags merges two tag maps; values in override take precedence.
func mergeTags(base, override map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}
