package topology

import (
	"context"
	"fmt"
	"log"
	"sync"
)

// MultiCloudStackDiscoverer handles multiple CloudStack clusters
type MultiCloudStackDiscoverer struct {
	clients  []*CloudStackClient
	clusters []string
}

func NewMultiCloudStackDiscoverer(configs []map[string]interface{}) *MultiCloudStackDiscoverer {
	discoverer := &MultiCloudStackDiscoverer{
		clients:  make([]*CloudStackClient, 0),
		clusters: make([]string, 0),
	}

	for i, config := range configs {
		endpoint, _ := config["endpoint"].(string)
		apiKey, _ := config["apiKey"].(string)
		secretKey, _ := config["secretKey"].(string)
		clusterName, _ := config["clusterName"].(string)

		if endpoint != "" && apiKey != "" && secretKey != "" {
			client := NewCloudStackClient(endpoint, apiKey, secretKey)
			discoverer.clients = append(discoverer.clients, client)

			if clusterName == "" {
				clusterName = fmt.Sprintf("cloudstack-cluster-%d", i+1)
			}
			discoverer.clusters = append(discoverer.clusters, clusterName)

			log.Printf("Configured CloudStack client %d: %s (%s)", i+1, endpoint, clusterName)
		} else {
			log.Printf("Invalid CloudStack config %d: missing credentials", i+1)
		}
	}

	return discoverer
}

func (d *MultiCloudStackDiscoverer) GetName() string {
	return fmt.Sprintf("Multi-CloudStack Discoverer (%d clusters)", len(d.clients))
}

func (d *MultiCloudStackDiscoverer) GetLayer() InfrastructureLayer {
	return LayerCloudStack
}

func (d *MultiCloudStackDiscoverer) Discover(ctx context.Context, region string) ([]InfraNode, error) {
	var allNodes []InfraNode
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []error

	log.Printf("Starting multi-CloudStack discovery for region %s with %d clusters", region, len(d.clients))

	// Discover from all CloudStack clusters in parallel
	for i, client := range d.clients {
		wg.Add(1)
		go func(clientIndex int, cs *CloudStackClient, clusterName string) {
			defer wg.Done()

			log.Printf("Discovering CloudStack cluster %d: %s", clientIndex+1, clusterName)

			// Get KVM hosts
			hosts, err := cs.ListHosts(ctx)
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("cluster %s hosts: %v", clusterName, err))
				mu.Unlock()
				log.Printf("Failed to get hosts from cluster %s: %v", clusterName, err)
				return
			}

			// Get VMs
			vms, err := cs.ListVirtualMachines(ctx)
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("cluster %s VMs: %v", clusterName, err))
				mu.Unlock()
				log.Printf("Failed to get VMs from cluster %s: %v", clusterName, err)
				return
			}

			log.Printf("Cluster %s: %d hosts, %d VMs", clusterName, len(hosts), len(vms))

			var clusterNodes []InfraNode

			// Create nodes for KVM hosts
			for _, host := range hosts {
				node := InfraNode{
					ID:           fmt.Sprintf("%s-%s", clusterName, host.ID),
					Name:         host.Name, // actual hostname from CloudStack API
					Type:         "kvm_host",
					Layer:        LayerBareMetal,
					Region:       region,
					Status:       host.State,
					HealthStatus: getHealthFromState(host.State),
					Properties: map[string]interface{}{
						"cloudstack_cluster":  clusterName,
						"original_host_id":    host.ID,
						"ip_address":          host.IPAddress,
						"cluster":             host.ClusterName,
						"zone":                host.ZoneName,
						"hypervisor":          host.Hypervisor,
						"cpu_cores":           host.CPUNumber,
						"cpu_speed_mhz":       host.CPUSpeed,
						"memory_total_mb":     host.MemoryTotal / 1024 / 1024,
						"memory_used_mb":      host.MemoryAllocated / 1024 / 1024,
						"cloudstack_endpoint": cs.Endpoint,
					},
					Labels: map[string]string{
						"type":               "kvm",
						"cloudstack_cluster": clusterName,
						"zone":               host.ZoneName,
						"region":             region,
					},
					Dependencies: []string{},
					Dependents:   []string{},
				}
				clusterNodes = append(clusterNodes, node)
			}

			// Create nodes for VMs
			for _, vm := range vms {
				vmNode := InfraNode{
					ID:           fmt.Sprintf("%s-%s", clusterName, vm.ID),
					Name:         vm.Name, // actual VM name from CloudStack API
					Type:         "cloudstack_vm",
					Layer:        LayerCloudStack,
					Region:       region,
					ParentID:     fmt.Sprintf("%s-%s", clusterName, vm.HostID),
					Status:       vm.State,
					HealthStatus: getHealthFromState(vm.State),
					Properties: map[string]interface{}{
						"cloudstack_cluster":  clusterName,
						"original_vm_id":      vm.ID,
						"original_host_id":    vm.HostID,
						"display_name":        vm.DisplayName,
						"kvm_host":            vm.HostName,
						"zone":                vm.ZoneName,
						"template":            vm.TemplateName,
						"service_offering":    vm.ServiceOffering,
						"vcpu":                vm.CPUNumber,
						"memory_mb":           vm.Memory,
						"cloudstack_endpoint": cs.Endpoint,
					},
					Labels: map[string]string{
						"cloudstack_cluster": clusterName,
						"kvm_host":           vm.HostName,
						"zone":               vm.ZoneName,
						"template":           vm.TemplateName,
						"region":             region,
					},
					Dependencies: []string{fmt.Sprintf("%s-%s", clusterName, vm.HostID)},
					Dependents:   []string{},
				}

				// Add IP addresses if available
				if len(vm.NICs) > 0 {
					vmNode.Properties["ip_address"] = vm.NICs[0].IPAddress
					vmNode.Properties["mac_address"] = vm.NICs[0].MACAddress
				}

				clusterNodes = append(clusterNodes, vmNode)
			}

			// Add cluster nodes to global list
			mu.Lock()
			allNodes = append(allNodes, clusterNodes...)
			mu.Unlock()

			log.Printf("Cluster %s discovery complete: %d total nodes", clusterName, len(clusterNodes))

		}(i, client, d.clusters[i])
	}

	wg.Wait()

	if len(errors) > 0 {
		log.Printf("Multi-CloudStack discovery completed with %d errors", len(errors))
		for _, err := range errors {
			log.Printf("   Error: %v", err)
		}

		// Return partial results if some clusters succeeded
		if len(allNodes) > 0 {
			log.Printf("Partial success: %d nodes discovered despite %d errors", len(allNodes), len(errors))
			return allNodes, nil
		}

		return nil, fmt.Errorf("all CloudStack clusters failed: %d errors", len(errors))
	}

	log.Printf("Multi-CloudStack discovery SUCCESS: %d total nodes from %d clusters", len(allNodes), len(d.clients))
	return allNodes, nil
}

// filterNodesByLayer filters nodes by infrastructure layer
func filterNodesByLayer(nodes []InfraNode, targetLayer InfrastructureLayer) []InfraNode {
	filtered := make([]InfraNode, 0)
	for _, node := range nodes {
		if node.Layer == targetLayer {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// NewMultiCloudStackDiscoverer is imported from enterprise_topology.go to avoid conflicts
