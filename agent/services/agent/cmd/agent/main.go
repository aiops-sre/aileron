// main.go — kubesense-agent entry point
//
// Deploys in the aileron-agent namespace, uses the existing aileron-agent-sa service account
// (ClusterRole ai-cluster-readonly: get/list/watch on *.*).
// Publishes to the existing Strimzi Kafka cluster in aileron.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/aileron-platform/aileron/agent/services/agent/internal/health"
	"github.com/aileron-platform/aileron/agent/services/agent/internal/informers"
	"github.com/aileron-platform/aileron/agent/services/agent/internal/publisher"
	"github.com/aileron-platform/aileron/agent/services/agent/internal/topology"
	"github.com/aileron-platform/aileron/agent/pkg/chaos"
	"github.com/aileron-platform/aileron/agent/pkg/configscan"
	"github.com/aileron-platform/aileron/agent/pkg/kafkapub"
	"github.com/aileron-platform/aileron/agent/pkg/events"

	corev1 "k8s.io/api/core/v1"
	appsv1 "k8s.io/api/apps/v1"
)

var version = "dev"

func main() {
	var (
		kafkaBrokers = flag.String("kafka-brokers",
			envOrDefault("KAFKA_BROKERS",
				"alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092"),
			"Comma-separated Strimzi Kafka bootstrap servers")
		clusterID  = flag.String("cluster-id", envOrDefault("CLUSTER_ID", ""), "Unique cluster identifier (required)")
		agentID    = flag.String("agent-id", envOrDefault("AGENT_ID", ""), "Agent identifier")
		kubeconfig = flag.String("kubeconfig", "", "Path to kubeconfig (empty = in-cluster)")
		bufferMax  = flag.Int("buffer-max", 5000, "Max events to buffer locally when Kafka unavailable")
		healthAddr = flag.String("health-addr", ":8081", "Address for /healthz and /readyz endpoints")
	)
	flag.Parse()

	if *clusterID == "" {
		log.Fatal("kubesense-agent: --cluster-id is required")
	}
	if *agentID == "" {
		hostname, _ := os.Hostname()
		*agentID = "kubesense-agent-" + hostname
	}

	log.Printf("kubesense-agent v%s starting: cluster=%s agent=%s", version, *clusterID, *agentID)

	// Kubernetes client
	k8sClient := buildK8sClient(*kubeconfig)

	// Kafka publisher — connect to existing Strimzi cluster.
	// Non-fatal: agent still watches and buffers if Kafka is initially unavailable.
	brokers := strings.Split(*kafkaBrokers, ",")
	pub, err := publisher.New(brokers, *clusterID, *agentID, version, *bufferMax)
	if err != nil {
		log.Printf("kubesense-agent: Kafka unavailable at startup (%v) — buffering until reconnect", err)
	}

	// Topology graph (in-memory, rebuilt from informer cache)
	graph := topology.NewGraph()

	// Health detector
	detector := health.NewDetector(pub, *clusterID)

	// Informer factory
	factory := informers.NewFactory(k8sClient)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start health server before informers so readiness probe passes after cache sync
	ready := make(chan struct{})
	go serveHealth(*healthAddr, ready)

	// Register informer event handlers
	factory.Register(informers.ResourceHandlers{
		Pods: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				pod, ok := obj.(*corev1.Pod)
				if !ok {
					return
				}
				graph.UpsertPod(pod)
				if pub != nil {
					pub.Publish(ctx, makeEvent(events.EventResourceCreated, *clusterID, "Pod", pod.Namespace, pod.Name, string(pod.UID), pod.Labels))
				}
			},
			UpdateFunc: func(_, newObj interface{}) {
				pod, ok := newObj.(*corev1.Pod)
				if !ok {
					return
				}
				graph.UpsertPod(pod)
				detector.OnPodUpdate(ctx, pod)
				if pub != nil {
					pub.Publish(ctx, makeEvent(events.EventResourceUpdated, *clusterID, "Pod", pod.Namespace, pod.Name, string(pod.UID), pod.Labels))
				}
			},
			DeleteFunc: func(obj interface{}) {
				pod, ok := obj.(*corev1.Pod)
				if !ok {
					return
				}
				graph.RemovePod(pod.Namespace, pod.Name)
				if pub != nil {
					pub.Publish(ctx, makeEvent(events.EventResourceDeleted, *clusterID, "Pod", pod.Namespace, pod.Name, string(pod.UID), pod.Labels))
				}
			},
		},
		Nodes: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				node, ok := obj.(*corev1.Node)
				if !ok {
					return
				}
				if pub != nil {
					pub.Publish(ctx, makeEvent(events.EventResourceCreated, *clusterID, "Node", "", node.Name, string(node.UID), node.Labels))
				}
			},
			UpdateFunc: func(_, newObj interface{}) {
				node, ok := newObj.(*corev1.Node)
				if !ok {
					return
				}
				detector.OnNodeUpdate(ctx, node)
				if pub != nil {
					pub.Publish(ctx, makeEvent(events.EventResourceUpdated, *clusterID, "Node", "", node.Name, string(node.UID), node.Labels))
				}
			},
			DeleteFunc: func(obj interface{}) {
				node, ok := obj.(*corev1.Node)
				if !ok {
					return
				}
				if pub != nil {
					pub.Publish(ctx, makeEvent(events.EventResourceDeleted, *clusterID, "Node", "", node.Name, string(node.UID), node.Labels))
				}
			},
		},
		Deployments: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				dep, ok := obj.(*appsv1.Deployment)
				if !ok {
					return
				}
				graph.UpsertDeployment(dep)
				if pub != nil {
					pub.Publish(ctx, makeEvent(events.EventResourceCreated, *clusterID, "Deployment", dep.Namespace, dep.Name, string(dep.UID), dep.Labels))
				}
			},
			UpdateFunc: func(old, newObj interface{}) {
				dep, ok := newObj.(*appsv1.Deployment)
				if !ok {
					return
				}
				oldDep, _ := old.(*appsv1.Deployment)
				graph.UpsertDeployment(dep)
				if pub != nil && oldDep != nil && oldDep.ResourceVersion != dep.ResourceVersion {
					ev := makeEvent(events.EventDeploymentRollout, *clusterID, "Deployment", dep.Namespace, dep.Name, string(dep.UID), dep.Labels)
					replicaStr := "0"
					if dep.Spec.Replicas != nil {
						replicaStr = fmt.Sprintf("%d", *dep.Spec.Replicas)
					}
					ev.Annotations = map[string]string{
						"replicas_desired": replicaStr,
						"replicas_ready":   fmt.Sprintf("%d", dep.Status.ReadyReplicas),
						"resource_version": dep.ResourceVersion,
					}
					pub.Publish(ctx, ev)
				}
			},
			DeleteFunc: func(obj interface{}) {
				dep, ok := obj.(*appsv1.Deployment)
				if !ok {
					return
				}
				if pub != nil {
					pub.Publish(ctx, makeEvent(events.EventResourceDeleted, *clusterID, "Deployment", dep.Namespace, dep.Name, string(dep.UID), dep.Labels))
				}
			},
		},
		ConfigMaps: cache.ResourceEventHandlerFuncs{
			UpdateFunc: func(old, newObj interface{}) {
				cm, ok := newObj.(*corev1.ConfigMap)
				if !ok {
					return
				}
				oldCM, _ := old.(*corev1.ConfigMap)
				if pub != nil && oldCM != nil && oldCM.ResourceVersion != cm.ResourceVersion {
					ev := makeEvent(events.EventConfigMapChanged, *clusterID, "ConfigMap", cm.Namespace, cm.Name, string(cm.UID), cm.Labels)
					ev.Annotations = map[string]string{
						"resource_version": cm.ResourceVersion,
						"key_count":        fmt.Sprintf("%d", len(cm.Data)),
					}
					pub.Publish(ctx, ev)
				}
			},
		},
		Secrets: cache.ResourceEventHandlerFuncs{
			// Secret values stripped by informer transform — metadata only
			UpdateFunc: func(old, newObj interface{}) {
				secret, ok := newObj.(*corev1.Secret)
				if !ok {
					return
				}
				oldSecret, _ := old.(*corev1.Secret)
				if pub != nil && oldSecret != nil && oldSecret.ResourceVersion != secret.ResourceVersion {
					ev := makeEvent(events.EventSecretRotated, *clusterID, "Secret", secret.Namespace, secret.Name, string(secret.UID), secret.Labels)
					ev.Annotations = map[string]string{
						"secret_type":      string(secret.Type),
						"resource_version": secret.ResourceVersion,
					}
					pub.Publish(ctx, ev)
				}
			},
		},
	})

	factory.Start(ctx)

	// Signal readiness after cache sync
	close(ready)
	log.Printf("kubesense-agent: cache synced — cluster=%s nodes=%d", *clusterID, graph.NodeCount())

	// ── Chaos readiness scorer ─────────────────────────────────────────────────
	// Periodically scores all Deployments for failure tolerance and publishes
	// the cluster-level score to kubesense.chaos.scores so AlertHub's OIE
	// evidence bus can surface chaos readiness as an investigation signal.
	chaosPub := kafkapub.New(strings.Join(brokers, ","))
	if chaosPub != nil {
		go func() {
			defer chaosPub.Close()
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			// Run immediately on startup.
			chaos.RunScoring(*clusterID,
				factory.ListDeployments(), factory.ListPods(), factory.ListPDBs(ctx),
				chaosPub)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					chaos.RunScoring(*clusterID,
						factory.ListDeployments(), factory.ListPods(), factory.ListPDBs(ctx),
						chaosPub)
				}
			}
		}()
		log.Printf("kubesense-agent: chaos readiness scorer started for cluster=%s", *clusterID)
	}

	// ── Config violation scanner ───────────────────────────────────────────────
	// Scans all Deployments every 5 min for missing probes, resource limits,
	// single replicas, and :latest image tags. Publishes to kubesense.config.violations
	// so AlertHub's consumer can populate kubesense_config_violations table.
	if chaosPub != nil { // reuse the same kafkapub connection
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			// Run once immediately on startup
			configscan.Scan(*clusterID, factory.ListDeployments(), chaosPub)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					configscan.Scan(*clusterID, factory.ListDeployments(), chaosPub)
				}
			}
		}()
		log.Printf("kubesense-agent: config violation scanner started for cluster=%s", *clusterID)
	}

	<-ctx.Done()
	log.Println("kubesense-agent: shutting down")
	if pub != nil {
		pub.Close()
	}
}

// serveHealth provides /healthz (liveness) and /readyz (readiness) endpoints.
// /readyz blocks until readyCh is closed (cache synced).
func serveHealth(addr string, readyCh <-chan struct{}) {
	ready := false
	go func() {
		<-readyCh
		ready = true
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if ready {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ready"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("syncing"))
		}
	})

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("kubesense-agent health server: %v", err)
	}
}

func makeEvent(t events.EventType, clusterID, kind, namespace, name, uid string, labels map[string]string) *events.IntelligenceEvent {
	return &events.IntelligenceEvent{
		Type:      t,
		Severity:  events.SeverityInfo,
		ClusterID: clusterID,
		Resource: events.ResourceRef{
			Kind:      kind,
			Namespace: namespace,
			Name:      name,
			UID:       uid,
			Labels:    labels,
		},
	}
}

func buildK8sClient(kubeconfig string) kubernetes.Interface {
	var cfg *rest.Config
	var err error
	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		log.Fatalf("kubesense-agent: k8s config: %v", err)
	}
	cfg.QPS = 50
	cfg.Burst = 100
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("kubesense-agent: k8s client: %v", err)
	}
	return client
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
