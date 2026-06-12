// Package informers sets up all client-go SharedInformers for the KubeSense agent.
package informers

import (
	"context"
	"log"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

const defaultResync = 30 * time.Second

// ResourceHandlers holds one EventHandler per watched resource kind.
// Each handler converts K8s objects → IntelligenceEvents and publishes to Kafka.
type ResourceHandlers struct {
	Pods               cache.ResourceEventHandler
	Nodes              cache.ResourceEventHandler
	Deployments        cache.ResourceEventHandler
	StatefulSets       cache.ResourceEventHandler
	DaemonSets         cache.ResourceEventHandler
	ReplicaSets        cache.ResourceEventHandler
	Jobs               cache.ResourceEventHandler
	CronJobs           cache.ResourceEventHandler
	Services           cache.ResourceEventHandler
	EndpointSlices     cache.ResourceEventHandler
	Ingresses          cache.ResourceEventHandler
	NetworkPolicies    cache.ResourceEventHandler
	PVCs               cache.ResourceEventHandler
	PVs                cache.ResourceEventHandler
	StorageClasses     cache.ResourceEventHandler
	ConfigMaps         cache.ResourceEventHandler
	Secrets            cache.ResourceEventHandler // metadata only
	Namespaces         cache.ResourceEventHandler
	ServiceAccounts    cache.ResourceEventHandler
	Roles              cache.ResourceEventHandler
	RoleBindings       cache.ResourceEventHandler
	ClusterRoles       cache.ResourceEventHandler
	ClusterRoleBindings cache.ResourceEventHandler
	HPAs               cache.ResourceEventHandler
	K8sEvents          cache.ResourceEventHandler
}

// Factory manages all informers with a shared cache.
type Factory struct {
	factory informers.SharedInformerFactory
	client  kubernetes.Interface
}

// NewFactory creates a SharedInformerFactory watching all namespaces.
func NewFactory(client kubernetes.Interface) *Factory {
	return &Factory{
		factory: informers.NewSharedInformerFactory(client, defaultResync),
		client:  client,
	}
}

// ListDeployments returns all Deployment objects currently in the informer cache.
func (f *Factory) ListDeployments() []*appsv1.Deployment {
	objs := f.factory.Apps().V1().Deployments().Informer().GetStore().List()
	deps := make([]*appsv1.Deployment, 0, len(objs))
	for _, o := range objs {
		if d, ok := o.(*appsv1.Deployment); ok {
			deps = append(deps, d)
		}
	}
	return deps
}

// ListPods returns all Pod objects currently in the informer cache.
func (f *Factory) ListPods() []*corev1.Pod {
	objs := f.factory.Core().V1().Pods().Informer().GetStore().List()
	pods := make([]*corev1.Pod, 0, len(objs))
	for _, o := range objs {
		if p, ok := o.(*corev1.Pod); ok {
			pods = append(pods, p)
		}
	}
	return pods
}

// ListPDBs fetches PodDisruptionBudgets via the K8s API (not all clusters support policyV1 informer).
func (f *Factory) ListPDBs(ctx context.Context) []*policyv1.PodDisruptionBudget {
	if f.client == nil {
		return nil
	}
	pdbList, err := f.client.PolicyV1().PodDisruptionBudgets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	out := make([]*policyv1.PodDisruptionBudget, len(pdbList.Items))
	for i := range pdbList.Items {
		out[i] = &pdbList.Items[i]
	}
	return out
}

// Register adds event handlers to all informers.
// Handlers that are nil are replaced with a no-op so the informer cache
// still syncs (required for topology completeness) without panicking.
func (f *Factory) Register(h ResourceHandlers) {
	noop := cache.ResourceEventHandlerFuncs{} // empty = silently cache, emit nothing

	// Core workloads
	f.factory.Core().V1().Pods().Informer().AddEventHandler(orNoop(h.Pods, noop))
	f.factory.Core().V1().Nodes().Informer().AddEventHandler(orNoop(h.Nodes, noop))
	f.factory.Apps().V1().Deployments().Informer().AddEventHandler(orNoop(h.Deployments, noop))
	f.factory.Apps().V1().StatefulSets().Informer().AddEventHandler(orNoop(h.StatefulSets, noop))
	f.factory.Apps().V1().DaemonSets().Informer().AddEventHandler(orNoop(h.DaemonSets, noop))
	f.factory.Apps().V1().ReplicaSets().Informer().AddEventHandler(orNoop(h.ReplicaSets, noop))
	f.factory.Batch().V1().Jobs().Informer().AddEventHandler(orNoop(h.Jobs, noop))
	f.factory.Batch().V1().CronJobs().Informer().AddEventHandler(orNoop(h.CronJobs, noop))

	// Networking
	f.factory.Core().V1().Services().Informer().AddEventHandler(orNoop(h.Services, noop))
	f.factory.Discovery().V1().EndpointSlices().Informer().AddEventHandler(orNoop(h.EndpointSlices, noop))
	f.factory.Networking().V1().Ingresses().Informer().AddEventHandler(orNoop(h.Ingresses, noop))
	f.factory.Networking().V1().NetworkPolicies().Informer().AddEventHandler(orNoop(h.NetworkPolicies, noop))

	// Storage
	f.factory.Core().V1().PersistentVolumeClaims().Informer().AddEventHandler(orNoop(h.PVCs, noop))
	f.factory.Core().V1().PersistentVolumes().Informer().AddEventHandler(orNoop(h.PVs, noop))
	f.factory.Storage().V1().StorageClasses().Informer().AddEventHandler(orNoop(h.StorageClasses, noop))

	// Configuration — secrets use metadata-only transform
	f.factory.Core().V1().ConfigMaps().Informer().AddEventHandler(orNoop(h.ConfigMaps, noop))
	secretInformer := f.factory.Core().V1().Secrets().Informer()
	secretInformer.SetTransform(stripSecretData)
	secretInformer.AddEventHandler(orNoop(h.Secrets, noop))

	f.factory.Core().V1().Namespaces().Informer().AddEventHandler(orNoop(h.Namespaces, noop))
	f.factory.Core().V1().ServiceAccounts().Informer().AddEventHandler(orNoop(h.ServiceAccounts, noop))

	// RBAC
	f.factory.Rbac().V1().Roles().Informer().AddEventHandler(orNoop(h.Roles, noop))
	f.factory.Rbac().V1().RoleBindings().Informer().AddEventHandler(orNoop(h.RoleBindings, noop))
	f.factory.Rbac().V1().ClusterRoles().Informer().AddEventHandler(orNoop(h.ClusterRoles, noop))
	f.factory.Rbac().V1().ClusterRoleBindings().Informer().AddEventHandler(orNoop(h.ClusterRoleBindings, noop))

	// Autoscaling
	f.factory.Autoscaling().V2().HorizontalPodAutoscalers().Informer().AddEventHandler(orNoop(h.HPAs, noop))

	// Native K8s events
	f.factory.Core().V1().Events().Informer().AddEventHandler(orNoop(h.K8sEvents, noop))
}

// orNoop returns h if non-nil, otherwise the no-op handler.
// Passing a nil ResourceEventHandler to AddEventHandler panics.
func orNoop(h cache.ResourceEventHandler, noop cache.ResourceEventHandler) cache.ResourceEventHandler {
	if h == nil {
		return noop
	}
	return h
}

// Start launches all informers and blocks until the cache is synced.
func (f *Factory) Start(ctx context.Context) {
	f.factory.Start(ctx.Done())

	log.Println("kubesense-agent: waiting for informer cache sync...")
	synced := f.factory.WaitForCacheSync(ctx.Done())
	for resource, ok := range synced {
		if !ok {
			log.Printf("kubesense-agent: cache sync FAILED for %v", resource)
		}
	}
	log.Println("kubesense-agent: all informer caches synced")
}

// stripSecretData is an informer transform that zeros all secret values before
// the object enters the informer cache. Keys are preserved for metadata analysis.
// Secret values NEVER enter agent memory, the topology graph, or Kafka.
func stripSecretData(obj interface{}) (interface{}, error) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return obj, nil
	}
	clone := secret.DeepCopy()
	for k := range clone.Data {
		clone.Data[k] = nil
	}
	for k := range clone.StringData {
		clone.StringData[k] = ""
	}
	return clone, nil
}
