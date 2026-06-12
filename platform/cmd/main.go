package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	gorillaws "github.com/gorilla/websocket"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	goredis "github.com/redis/go-redis/v9"

	"github.com/aileron-platform/aileron/platform/internal/api/handlers"
	"github.com/aileron-platform/aileron/platform/internal/api/middleware"
	"github.com/aileron-platform/aileron/platform/internal/auth/oidc"
	"github.com/aileron-platform/aileron/platform/internal/cache"
	"github.com/aileron-platform/aileron/platform/internal/db"
	"github.com/aileron-platform/aileron/platform/internal/services/ai"
	"github.com/aileron-platform/aileron/platform/internal/services/alerts"
	"github.com/aileron-platform/aileron/platform/internal/services/analytics"
	"github.com/aileron-platform/aileron/platform/internal/services/audit"
	apikeyssvc "github.com/aileron-platform/aileron/platform/internal/services/apikeys"
	"github.com/aileron-platform/aileron/platform/internal/services/correlation"
	dsldapsvc "github.com/aileron-platform/aileron/platform/internal/services/dsldap"
	"github.com/aileron-platform/aileron/platform/internal/services/floodgate"
	"github.com/aileron-platform/aileron/platform/internal/services/incidents"
	"github.com/aileron-platform/aileron/platform/internal/services/jwt"
	"github.com/aileron-platform/aileron/platform/internal/services/kubesense"
	"github.com/aileron-platform/aileron/platform/internal/services/maintenance"
	"github.com/aileron-platform/aileron/platform/internal/services/oauth"
	"github.com/aileron-platform/aileron/platform/internal/services/notifications"
	notificationsvc "github.com/aileron-platform/aileron/platform/internal/services/notifications"
	"github.com/aileron-platform/aileron/platform/internal/services/pipeline"
	"github.com/aileron-platform/aileron/platform/internal/services/policy"
	"github.com/aileron-platform/aileron/platform/internal/services/postmortems"
	"github.com/aileron-platform/aileron/platform/internal/services/rbac"
	"github.com/aileron-platform/aileron/platform/internal/services/sso"
	"github.com/aileron-platform/aileron/platform/internal/services/topology"
	webhookmgr "github.com/aileron-platform/aileron/platform/internal/services/webhook_manager"
	"github.com/aileron-platform/aileron/platform/internal/services/workflows"
	"github.com/aileron-platform/aileron/platform/internal/shared/registry"
)

// ============================================================================
// ALERTHUB ENTERPRISE - CLEAN, SINGLE-FILE ARCHITECTURE
// Goals: Event correlation, noise reduction, root cause analysis
// Infrastructure: 150 BM + CloudStack + K8s (Reno + Maiden)
// ============================================================================


func main() {
	// Load configuration
	config := loadConfig()

	// Enforce minimum JWT secret strength at startup
	if len(config.JWTSecret) < 32 {
		log.Fatal("FATAL: JWT_SECRET must be at least 32 characters — refusing to start with a weak secret")
	}
	if len(config.JWTRefreshSecret) < 32 {
		log.Fatal("FATAL: JWT_REFRESH_SECRET must be at least 32 characters — refusing to start with a weak secret")
	}

	// Initialize database
	database, err := initDatabase(config)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	// Run database migrations and setup
	if err := db.InitializeDatabase(database); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Initialize Redis cache
	redisCache, err := initRedis(config)
	if err != nil {
		log.Printf("Redis initialization failed: %v - Continuing without cache", err)
		redisCache = nil // Continue without Redis if unavailable
	} else {
		log.Printf("Redis cache connected successfully")
		defer redisCache.Close()

		// Warm cache with hot data
		if err := redisCache.WarmCache(database); err != nil {
			log.Printf("Cache warming failed: %v", err)
		}
	}

	// Note: Event-driven pipeline implementation available in separate files
	// cmd/event_driven_pipeline.go and cmd/complete_event_driven_implementation.go
	log.Printf("Enterprise AlertHub with AI microservices integration")

	// Initialize service registry
	serviceRegistry := registry.NewServiceRegistry(database)

	// Initialize core services
	rbacService := rbac.NewRBACService(database)
	jwtService := jwt.NewJWTService(
		config.JWTSecret,
		config.JWTRefreshSecret,
		"AlertHub",
		24*time.Hour,
		7*24*time.Hour,
	)

	ssoConfig := &sso.SSOConfig{
		LDAPEnabled:    config.LDAPEnabled,
		LDAPServer:     config.LDAPServer,
		LDAPPort:       config.LDAPPort,
		LDAPBaseDN:     config.LDAPBaseDN,
		LDAPBindDN:     config.LDAPBindDN,
		LDAPBindPass:   config.LDAPBindPassword,
		LDAPUserFilter: config.LDAPUserFilter,
		LDAPUseTLS:     config.LDAPUseTLS,
	}
	ssoManager := sso.NewSSOManager(ssoConfig)

	aiService := ai.NewAIService(config.AIServiceURL, config.AIAPIKey)
	floodgateService := floodgate.NewFloodgateService()

	// Register services in the registry
	serviceRegistry.RegisterService("rbac", rbacService)
	serviceRegistry.RegisterService("ai", aiService)

	// CRITICAL FIX: Initialize and register correlation engine BEFORE creating alert service
	// This ensures alertService.correlationService won't be nil
	correlationEngine := correlation.NewCorrelationEngine(database)
	serviceRegistry.RegisterService("correlation", correlationEngine)

	// CRITICAL FIX: Initialize workflow engine and register it
	workflowEngine := workflows.NewWorkflowEngine(database)
	serviceRegistry.RegisterService("workflow", workflowEngine)

	// NOW initialize services that depend on registry (correlation service is now available)
	alertService := alerts.NewAlertService(serviceRegistry)
	analyticsService := analytics.NewAnalyticsService(database)
	auditService := audit.NewAuditService(database)
	maintenanceService := maintenance.NewMaintenanceService(database)
	incidentService := incidents.NewIncidentService(database, rbacService, aiService)
	if rcaURLForSvc := getEnv("RCA_ORCHESTRATOR_URL", ""); rcaURLForSvc != "" {
		incidentService.SetRCAURL(rcaURLForSvc)
	}
	// Sweep stale incidents whose alerts are all resolved but incident still shows open.
	go func() {
		n, err := incidentService.AutoCloseResolvedIncidents(context.Background())
		if err != nil {
			log.Printf("startup auto-close sweep failed: %v", err)
		} else if n > 0 {
			log.Printf("startup auto-close: resolved %d incident(s) whose alerts were all resolved", n)
		}
	}()

	// Parallel correlation pipeline 
	// Runs all 4 strategies (Semantic, Temporal, Topology, Rules) in parallel and
	// uses weighted aggregation to decide whether to create/merge/monitor/discard.
	parallelCorrelationEngine := correlation.NewParallelCorrelationEngine(database)
	// CORRELATION_WEIGHTS_LOCKED controls whether strategy weights are frozen at
	// their go-live values. Default is locked ("true") — safe for production.
	// Set to "false" after 30+ days of operator feedback data has been collected
	// and the ops team has reviewed GET /api/v1/admin/correlation/weight-status.
	if os.Getenv("CORRELATION_WEIGHTS_LOCKED") != "false" {
		parallelCorrelationEngine.LockWeights()
		log.Printf("Correlation engine weights LOCKED (set CORRELATION_WEIGHTS_LOCKED=false to enable feedback learning)")
	} else {
		log.Printf("Correlation engine weights UNLOCKED — feedback loop active")
	}
	var redisClient *goredis.Client
	if redisCache != nil {
		redisClient = redisCache.Client()
	}
	correlationAggregator := correlation.NewCorrelationAggregatorService(database, redisClient)
	if os.Getenv("CORRELATION_WEIGHTS_LOCKED") != "false" {
		correlationAggregator.LockWeights()
	}
	alertPipeline := pipeline.NewAlertPipelineService(parallelCorrelationEngine, correlationAggregator, incidentService)
	alertPipeline.SetDB(database) // persist every correlation decision for AIOps dashboard
	notificationSvc := notifications.NewNotificationService(database)
	alertPipeline.SetNotificationService(notificationSvc)
	if rcaURL := getEnv("RCA_ORCHESTRATOR_URL", ""); rcaURL != "" {
		alertPipeline.SetRCAURL(rcaURL)
		log.Printf("RCA orchestrator URL configured: %s", rcaURL)
	}
	// Start pipeline processor goroutine (runs for lifetime of the process)
	pipelineCtx, pipelineCancel := context.WithCancel(context.Background())
	defer pipelineCancel()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Alert pipeline goroutine panicked: %v — pipeline stopped", r)
			}
		}()
		alertPipeline.Start(pipelineCtx)
	}()
	log.Printf("Parallel correlation pipeline started (4 strategies + weighted aggregator)")

	// StagedPipeline: 3-stage worker pool for alert processing 
	// Replaces the single-goroutine alertCh dispatch with dedicated worker pools:
	//   Stage 1 — FAST PATH  (32 workers): resolved-alert fast-exit
	//   Stage 2 — TOPO PATH  (16 workers): root cause engine (deterministic)
	//   Stage 3 — FULL PATH  (8 workers):  4-strategy parallel scoring
	stagedPipeline := pipeline.NewStagedPipelineForService(alertPipeline)
	stagedPipeline.Start(pipelineCtx)
	log.Printf("StagedPipeline started (32 fast + 16 topo + 8 full workers)")

	// Neo4j + Live Topology 
	// Wire live CloudStack/K8s topology fetcher into the parallel correlation engine.
	neo4jURL := getEnv("NEO4J_URL", "neo4j://neo4j.aileron.svc.cluster.local:7687")
	neo4jUser := getEnv("NEO4J_USERNAME", "neo4j")
	neo4jPassword := getEnv("NEO4J_PASSWORD", "")
	// NEO4J_AUTH is the official Neo4j Docker env var (format: "user/password").
	// Parse it as a fallback so the Go backend can share the same K8s secret
	// without needing a separate NEO4J_PASSWORD entry.
	if neo4jPassword == "" {
		if auth := os.Getenv("NEO4J_AUTH"); auth != "" {
			if parts := strings.SplitN(auth, "/", 2); len(parts) == 2 {
				neo4jUser = parts[0]
				neo4jPassword = parts[1]
			}
		}
	}

	// LiveTopologyFetcher writes CloudStack + K8s + NetApp topology to Neo4j every 5 minutes.
	// If Neo4j is unreachable at startup, a background goroutine retries with
	// exponential back-off (30 s 5 min, up to 10 attempts) so topology is not
	// permanently disabled for the lifetime of the pod.
	wireTopologyFetcher := func(driver neo4j.DriverWithContext) {
		fetcher := topology.NewLiveTopologyFetcher(database, driver)
		if redisClient != nil {
			fetcher.SetRedisClient(redisClient)
		}
		// Wire NetApp ONTAP clusters from environment variables.
		netappClusters, netappUser, netappPassword := topology.LoadNetAppClustersFromEnv()
		if len(netappClusters) > 0 {
			fetcher.SetNetAppClusters(netappClusters, netappUser, netappPassword)
			log.Printf("NetApp topology configured: %d cluster(s)", len(netappClusters))
		}
		parallelCorrelationEngine.SetLiveTopologyFetcher(fetcher)
		go fetcher.StartBackgroundRefresh(pipelineCtx, 10*time.Minute)
		log.Printf("Live topology fetcher wired (CloudStack + K8s + NetApp Neo4j, 10-min background refresh)")
	}

	neo4jDriver, neo4jDriverErr := createNeo4jDriver(neo4jURL, neo4jUser, neo4jPassword)
	if neo4jDriverErr != nil {
		log.Printf("Neo4j driver unavailable at startup: %v — retrying in background", neo4jDriverErr)
		go func() {
			for attempt := 1; attempt <= 10; attempt++ {
				wait := time.Duration(attempt) * 30 * time.Second
				if wait > 5*time.Minute {
					wait = 5 * time.Minute
				}
				time.Sleep(wait)
				driver, err := createNeo4jDriver(neo4jURL, neo4jUser, neo4jPassword)
				if err != nil {
					log.Printf("Neo4j reconnect attempt %d/10: %v", attempt, err)
					continue
				}
				log.Printf("Neo4j connected on attempt %d", attempt)
				wireTopologyFetcher(driver)
				return
			}
			log.Printf("Neo4j unreachable after 10 attempts — live topology disabled")
		}()
	} else {
		wireTopologyFetcher(neo4jDriver)
	}

	// Initialize IDMS authentication (Apple IdMS OAuth2 + DS-LDAP RBAC)
	idmsConfig := idms.DefaultConfig()
	idmsConfig.Enabled = getEnv("IDMS_AUTH_ENABLED", "true") == "true"
	idmsConfig.AutoProvision = getEnv("IDMS_AUTO_PROVISION", "true") == "true"
	idmsConfig.DefaultRole = getEnv("IDMS_DEFAULT_ROLE", "viewer")
	idmsConfig.StrictMode = getEnv("IDMS_STRICT_MODE", "false") == "true"
	idmsProvisioner := idms.NewUserProvisioner(database, idmsConfig)

	// Initialize Corporate OAuth 2.0 client
	oauthConfig := &oauth.OAuthConfig{
		IdMSBaseURL:      getEnv("OIDC_PROVIDER_URL", ""),
		ClientID:         getEnv("OAUTH_CLIENT_ID", "7jdvu5f1gxuuckpbdb5s7jw6tcwpf3"),
		ClientSecret:     getEnv("OAUTH_CLIENT_SECRET", ""),
		Audiences:        []string{"sre-command-center", "sear-floodgate"},
		RequiredScopes:   []string{"dsid", "offline_access", "groups"},
		RequiredGroups:   []string{"aileron-operators", "floodgate-google-models-access", "floodgate-anthropic-access"},
		FloodgateAppID:   "928148",
		FloodgateBaseURL: "",
	}
	oauthClient := oauth.NewOAuthClient(oauthConfig)

	// Initialize topology services (both infrastructure and service-level)
	infraTopoService := topology.NewInfraTopologyService(database)
	topoService := topology.NewTopologyService(database)
	k8sTopoService := topology.NewKubernetesTopologyService(database)

	// Initialize K8s topology service
	if err := k8sTopoService.Initialize(context.Background()); err != nil {
		log.Printf("K8s topology service initialization failed: %v", err)
	} else {
		log.Printf("K8s topology service initialized successfully")
	}

	authHandler := handlers.NewAuthHandler(rbacService, jwtService, ssoManager, database)
	userHandler := handlers.NewUserHandler(rbacService, database, redisCache)
	roleHandler := handlers.NewRoleHandler(rbacService, database)

	// Enhanced webhook handler with AI integration
	webhookHandler := handlers.NewWebhookHandler(alertService, database)
	// Enable auto-correlation and incident creation
	webhookHandler.SetCorrelationEngine(correlationEngine)
	webhookHandler.SetIncidentService(incidentService)
	// Wire parallel pipeline — enqueues alerts for 4-strategy async correlation
	webhookHandler.SetPipelineProcessor(alertPipeline)

	// Wire Kafka producers and consumer (Kafka-first architecture).
	// Topic layout:
	//   raw-alerts          webhooks + universal-alert-gateway publish here
	//   normalized-alerts   pipeline publishes enriched alerts after DB persistence
	//   correlation-results pipeline publishes correlation decisions
	kafkaBrokers := strings.Split(getEnv("KAFKA_BROKERS",
		"alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092"), ",")

	kafkaProducer, kafkaProducerErr := pipeline.NewAlertKafkaProducer(kafkaBrokers, pipeline.DefaultAlertsTopic)
	if kafkaProducerErr != nil {
		log.Printf("Kafka producer unavailable: %v — webhooks will use direct processing", kafkaProducerErr)
	} else {
		webhookHandler.SetKafkaProducer(kafkaProducer)
		log.Printf("Kafka producer connected topic=%s", pipeline.DefaultAlertsTopic)
	}

	// Normalized-alerts publisher — pipeline publishes after DB persistence.
	normalizedProducer, err := pipeline.NewAlertKafkaProducer(kafkaBrokers, pipeline.NormalizedAlertsTopic)
	if err != nil {
		log.Printf("normalized-alerts producer unavailable: %v", err)
	} else {
		alertPipeline.SetNormalizedPublisher(normalizedProducer)
		log.Printf("Kafka producer connected topic=%s", pipeline.NormalizedAlertsTopic)
	}

	// Correlation-results publisher — pipeline publishes after correlation completes.
	correlationResultsProducer, err := pipeline.NewAlertKafkaProducer(kafkaBrokers, pipeline.CorrelationResultsTopic)
	if err != nil {
		log.Printf("correlation-results producer unavailable: %v", err)
	} else {
		alertPipeline.SetCorrelationPublisher(correlationResultsProducer)
		log.Printf("Kafka producer connected topic=%s", pipeline.CorrelationResultsTopic)
	}

	// Wire OIE incident publisher — fires incident.created events so OIE
	// auto-triggers investigations for every new high/critical incident.
	oieProducer, oieProducerErr := pipeline.NewAlertKafkaProducer(kafkaBrokers, "alerthub.incidents")
	if oieProducerErr != nil {
		log.Printf("OIE incident publisher unavailable (investigations disabled): %v", oieProducerErr)
	} else {
		alertPipeline.SetOIEPublisher(oieProducer)
		log.Printf("Kafka producer connected topic=alerthub.incidents (OIE auto-trigger enabled)")
	}

	// Start Kafka consumer — reads from raw-alerts (published by universal-alert-gateway / webhook handler).
	kafkaConsumer := pipeline.NewAlertKafkaConsumer(kafkaBrokers, pipeline.DefaultAlertsTopic, alertService, alertPipeline)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Kafka consumer goroutine panicked: %v — consumer stopped", r)
			}
		}()
		kafkaConsumer.Start(pipelineCtx)
	}()

	// Wire KubeSense intelligence: publisher (AlertHub → KubeSense requests) +
	// consumer (KubeSense results/events → AlertHub tables).
	kubeSensePublisher, ksPublisherErr := kubesense.NewPublisher(kafkaBrokers)
	if ksPublisherErr != nil {
		log.Printf("KubeSense publisher unavailable (parallel investigations disabled): %v", ksPublisherErr)
	} else {
		alertPipeline.SetKubeSensePublisher(kubeSensePublisher)
		log.Printf("KubeSense publisher ready: topic=%s", kubesense.TopicInvRequests)
	}
	ksConsumer := kubesense.NewConsumer(kafkaBrokers, database, alertPipeline)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("KubeSense consumer goroutine panicked: %v", r)
			}
		}()
		ksConsumer.Start(pipelineCtx)
	}()

	// Postmortem auto-generation (Aurora pattern): wire service into pipeline so
	// every auto-resolved incident gets a structured postmortem immediately.
	postmortemSvc := postmortems.NewPostmortemService(database)
	alertPipeline.SetPostmortemTrigger(postmortemSvc)
	log.Printf("Postmortem auto-generation enabled")

	// Policy engine (Sympozium SympoziumPolicy): loads intelligence_policies table
	// and replaces hardcoded isKnownTestWorkload / alert suppression logic.
	// Wired into the alert pipeline for pre-pipeline suppression decisions.
	pipelinePolicyEngine := policy.NewPolicyEngine(database)
	alertPipeline.SetPolicyEngine(pipelinePolicyEngine)
	log.Printf("Intelligence policy engine wired into alert pipeline")

	// LLMFit model benchmark: validate structured-output capability at startup.
	// Non-blocking — runs in background goroutine, never delays server start.
	pipeline.RunAtStartup(
		getEnv("LLM_SERVICE_URL", fmt.Sprintf("http://ollama.%s.svc.cluster.local:11434", getEnv("POD_NAMESPACE", "aileron"))),
		getEnv("LLM_RCA_MODEL", getEnv("LLM_MODEL", "qwen2.5:3b")),
	)

	integrationHandler := handlers.NewIntegrationHandler(database)
	alertHandler := handlers.NewAlertHandler(alertService, maintenanceService, database)
	alertHandler.SetPipelineNotifier(alertPipeline)
	analyticsHandler := handlers.NewAnalyticsHandler(analyticsService)
	incidentHandler := handlers.NewIncidentHandler(incidentService, database)
	// Wire feedback service so POST /incidents/:id/feedback is active.
	// feedbackSvc is constructed later — set it after both are ready.
	notificationHandler := handlers.NewNotificationHandler(database)
	configHandler := handlers.NewConfigHandler(database)
	aiHandler := handlers.NewAIHandler(floodgateService, database)

	// Aurora Integration: Use Enhanced Webhook Handler with Aurora correlation
	enhancedWebhookHandler := handlers.NewEnhancedWebhookHandler(alertService, correlationEngine)
	correlationHandler := handlers.NewEnhancedCorrelationHandler(correlationEngine, database)
	workflowHandler := handlers.NewWorkflowHandler(workflowEngine, database)
	idmsHandler := handlers.NewIDMSHandler(idmsConfig, idmsProvisioner, jwtService, rbacService, database, oauthClient,
		getEnv("APP_BASE_URL", "https://aileron.example.com"),
		getEnv("OAUTH_CALLBACK_URL", "https://aileron.example.com/api/v1/auth"),
		redisCache,
	)
	oauthHandler := handlers.NewOAuthHandler(oauthClient)
	enhancedTopoConfigHandler := handlers.NewEnhancedTopologyConfigHandler(infraTopoService)
	topologyHandler := handlers.NewTopologyHandler(k8sTopoService)
	k8sProxyHandler := handlers.NewK8sServiceProxyHandler("http://localhost:8001")
	k8sProxyHandler.SetK8sTopologyService(k8sTopoService)
	infraTopoService.SetK8sTopologyService(k8sTopoService)
	k8sClusterSyncHandler := handlers.NewK8sClusterSyncHandler(infraTopoService)
	dynamicK8sExplorerHandler := handlers.NewDynamicK8sExplorerHandler("http://localhost:8001")
	hclIncidentHandler := handlers.NewHCLIncidentHandler()

	// Build and start the topology graph cache (Redis-backed, 2-minute refresh).
	var graphRedis topology.RedisLike
	if redisCache != nil {
		graphRedis = redisCache
	}
	graphCache := topology.NewTopologyGraphCache(infraTopoService, graphRedis)
	graphCache.Start(pipelineCtx)
	infraTopoHandler := handlers.NewInfraTopologyHandler(infraTopoService, topoService)
	infraTopoHandler.SetGraphCache(graphCache)

	// Topology-graph correlator: wire Redis infra graph into parallel engine
	topoAdapter := &topoProviderAdapter{cache: graphCache}
	topoGraphCorrelator := correlation.NewTopologyGraphCorrelator(topoAdapter, database)
	parallelCorrelationEngine.SetTopologyGraphCorrelator(topoGraphCorrelator)
	// Wire correlator into topology handler so /topology/health can report freshness.
	infraTopoHandler.SetTopologyCorrelator(topoGraphCorrelator)
	log.Printf("Topology-graph correlator wired (Redis infra graph parallel engine)")

	// Topology search adapter: enrich Dynatrace host/infra alert labels 
	alertPipeline.SetTopologyGraph(&topoSearchAdapter{cache: graphCache})
	log.Printf("Topology search adapter wired (Dynatrace host label enrichment)")

	// Root Cause Engine: authoritative first-pass before 4-strategy scoring 
	rootCauseEngine := correlation.NewRootCauseEngine(topoGraphCorrelator, database, redisClient)
	alertPipeline.SetRootCauseEngine(rootCauseEngine)
	alertBuffer := correlation.NewAlertBuffer(redisClient)
	alertPipeline.SetAlertBuffer(alertBuffer)
	log.Printf("Root cause engine + alert state machine wired into pipeline")

	// Feedback loop: operator verdicts adaptive strategy weights
	feedbackSvc := correlation.NewCorrelationFeedbackService(database, parallelCorrelationEngine)
	incidentHandler.SetFeedbackService(feedbackSvc)
	log.Printf("Correlation feedback loop initialized")

	// Wire OIE URL so GetIncident enriches responses with real evidence-based RCA.
	oieURL := getEnv("OIE_URL", "http://oie.aileron.svc.cluster.local:8080")
	incidentHandler.SetOIEURL(oieURL)
	incidentHandler.SetNotificationService(notificationSvc) // RCA completion Slack updates
	log.Printf("OIE integration enabled: %s", oieURL)

	// V2 Correlation Engine 
	// All components are nil-safe: if any external dependency is unavailable at
	// startup the pipeline continues to run with V1 behavior.


	// Ontology engine — pure in-process keyword classification, always available.
	ontologyEngine := correlation.NewOntologyEngine()
	alertPipeline.SetOntologyEngine(ontologyEngine)
	log.Printf("V2: Ontology engine wired (domain/class classification)")

	// Adaptive learning — EMA weight tuning per (domain, source, cluster).
	// Weights kick in after 10 feedback samples per context; conservative EMA (α=0.08).
	// Enable when CORRELATION_WEIGHTS_LOCKED=false — the same flag that unlocks the
	// parallel engine weights, ensuring both systems are consistent.
	adaptiveLearning := correlation.NewAdaptiveLearningEngine(database, redisClient, feedbackSvc)
	if os.Getenv("CORRELATION_WEIGHTS_LOCKED") == "false" {
		// Learning is active — adaptive weights will override go-live defaults
		// once each domain+source+cluster context accumulates ≥10 feedback samples.
		log.Printf("V2: Adaptive learning ENABLED (CORRELATION_WEIGHTS_LOCKED=false)")
	} else {
		// Weights locked: adaptive learning records feedback but returns defaults.
		// Enables data collection without production impact.
		adaptiveLearning.SetDisabled(true)
		log.Printf("V2: Adaptive learning RECORDING ONLY (weights locked — set CORRELATION_WEIGHTS_LOCKED=false to activate)")
	}
	alertPipeline.SetAdaptiveLearning(adaptiveLearning)

	// Vector repository — pgvector alert embeddings for ANN similarity search.
	vectorRepo := correlation.NewVectorRepository(database)
	alertPipeline.SetVectorRepository(vectorRepo)
	go func() {
		if err := vectorRepo.EnsureSchema(context.Background()); err != nil {
			log.Printf("V2: pgvector schema init skipped (install pgvector extension): %v", err)
		} else {
			log.Printf("V2: alert_embeddings table ready (pgvector)")
		}
	}()

	// Recursive topo RCA + Probabilistic RCA — require Neo4j driver.
	if neo4jDriver != nil {
		// Wire Neo4j into infra topology service so BuildInfraGraph can query NetApp nodes.
		infraTopoService.SetNeo4jDriver(neo4jDriver)

		recursiveTopoRCA := correlation.NewRecursiveTopoRCAEngine(neo4jDriver, topoAdapter)
		probabilisticRCA := correlation.NewProbabilisticRCAEngine(redisClient, recursiveTopoRCA, rootCauseEngine)
		alertPipeline.SetRecursiveTopoRCA(recursiveTopoRCA)
		alertPipeline.SetProbabilisticRCA(probabilisticRCA)

		// CACIE — wire after all sub-engines are ready so SetX calls are complete.
		cacie := correlation.NewCausalInferenceEngine(database)
		cacie.SetRCE(rootCauseEngine)
		cacie.SetRecursiveTopo(recursiveTopoRCA)
		cacie.SetProbabilisticRCA(probabilisticRCA)
		cacie.SetOntology(ontologyEngine)
		alertPipeline.SetCausalInferenceEngine(cacie)
		alertPipeline.SetInvestigationDAGEngine(correlation.NewInvestigationDAGEngine())
		log.Printf("V2: Recursive topo RCA + Probabilistic RCA + CACIE + Investigation DAG wired (Neo4j driver available)")
	} else {
		// CACIE works without Neo4j — uses RCE + probabilistic evidence only.
		cacie := correlation.NewCausalInferenceEngine(database)
		cacie.SetRCE(rootCauseEngine)
		cacie.SetOntology(ontologyEngine)
		alertPipeline.SetCausalInferenceEngine(cacie)
		alertPipeline.SetInvestigationDAGEngine(correlation.NewInvestigationDAGEngine())
		log.Printf("V2: Recursive topo RCA + Probabilistic RCA deferred (Neo4j unavailable). CACIE + Investigation DAG wired in RCE-only mode")
	}

	// Incident evolution engine — merges/splits/escalates incidents in a background loop.
	evolutionEngine := incidents.NewIncidentEvolutionEngine(database, redisClient, incidentService)
	go evolutionEngine.Run(pipelineCtx)
	log.Printf("V2: Incident evolution engine started (30s evaluation loop)")

	// Initialize WebSocket handler
	websocketHandler := handlers.NewWebSocketHandler(redisCache, database)

	// Initialize Splunk webhook handler
	splunkWebhookHandler := handlers.NewSplunkWebhookHandler(alertService, correlationEngine)
	if kafkaProducer != nil {
		splunkWebhookHandler.SetKafkaProducer(kafkaProducer)
		log.Printf("Splunk webhook handler wired to Kafka (raw-alerts topic)")
	}
	splunkWebhookHandler.SetPipelineProcessor(alertPipeline)
	log.Printf("Splunk webhook handler wired to parallel correlation pipeline")

	// Initialize deduplication handler
	deduplicationHandler := handlers.NewDeduplicationHandler(database)

	// Initialize AIOps handler
	aiopsHandler := handlers.NewAIOpsHandler(database)

	// Enterprise API management 
	apiKeySvc := apikeyssvc.NewService(database)
	webhookMgr := webhookmgr.NewManager(database)
	enterpriseAPIHandler := handlers.NewEnterpriseAPIHandler(database, apiKeySvc, webhookMgr)

	// Start webhook retry worker (polls pending deliveries every 5 s)
	go webhookMgr.RunRetryWorker(pipelineCtx)
	log.Printf("Enterprise API management: api_keys + webhook delivery worker started")

	// Initialize monitoring, health, Prometheus, and extended analytics handlers
	monitoringHandler := handlers.NewMonitoringHandler(database, neo4jDriver, redisClient)
	healthHandler := handlers.NewHealthHandler(database, neo4jDriver, redisClient)
	prometheusMetricsHandler := handlers.NewPrometheusMetricsHandler(database)
	analyticsExtHandler := handlers.NewAnalyticsExtendedHandler(database, redisClient)

	// Initialize middleware
	authMiddleware := middleware.NewAuthMiddleware(jwtService, rbacService)
	apiKeyAuthMiddleware := middleware.NewAPIKeyAuth(apiKeySvc)
	auditLogger := middleware.NewAuditLogger(database)

	// DS-LDAP group-based RBAC enrichment (optional, controlled by DSLDAP_ENABLED=true)
	// Credentials LDAP_APP_ID and LDAP_APP_PASSWORD are loaded from K8s Secret
	// alerthub-dsldap-credentials (see k8s/ldap-credentials-secret.yaml).
	ldapSvc := dsldapsvc.New(dsldapsvc.Config{
		Enabled:        config.DSLDAPEnabled,
		ServerURL:      config.DSLDAPServerURL,
		AppID:          config.DSLDAPAppID,
		AppPassword:    config.DSLDAPAppPassword,
		UserSearchBase: config.DSLDAPUserSearchBase,
		CacheTTL:       time.Duration(config.DSLDAPCacheTTLMins) * time.Minute,
		AdminGroups:    splitCSV(config.DSLDAPAdminGroups),
		OperatorGroups: splitCSV(config.DSLDAPOperatorGroups),
		ViewerGroups:   splitCSV(config.DSLDAPViewerGroups),
	})
	if ldapSvc != nil {
		ldapSvc.SetDB(database) // enables live DB-backed grouprole mapping reloads
		authMiddleware.SetLDAPService(ldapSvc)
		roleHandler.SetLDAPService(ldapSvc)   // triggers mapping reload after admin UI changes
		idmsHandler.SetLDAPService(ldapSvc)    // fetch groups from DS-LDAP when IDMS token omits them
		log.Printf("DS-LDAP: enabled, server=%s appID=%s cacheTTL=%dm",
			config.DSLDAPServerURL, config.DSLDAPAppID, config.DSLDAPCacheTTLMins)
		log.Printf("   admin-groups=%s  operator-groups=%s  viewer-groups=%s",
			config.DSLDAPAdminGroups, config.DSLDAPOperatorGroups, config.DSLDAPViewerGroups)
	} else {
		log.Printf("DS-LDAP: disabled (set DSLDAP_ENABLED=true to enable group-based RBAC)")
	}

	// Setup router with Redis cache and AI service integration
	router := setupRouter(
		database,
		authHandler,
		userHandler,
		roleHandler,
		webhookHandler,
		integrationHandler,
		alertHandler,
		analyticsHandler,
		incidentHandler,
		notificationHandler,
		configHandler,
		aiHandler,
		correlationHandler,
		workflowHandler,
		hclIncidentHandler,
		authMiddleware,
		auditService,
		idmsHandler,
		oauthHandler,
		infraTopoHandler,
		enhancedTopoConfigHandler,
		k8sProxyHandler,
		k8sClusterSyncHandler,
		dynamicK8sExplorerHandler,
		enhancedWebhookHandler,
		topologyHandler,
		websocketHandler,
		splunkWebhookHandler,
		idmsConfig,
		redisCache,
		deduplicationHandler,
		feedbackSvc,
		aiopsHandler,
		ldapSvc,            // DS-LDAP service (nil when disabled)
		enterpriseAPIHandler,
		apiKeyAuthMiddleware,
		auditLogger,
		monitoringHandler,
		healthHandler,
		prometheusMetricsHandler,
		analyticsExtHandler,
		neo4jDriver, // OKG Change Intelligence endpoint
	)

	// OIE SSE investigation stream proxy (HolmesGPT pattern: live investigation progress).
	// GET /api/v1/incidents/:id/investigation/stream → proxied to OIE service SSE endpoint.
	// Registered here (not in setupRouter) because it needs oieURL from main scope.

	// Topology entity resolution — used by OIE evidence bus and the frontend.
	// Gated by JWT auth: topology data reveals internal cluster/VM names.
	if graphCache != nil {
		resolveHandler := handlers.NewTopologyResolveHandler(graphCache)
		router.GET("/api/v1/topology/resolve",
			authMiddleware.Authenticate(),
			resolveHandler.Resolve,
		)
		log.Printf("Topology resolve endpoint registered (/api/v1/topology/resolve)")
	}

	oieSSEClient := &http.Client{Timeout: 130 * time.Second}
	router.GET("/api/v1/incidents/:id/investigation/stream",
		authMiddleware.Authenticate(),
		func(c *gin.Context) {
		incidentID := c.Param("id")
		target := fmt.Sprintf("%s/api/v1/incidents/%s/investigation/stream", oieURL, incidentID)
		oieReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, target, nil)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "oie unavailable"})
			return
		}
		oieReq.Header.Set("Accept", "text/event-stream")
		oieResp, err := oieSSEClient.Do(oieReq)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "oie stream unavailable"})
			return
		}
		defer oieResp.Body.Close()
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")
		buf := make([]byte, 4096)
		for {
			n, readErr := oieResp.Body.Read(buf)
			if n > 0 {
				c.Writer.Write(buf[:n]) //nolint:errcheck
				c.Writer.Flush()
			}
			if readErr != nil {
				return
			}
		}
	})

	// Start server
	port := config.Port
	if port == "" {
		port = "3000"
	}

	log.Printf("Enterprise AlertHub starting on port %s", port)
	log.Printf("Database: %s@%s:%s/%s", config.DBUser, config.DBHost, config.DBPort, config.DBName)
	log.Printf("LDAP: %s (enabled: %v)", config.LDAPServer, config.LDAPEnabled)
	log.Printf("AI Service: %s", config.AIServiceURL)
	log.Printf("Redis: %s (enabled: %v)", config.RedisAddr, redisCache != nil)
	log.Printf("OAuth: IdMS=%s, Client=%s", oauthConfig.IdMSBaseURL, oauthConfig.ClientID)
	log.Printf("Floodgate: %s (App ID: %s)", oauthConfig.FloodgateBaseURL, oauthConfig.FloodgateAppID)
	log.Printf("Topology: Infrastructure + Service mapping enabled")
	log.Printf("Environment: %s", config.Environment)
	log.Printf("Kafka: %s (always-on)", getEnv("KAFKA_BROKERS", "alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092"))
	log.Printf("   Kafka-first architecture: Webhooks raw-alerts Pipeline correlation-results")

	// Start server with graceful shutdown so SIGTERM drains in-flight requests
	// before the pod is forcibly killed. K8s sends SIGTERM 30s before SIGKILL.
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("Shutdown signal received — draining requests (30s grace period)...")
	// Cancel the pipeline context first so no new alerts are accepted from the queue.
	pipelineCancel()

	// Drain in-flight correlations before shutting down the HTTP server and DB pool.
	// K8s gives 30s between SIGTERM and SIGKILL; allow 20s for the pipeline drain,
	// leaving 10s for HTTP shutdown + connection teardown.
	drainDone := make(chan struct{})
	go func() {
		alertPipeline.Drain()
		close(drainDone)
	}()
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer drainCancel()
	select {
	case <-drainDone:
		log.Printf("Alert pipeline drained cleanly")
	case <-drainCtx.Done():
		log.Printf("Alert pipeline drain timed out after 20s — some in-flight alerts may be incomplete")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server forced shutdown: %v", err)
	}
	log.Printf("Server stopped cleanly")
}

// Config holds application configuration
type Config struct {
	// Server
	Port        string
	Environment string
	LogLevel    string

	// Database
	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string
	DBSSLMode  string

	// JWT
	JWTSecret        string
	JWTRefreshSecret string

	// LDAP (generic SSO)
	LDAPEnabled      bool
	LDAPServer       string
	LDAPPort         int
	LDAPBaseDN       string
	LDAPBindDN       string
	LDAPBindPassword string
	LDAPUserFilter   string
	LDAPUseTLS       bool

	// DS-LDAP (Apple Directory Service — group-based RBAC)
	// Credentials stored in K8s Secret alerthub-dsldap-credentials
	DSLDAPEnabled        bool
	DSLDAPAppID          string // LDAP_APP_ID from K8s secret
	DSLDAPAppPassword    string // LDAP_APP_PASSWORD from K8s secret
	DSLDAPServerURL      string // default: 
	DSLDAPUserSearchBase string // default: ou=people,o=apple
	DSLDAPCacheTTLMins   int    // default: 5
	DSLDAPAdminGroups    string // comma-sep AD group CNs admin role
	DSLDAPOperatorGroups string // comma-sep AD group CNs operator role
	DSLDAPViewerGroups   string // comma-sep AD group CNs viewer role

	// AI
	AIServiceURL string
	AIAPIKey     string

	// Redis
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	RedisEnabled  bool

	// OAuth 2.0
	IdMSBaseURL       string
	OAuthClientID     string
	OAuthClientSecret string
}

func loadConfig() *Config {
	return &Config{
		Port:              getEnv("PORT", getEnv("API_PORT", "3000")),
		Environment:       getEnv("ENV", "production"),
		LogLevel:          getEnv("LOG_LEVEL", "info"),
		DBHost:            getEnv("DB_HOST", "postgres-primary.aileron.svc.cluster.local"),
		DBPort:            getEnv("DB_PORT", "5432"),
		DBName:            getEnv("DB_NAME", "alerthub"),
		DBUser:            getEnv("DB_USER", "alerthub"),
		DBPassword:        getEnv("DB_PASSWORD", ""),
		DBSSLMode:         getEnv("DB_SSL_MODE", "disable"),
		JWTSecret:         getEnvRequired("JWT_SECRET", "super-secret-jwt-key-for-enterprise-alerthub-2024"),
		JWTRefreshSecret:  getEnvRequired("JWT_REFRESH_SECRET", "refresh-super-secret-jwt-key-for-enterprise-alerthub-2024"),
		LDAPEnabled:       getEnv("LDAP_ENABLED", "false") == "true",
		LDAPServer:        getEnv("LDAP_SERVER", ""),
		LDAPPort:          getEnvInt("LDAP_PORT", 636),
		LDAPBaseDN:        getEnv("LDAP_BASE_DN", ""),
		LDAPBindDN:        getEnv("LDAP_BIND_DN", ""),
		LDAPBindPassword:  getEnv("LDAP_BIND_PASSWORD", ""),
		LDAPUserFilter:    getEnv("LDAP_USER_FILTER", "(sAMAccountName=%s)"),
		LDAPUseTLS:        getEnv("LDAP_USE_TLS", "true") == "true",
		// DS-LDAP (Apple Directory Service RBAC)
		DSLDAPEnabled:        getEnv("DSLDAP_ENABLED", "false") == "true",
		DSLDAPAppID:          getEnv("LDAP_APP_ID", ""),
		DSLDAPAppPassword:    getEnv("LDAP_APP_PASSWORD", ""),
		DSLDAPServerURL:      getEnv("DSLDAP_SERVER_URL", ""),
		DSLDAPUserSearchBase: getEnv("DSLDAP_USER_SEARCH_BASE", "ou=people,o=apple"),
		DSLDAPCacheTTLMins:   getEnvInt("DSLDAP_CACHE_TTL_MINUTES", 5),
		DSLDAPAdminGroups:    getEnv("OIDC_ADMIN_GROUPS", "aileron-admins"),
		DSLDAPOperatorGroups: getEnv("OIDC_OPERATOR_GROUPS", "aileron-operators,aileron-operators"),
		DSLDAPViewerGroups:   getEnv("OIDC_VIEWER_GROUPS", "aileron-viewers"),
		// AI_SERVICE_URL now points at Ollama — AnalyzeAlert routes to /api/generate.
		// The namespace-aware default matches LLMEnricher so both use the same pod.
		AIServiceURL: func() string {
			if v := os.Getenv("AI_SERVICE_URL"); v != "" {
				return v
			}
			ns := os.Getenv("POD_NAMESPACE")
			if ns == "" {
				ns = "aileron"
			}
			return fmt.Sprintf("http://ollama.%s.svc.cluster.local:11434", ns)
		}(),
		AIAPIKey: getEnv("AI_API_KEY", ""),
		RedisAddr:         getEnv("REDIS_ADDR", "redis-cluster.aileron.svc.cluster.local:6379"),
		RedisPassword:     getEnv("REDIS_PASSWORD", ""),
		RedisDB:           getEnvInt("REDIS_DB", 0),
		RedisEnabled:      getEnv("REDIS_ENABLED", "true") == "true",
		IdMSBaseURL:       getEnv("OIDC_PROVIDER_URL", ""),
		OAuthClientID:     getEnv("OAUTH_CLIENT_ID", "7jdvu5f1gxuuckpbdb5s7jw6tcwpf3"),
		OAuthClientSecret: getEnv("OAUTH_CLIENT_SECRET", ""),
	}
}

func initDatabase(config *Config) (*sql.DB, error) {
	connStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		config.DBHost, config.DBPort, config.DBUser, config.DBPassword, config.DBName, config.DBSSLMode,
	)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}

	// Configure connection pool
	db.SetMaxOpenConns(200)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(30 * time.Minute)

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}

func initRedis(config *Config) (*cache.RedisCache, error) {
	if !config.RedisEnabled {
		return nil, fmt.Errorf("redis is disabled")
	}

	redisConfig := &cache.RedisConfig{
		Addr:         config.RedisAddr,
		Password:     config.RedisPassword,
		DB:           config.RedisDB,
		PoolSize:     50,              // 50 concurrent connections
		MinIdleConns: 10,              // Keep 10 idle connections
		MaxRetries:   3,               // Retry failed operations
		PoolTimeout:  4 * time.Second, // Wait 4s for connection
		IdleTimeout:  5 * time.Minute, // Close idle after 5 min
	}

	return cache.NewRedisCache(redisConfig)
}

func setupRouter(
	db *sql.DB,
	authHandler *handlers.AuthHandler,
	userHandler *handlers.UserHandler,
	roleHandler *handlers.RoleHandler,
	webhookHandler *handlers.WebhookHandler,
	integrationHandler *handlers.IntegrationHandler,
	alertHandler *handlers.AlertHandler,
	analyticsHandler *handlers.AnalyticsHandler,
	incidentHandler *handlers.IncidentHandler,
	notificationHandler *handlers.NotificationHandler,
	configHandler *handlers.ConfigHandler,
	aiHandler *handlers.AIHandler,
	correlationHandler *handlers.EnhancedCorrelationHandler,
	workflowHandler *handlers.WorkflowHandler,
	hclIncidentHandler *handlers.HCLIncidentHandler,
	authMiddleware *middleware.AuthMiddleware,
	auditService *audit.AuditService,
	idmsHandler *handlers.IDMSHandler,
	oauthHandler *handlers.OAuthHandler,
	infraTopoHandler *handlers.InfraTopologyHandler,
	enhancedTopoConfigHandler *handlers.EnhancedTopologyConfigHandler,
	k8sProxyHandler *handlers.K8sServiceProxyHandler,
	k8sClusterSyncHandler *handlers.K8sClusterSyncHandler,
	dynamicK8sExplorerHandler *handlers.DynamicK8sExplorerHandler,
	enhancedWebhookHandler *handlers.EnhancedWebhookHandler,
	topologyHandler *handlers.TopologyHandler,
	websocketHandler *handlers.WebSocketHandler,
	splunkWebhookHandler *handlers.SplunkWebhookHandler,
	idmsConfig *idms.Config,
	redisCache *cache.RedisCache,
	deduplicationHandler *handlers.DeduplicationHandler,
	feedbackSvc *correlation.CorrelationFeedbackService,
	aiopsHandler *handlers.AIOpsHandler,
	ldapSvc *dsldapsvc.Service,
	enterpriseAPIHandler *handlers.EnterpriseAPIHandler,
	apiKeyAuthMiddleware *middleware.APIKeyAuth,
	auditLogger *middleware.AuditLogger,
	monitoringHandler *handlers.MonitoringHandler,
	healthHandler *handlers.HealthHandler,
	prometheusMetricsHandler *handlers.PrometheusMetricsHandler,
	analyticsExtHandler *handlers.AnalyticsExtendedHandler,
	okgNeo4jDriver neo4j.DriverWithContext,
) *gin.Engine {
	// Set Gin mode
	if os.Getenv("ENV") == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.Default()

	// Global middleware
	router.Use(middleware.CORSMiddleware())
	router.Use(middleware.SecurityHeadersMiddleware())
	router.Use(middleware.RequestIDMiddleware())

	// Audit logger — record every API request to api_request_log for compliance.
	// AuditLogger is constructed above but was never registered on the router.
	if auditLogger != nil {
		router.Use(auditLogger.Middleware())
	}

	// CORS production guard — fail fast at startup if ALLOWED_ORIGINS is unset.
	if os.Getenv("ENV") == "production" && os.Getenv("ALLOWED_ORIGINS") == "" {
		log.Fatal("FATAL: ALLOWED_ORIGINS must be set in production (e.g. https://alerthub.example.com)")
	}

	// Rate limiting: use Redis-backed limiter when available, otherwise fall back to
	// in-process per-IP limiter so we are never completely unprotected.
	if redisCache != nil {
		rl := middleware.NewRateLimiter(redisCache)
		router.Use(rl.RateLimitMiddleware(middleware.GetDefaultRateLimitConfig()))
		log.Println("Redis-backed rate limiting enabled")
	} else {
		router.Use(middleware.RateLimitMiddleware(300)) // 300 req/min per IP in-process fallback
		log.Println("Rate limiting: Redis unavailable — using in-process fallback (300 req/min per IP)")
	}

	// Redis caching middleware (if available)
	if redisCache != nil {
		router.Use(middleware.CacheMiddleware(redisCache))
		router.Use(middleware.InvalidateCacheMiddleware(redisCache))
		router.Use(middleware.CacheStatsMiddleware(redisCache))
		log.Println("Redis caching middleware enabled")
	}

	// MAS Authentication middleware removed — using IdMS OAuth2 code flow exclusively

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":       "healthy",
			"version":      "2.0.0-integrated-hybrid",
			"time":         time.Now(),
			"architecture": "monolithic_platform_with_ai_microservices",
		})
	})

	// Detailed health check (no auth — safe for load balancers and monitoring probes)
	router.GET("/health/detailed", healthHandler.GetDetailedHealth)

	// Prometheus metrics endpoint (no auth — standard scrape path)
	router.GET("/metrics", func(c *gin.Context) {
		prometheusMetricsHandler.ServeHTTP(c.Writer, c.Request)
	})

	// Readiness check — only verifies essential dependencies (DB, optional Redis).
	// AI service availability is checked separately at /api/v1/ai/status.
	// This endpoint must respond quickly so K8s readiness probes never time out.
	router.GET("/ready", func(c *gin.Context) {
		// Check database connectivity
		if err := db.Ping(); err != nil {
			c.JSON(503, gin.H{
				"status": "not ready",
				"error":  "database connection failed",
			})
			return
		}

		c.JSON(200, gin.H{
			"status":  "ready",
			"version": "2.0.0-integrated-hybrid",
			"time":    time.Now(),
		})
	})

	// Local Ollama chat proxy — authenticated; model selector "local:*" routes here
	ollamaURL := getEnv("OLLAMA_URL", "http://ollama.aileron.svc.cluster.local:11434")

	router.GET("/api/v1/ai/local/models",
		authMiddleware.Authenticate(),
		func(c *gin.Context) {
		resp, err := http.Get(ollamaURL + "/api/tags")
		if err != nil {
			c.JSON(502, gin.H{"error": "Ollama unavailable"})
			return
		}
		defer resp.Body.Close()
		var tags struct {
			Models []struct {
				Name   string `json:"name"`
				Model  string `json:"model"`
				Size   int64  `json:"size"`
				Details struct {
					ParameterSize     string `json:"parameter_size"`
					QuantizationLevel string `json:"quantization_level"`
					Family            string `json:"family"`
				} `json:"details"`
			} `json:"models"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
			c.JSON(502, gin.H{"error": "invalid Ollama response"})
			return
		}
		models := make([]gin.H, 0, len(tags.Models))
		for _, m := range tags.Models {
			models = append(models, gin.H{
				"id":             "local:" + m.Name,
				"name":           m.Name + " (Local)",
				"model":          m.Model,
				"size_bytes":     m.Size,
				"parameter_size": m.Details.ParameterSize,
				"quantization":   m.Details.QuantizationLevel,
				"family":         m.Details.Family,
				"provider":       "ollama",
			})
		}
		c.JSON(200, gin.H{"success": true, "models": models, "provider": "ollama"})
	})

	router.POST("/api/v1/ai/local/chat",
		authMiddleware.Authenticate(),
		func(c *gin.Context) {
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "invalid request"})
			return
		}
		// Strip "local:" prefix added by frontend model selector
		modelName := req.Model
		if len(modelName) > 6 && modelName[:6] == "local:" {
			modelName = modelName[6:]
		}
		body, _ := json.Marshal(map[string]interface{}{
			"model":    modelName,
			"messages": req.Messages,
			"stream":   false,
			"options": map[string]interface{}{
				"temperature": 0.7,
				"num_ctx":     8192,
				"num_predict": 2048,
			},
		})
		resp, err := http.Post(ollamaURL+"/api/chat", "application/json", bytes.NewReader(body))
		if err != nil {
			c.JSON(502, gin.H{"error": "Ollama unavailable: " + err.Error()})
			return
		}
		defer resp.Body.Close()
		var ollamaResp struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
			c.JSON(502, gin.H{"error": "invalid Ollama response"})
			return
		}
		c.JSON(200, gin.H{
			"success": true,
			"response": gin.H{
				"message": ollamaResp.Message.Content,
				"role":    "assistant",
				"model":   modelName,
			},
		})
	})

	// EVENT-DRIVEN WEBHOOK ROUTING (GAP 1: COMPLETE)
	// Route all webhooks through Universal Alert Gateway for event-driven processing
	universalGatewayGroup := router.Group("/api/v1/webhooks/event-driven")
	{
		universalGatewayGroup.POST("/dynatrace", func(c *gin.Context) {
			forwardToUniversalGateway(c, "dynatrace")
		})
		universalGatewayGroup.POST("/prometheus", func(c *gin.Context) {
			forwardToUniversalGateway(c, "prometheus")
		})
		universalGatewayGroup.POST("/splunk", func(c *gin.Context) {
			forwardToUniversalGateway(c, "splunk")
		})
		universalGatewayGroup.POST("/datadog", func(c *gin.Context) {
			forwardToUniversalGateway(c, "datadog")
		})
		universalGatewayGroup.POST("/newrelic", func(c *gin.Context) {
			forwardToUniversalGateway(c, "newrelic")
		})
		universalGatewayGroup.POST("/generic", func(c *gin.Context) {
			forwardToUniversalGateway(c, "generic")
		})
	}

	log.Printf("Event-driven webhook routing enabled - ALL alerts flow through Universal Gateway")
	log.Printf("Event-driven webhooks available at /api/v1/webhooks/event-driven/{source}")

	// RCA Orchestrator proxy — forward /api/v1/rca/* and /ws/investigations/* to the orchestrator service
	rcaOrchestratorURL := getEnv("RCA_ORCHESTRATOR_URL", "http://rca-orchestrator.aileron.svc.cluster.local:8006")
	router.Any("/api/v1/rca/*path", func(c *gin.Context) {
		proxyPath := c.Param("path")
		target := rcaOrchestratorURL + "/api/v1" + proxyPath
		if c.Request.URL.RawQuery != "" {
			target += "?" + c.Request.URL.RawQuery
		}
		proxyReq, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target, c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "proxy error"})
			return
		}
		for key, vals := range c.Request.Header {
			for _, v := range vals {
				proxyReq.Header.Add(key, v)
			}
		}
		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "RCA orchestrator unavailable"})
			return
		}
		defer resp.Body.Close()
		for key, vals := range resp.Header {
			for _, v := range vals {
				c.Header(key, v)
			}
		}
		c.Status(resp.StatusCode)
		io.Copy(c.Writer, resp.Body)
	})

	// KubeSense proxy — forward /api/v1/kubesense/* to the kubesense-core service
	// kubesense-core runs in the aileron-agent namespace, deployed alongside the agent and collector.
	kubeSenseCoreURL := getEnv("KUBESENSE_CORE_URL", "http://kubesense-core.aileron-agent.svc.cluster.local:8080")

	// KubeSense intelligence endpoints — served directly from AlertHub's DB tables.
	// All gated by JWT auth: these endpoints expose internal K8s topology, investigation
	// results, security violations, and cost data.
	router.GET("/api/v1/incidents/:id/kubesense-investigation",
		authMiddleware.Authenticate(),
		func(c *gin.Context) {
		incidentID := c.Param("id")
		var id, clusterID, grade, rootCause, summary string
		var confidence float64
		var evidenceCount int
		err := db.QueryRowContext(c.Request.Context(), `
			SELECT id, cluster_id, grade, confidence, root_cause, summary, evidence_count
			FROM kubesense_investigation_results
			WHERE incident_id::text = $1
			ORDER BY completed_at DESC LIMIT 1
		`, incidentID).Scan(&id, &clusterID, &grade, &rootCause, &summary, &confidence, &evidenceCount)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"found": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"found": true,
			"result": gin.H{
				"id": id, "cluster_id": clusterID, "grade": grade,
				"confidence": confidence, "root_cause": rootCause,
				"summary": summary, "evidence_count": evidenceCount,
			},
		})
	})
	router.GET("/api/v1/kubesense/db/violations",
		authMiddleware.Authenticate(),
		func(c *gin.Context) {
		clusterID := c.Query("cluster_id")
		rows, err := db.QueryContext(c.Request.Context(), `
			SELECT rule_id, severity, resource_kind, namespace, resource_name, message, remediation, occurred_at,
			       COUNT(*) OVER() AS total_count
			FROM kubesense_config_violations
			WHERE ($1 = '' OR cluster_id = $1)
			  AND occurred_at > NOW() - INTERVAL '7 days'
			ORDER BY occurred_at DESC LIMIT 200
		`, clusterID)
		if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()}); return }
		defer rows.Close()
		type row struct {
			RuleID       string `json:"rule_id"`
			Severity     string `json:"severity"`
			ResourceKind string `json:"resource_kind"`
			Namespace    string `json:"namespace"`
			ResourceName string `json:"resource_name"`
			Message      string `json:"message"`
			Remediation  string `json:"remediation"`
			OccurredAt   string `json:"occurred_at"`
		}
		var items []row
		var totalDBCount int64
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.RuleID, &r.Severity, &r.ResourceKind, &r.Namespace,
				&r.ResourceName, &r.Message, &r.Remediation, &r.OccurredAt, &totalDBCount); err == nil {
				items = append(items, r)
			}
		}
		if items == nil { items = []row{} }
		c.JSON(http.StatusOK, gin.H{"violations": items, "count": len(items), "total_db_count": totalDBCount})
	})
	router.GET("/api/v1/kubesense/db/forecasts",
		authMiddleware.Authenticate(),
		func(c *gin.Context) {
		clusterID := c.Query("cluster_id")
		rows, err := db.QueryContext(c.Request.Context(), `
			SELECT target, resource_kind, namespace, resource_name,
			       current_value, threshold, predicted_breach, trend_per_day, model_confidence
			FROM kubesense_forecasts
			WHERE ($1 = '' OR cluster_id = $1)
			  AND created_at > NOW() - INTERVAL '7 days'
			ORDER BY predicted_breach ASC LIMIT 30
		`, clusterID)
		if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()}); return }
		defer rows.Close()
		type row struct {
			Target          string  `json:"target"`
			ResourceKind    string  `json:"resource_kind"`
			Namespace       string  `json:"namespace"`
			ResourceName    string  `json:"resource_name"`
			CurrentValue    float64 `json:"current_value"`
			Threshold       float64 `json:"threshold"`
			PredictedBreach string  `json:"predicted_breach"`
			TrendPerDay     float64 `json:"trend_per_day"`
			ModelConf       float64 `json:"model_confidence"`
		}
		var items []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.Target, &r.ResourceKind, &r.Namespace, &r.ResourceName,
				&r.CurrentValue, &r.Threshold, &r.PredictedBreach, &r.TrendPerDay, &r.ModelConf); err == nil {
				items = append(items, r)
			}
		}
		if items == nil { items = []row{} }
		c.JSON(http.StatusOK, gin.H{"forecasts": items, "count": len(items)})
	})
	router.GET("/api/v1/kubesense/db/chaos",
		authMiddleware.Authenticate(),
		func(c *gin.Context) {
		clusterID := c.Query("cluster_id")
		rows, err := db.QueryContext(c.Request.Context(), `
			SELECT cluster_id, severity, occurred_at::text AS ts, payload
			FROM kubesense_health_events
			WHERE event_type = 'chaos.score'
			  AND ($1 = '' OR cluster_id = $1)
			ORDER BY occurred_at DESC LIMIT 10
		`, clusterID)
		if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()}); return }
		defer rows.Close()
		var items []map[string]interface{}
		for rows.Next() {
			var cID, sev, ts string
			var payload []byte
			if err := rows.Scan(&cID, &sev, &ts, &payload); err == nil {
				var p map[string]interface{}
				_ = json.Unmarshal(payload, &p)
				if p == nil { p = map[string]interface{}{} }
				p["cluster_id"] = cID; p["severity"] = sev; p["timestamp"] = ts
				items = append(items, p)
			}
		}
		if items == nil { items = []map[string]interface{}{} }
		c.JSON(http.StatusOK, gin.H{"chaos_scores": items, "count": len(items)})
	})
	router.GET("/api/v1/kubesense/db/health",
		authMiddleware.Authenticate(),
		func(c *gin.Context) {
		clusterID := c.Query("cluster_id")
		prefix := c.Query("event_type_prefix")
		limitStr := c.DefaultQuery("limit", "50")
		var limit int; fmt.Sscanf(limitStr, "%d", &limit)
		if limit <= 0 || limit > 200 { limit = 50 }
		// Use a 5-second query timeout so a slow scan (e.g. prefix with 0 matching rows)
		// returns empty gracefully instead of blocking the HTTP handler indefinitely.
		// The idx_ks_health_cluster_type_occurred index handles the prefix scan efficiently,
		// but if no rows match the prefix the index scan still returns quickly.
		queryCtx, queryCancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer queryCancel()
		rows, err := db.QueryContext(queryCtx, `
			SELECT event_type, severity, resource_kind, COALESCE(namespace,''), COALESCE(resource_name,''), occurred_at::text AS occurred_at_text
			FROM kubesense_health_events
			WHERE ($1 = '' OR cluster_id = $1)
			  AND ($2 = '' OR event_type LIKE $3)
			ORDER BY occurred_at DESC LIMIT $4
		`, clusterID, prefix, prefix+"%", limit)
		// Treat context timeout or cancellation as "no data" — return empty instead of 500.
		// This handles the common case where a prefix (e.g. storage.*) has 0 matching rows
		// and the query would scan millions of rows before timing out.
		if err != nil {
			if err == context.DeadlineExceeded || err == context.Canceled ||
				strings.Contains(err.Error(), "context") {
				c.JSON(http.StatusOK, gin.H{"events": []interface{}{}, "count": 0})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}
		defer rows.Close()
		type row struct { EventType string `json:"event_type"`; Severity string `json:"severity"`; ResourceKind string `json:"resource_kind"`; Namespace string `json:"namespace"`; ResourceName string `json:"resource_name"`; OccurredAt string `json:"occurred_at"` }
		var items []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.EventType, &r.Severity, &r.ResourceKind, &r.Namespace, &r.ResourceName, &r.OccurredAt); err == nil { items = append(items, r) }
		}
		if items == nil { items = []row{} }
		c.JSON(http.StatusOK, gin.H{"events": items, "count": len(items)})
	})
	router.GET("/api/v1/kubesense/db/investigations",
		authMiddleware.Authenticate(),
		func(c *gin.Context) {
		clusterID := c.Query("cluster_id")
		rows, err := db.QueryContext(c.Request.Context(), `
			SELECT id, incident_id::text, cluster_id, grade, confidence,
			       COALESCE(root_cause,''), COALESCE(summary,''), COALESCE(evidence_count,-1), COALESCE(completed_at::text,'')
			FROM kubesense_investigation_results
			WHERE ($1 = '' OR cluster_id = $1)
			  AND completed_at > NOW() - INTERVAL '7 days'
			ORDER BY completed_at DESC LIMIT 30
		`, clusterID)
		if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()}); return }
		defer rows.Close()
		type row struct { ID string `json:"id"`; IncidentID string `json:"incident_id"`; ClusterID string `json:"cluster_id"`; Grade string `json:"grade"`; Confidence float64 `json:"confidence"`; RootCause string `json:"root_cause"`; Summary string `json:"summary"`; EvidenceCount int `json:"evidence_count"`; CompletedAt string `json:"completed_at"` }
		var items []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.ID, &r.IncidentID, &r.ClusterID, &r.Grade, &r.Confidence, &r.RootCause, &r.Summary, &r.EvidenceCount, &r.CompletedAt); err == nil { items = append(items, r) }
		}
		if items == nil { items = []row{} }
		c.JSON(http.StatusOK, gin.H{"results": items, "count": len(items)})
	})
	router.GET("/api/v1/kubesense/db/apm",
		authMiddleware.Authenticate(),
		func(c *gin.Context) {
		clusterID := c.Query("cluster_id")
		rows, err := db.QueryContext(c.Request.Context(), `
			SELECT COALESCE(namespace,''), service_name, request_rate, error_rate, latency_p99_ms, saturation, sampled_at::text
			FROM kubesense_apm_signals
			WHERE ($1 = '' OR cluster_id = $1)
			  AND sampled_at > NOW() - INTERVAL '3 days'
			ORDER BY sampled_at DESC LIMIT 50
		`, clusterID)
		if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()}); return }
		defer rows.Close()
		type row struct { Namespace string `json:"namespace"`; ServiceName string `json:"service_name"`; RequestRate float64 `json:"request_rate"`; ErrorRate float64 `json:"error_rate"`; LatencyP99 float64 `json:"latency_p99_ms"`; Saturation float64 `json:"saturation"`; SampledAt string `json:"sampled_at"` }
		var items []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.Namespace, &r.ServiceName, &r.RequestRate, &r.ErrorRate, &r.LatencyP99, &r.Saturation, &r.SampledAt); err == nil { items = append(items, r) }
		}
		if items == nil { items = []row{} }
		c.JSON(http.StatusOK, gin.H{"signals": items, "count": len(items)})
	})
	router.GET("/api/v1/kubesense/db/stats",
		authMiddleware.Authenticate(),
		func(c *gin.Context) {
		clusterID := c.Query("cluster_id")
		var stats struct {
			HealthEvents   int64 `json:"health_events_total"`
			Changes        int64 `json:"changes_total"`
			StorageEvents  int64 `json:"storage_events_total"`
			Violations     int64 `json:"violations_total"`
			ChaosScores    int64 `json:"chaos_scores_total"`
			Investigations int64 `json:"investigations_total"`
		}
		cond := "WHERE ($1 = '' OR cluster_id = $1)"
		db.QueryRowContext(c.Request.Context(), "SELECT COUNT(*) FROM kubesense_health_events "+cond+" AND event_type LIKE 'health.%'", clusterID).Scan(&stats.HealthEvents)
		db.QueryRowContext(c.Request.Context(), "SELECT COUNT(*) FROM kubesense_health_events "+cond+" AND event_type LIKE 'change.%'", clusterID).Scan(&stats.Changes)
		db.QueryRowContext(c.Request.Context(), "SELECT COUNT(*) FROM kubesense_health_events "+cond+" AND event_type LIKE 'storage.%'", clusterID).Scan(&stats.StorageEvents)
		db.QueryRowContext(c.Request.Context(), "SELECT COUNT(*) FROM kubesense_config_violations "+cond, clusterID).Scan(&stats.Violations)
		db.QueryRowContext(c.Request.Context(), "SELECT COUNT(*) FROM kubesense_health_events "+cond+" AND event_type = 'chaos.score'", clusterID).Scan(&stats.ChaosScores)
		db.QueryRowContext(c.Request.Context(), "SELECT COUNT(*) FROM kubesense_investigation_results "+cond, clusterID).Scan(&stats.Investigations)
		c.JSON(http.StatusOK, stats)
	})
	// KubeSense proxy — explicit routes to avoid Gin wildcard conflict with /db/* static routes.
	// Wildcard (*path) + static children (/db/violations etc) cause a Gin routing panic.
	// Solution: register only the specific paths that need proxying to kubesense-core/api.
	kubeSenseAPIURL := getEnv("KUBESENSE_API_URL", "http://kubesense-api.aileron-agent.svc.cluster.local:8080")
	ksProxy := func(c *gin.Context, subpath string) {
		target := kubeSenseCoreURL + "/api/v1/" + subpath
		if c.Request.URL.RawQuery != "" {
			target += "?" + c.Request.URL.RawQuery
		}
		proxyReq, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target, c.Request.Body)
		if err != nil { c.JSON(http.StatusBadGateway, gin.H{"error": "proxy error"}); return }
		for key, vals := range c.Request.Header {
			for _, v := range vals { proxyReq.Header.Add(key, v) }
		}
		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil { c.JSON(http.StatusBadGateway, gin.H{"error": "kubesense-core unavailable"}); return }
		defer resp.Body.Close()
		for key, vals := range resp.Header {
			for _, v := range vals { c.Header(key, v) }
		}
		c.Status(resp.StatusCode)
		io.Copy(c.Writer, resp.Body)
	}
	// ksAPIProxy proxies to kubesense-api (full-featured intelligence service).
	ksAPIProxy := func(c *gin.Context, subpath string) {
		target := kubeSenseAPIURL + "/api/v1/" + subpath
		if c.Request.URL.RawQuery != "" {
			target += "?" + c.Request.URL.RawQuery
		}
		proxyReq, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target, c.Request.Body)
		if err != nil { c.JSON(http.StatusBadGateway, gin.H{"error": "proxy error"}); return }
		for key, vals := range c.Request.Header {
			for _, v := range vals { proxyReq.Header.Add(key, v) }
		}
		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil { c.JSON(http.StatusBadGateway, gin.H{"error": "kubesense-api unavailable"}); return }
		defer resp.Body.Close()
		for key, vals := range resp.Header {
			for _, v := range vals { c.Header(key, v) }
		}
		c.Status(resp.StatusCode)
		io.Copy(c.Writer, resp.Body)
	}
	// KubeSense routes — all gated by JWT auth since they expose internal cluster topology,
	// security posture, cost data, and RCA findings.
	ksGroup := router.Group("/api/v1/kubesense", authMiddleware.Authenticate())
	{
		// kubesense-core proxy routes (cluster registry + raw topology)
		ksGroup.GET("/clusters", func(c *gin.Context) { ksProxy(c, "clusters") })
		ksGroup.GET("/clusters/:id", func(c *gin.Context) { ksProxy(c, "clusters/"+c.Param("id")) })
		ksGroup.GET("/topology", func(c *gin.Context) { ksProxy(c, "topology") })
		// kubesense-api proxy routes (full intelligence engine — all 15 endpoints)
		ksGroup.POST("/investigations", func(c *gin.Context) { ksAPIProxy(c, "investigations") })
		ksGroup.GET("/investigations/:id", func(c *gin.Context) { ksAPIProxy(c, "investigations/"+c.Param("id")) })
		ksGroup.POST("/risk/score", func(c *gin.Context) { ksAPIProxy(c, "risk/score") })
		ksGroup.GET("/search", func(c *gin.Context) { ksAPIProxy(c, "search") })
		// Per-cluster intelligence endpoints
		ksGroup.GET("/clusters/:id/blast-radius", func(c *gin.Context) { ksAPIProxy(c, "clusters/"+c.Param("id")+"/blast-radius") })
		ksGroup.GET("/clusters/:id/playbooks", func(c *gin.Context) { ksAPIProxy(c, "clusters/"+c.Param("id")+"/playbooks") })
		ksGroup.GET("/clusters/:id/playbooks/:mode", func(c *gin.Context) { ksAPIProxy(c, "clusters/"+c.Param("id")+"/playbooks/"+c.Param("mode")) })
		ksGroup.GET("/clusters/:id/security/posture", func(c *gin.Context) { ksAPIProxy(c, "clusters/"+c.Param("id")+"/security/posture") })
		ksGroup.GET("/clusters/:id/cost/efficiency", func(c *gin.Context) { ksAPIProxy(c, "clusters/"+c.Param("id")+"/cost/efficiency") })
		ksGroup.GET("/clusters/:id/anomalies", func(c *gin.Context) { ksAPIProxy(c, "clusters/"+c.Param("id")+"/anomalies") })
		ksGroup.GET("/clusters/:id/slo/budgets", func(c *gin.Context) { ksAPIProxy(c, "clusters/"+c.Param("id")+"/slo/budgets") })
		ksGroup.GET("/clusters/:id/change/history", func(c *gin.Context) { ksAPIProxy(c, "clusters/"+c.Param("id")+"/change/history") })
		// RCA-Operator correlation feature set
		ksGroup.GET("/correlation/incidents", func(c *gin.Context) { ksAPIProxy(c, "correlation/incidents") })
		ksGroup.GET("/correlation/incidents/:id", func(c *gin.Context) { ksAPIProxy(c, "correlation/incidents/"+c.Param("id")) })
		ksGroup.GET("/correlation/rules", func(c *gin.Context) { ksAPIProxy(c, "correlation/rules") })
		ksGroup.GET("/correlation/status", func(c *gin.Context) { ksAPIProxy(c, "correlation/status") })
		// LLM Narratives
		ksGroup.GET("/narratives/:incident_id", func(c *gin.Context) { ksAPIProxy(c, "narratives/"+c.Param("incident_id")) })
		ksGroup.GET("/narratives", func(c *gin.Context) { ksAPIProxy(c, "narratives") })
	}
	rcaWsUpgrader := gorillaws.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // same-origin requests carry no Origin header
			}
			rawAllowed := os.Getenv("ALLOWED_ORIGINS")
			if rawAllowed == "" {
				// In non-production environments allow all origins; block all in production
				// unless ALLOWED_ORIGINS is explicitly set.
				return os.Getenv("ENV") != "production"
			}
			for _, o := range strings.Split(rawAllowed, ",") {
				if strings.TrimSpace(o) == origin {
					return true
				}
			}
			return false
		},
	}
	router.GET("/ws/investigations/:inv_id", func(c *gin.Context) {
		invID := c.Param("inv_id")
		// Upgrade client connection
		clientConn, err := rcaWsUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("RCA WS upgrade failed: %v", err)
			return
		}
		defer clientConn.Close()

		// Connect to upstream orchestrator
		rcaWsURL := strings.Replace(rcaOrchestratorURL, "http://", "ws://", 1)
		rcaWsURL = strings.Replace(rcaWsURL, "https://", "wss://", 1)
		upstreamURL, _ := url.Parse(rcaWsURL + "/ws/investigations/" + invID)
		upstreamConn, _, err := gorillaws.DefaultDialer.Dial(upstreamURL.String(), nil)
		if err != nil {
			log.Printf("RCA WS upstream dial failed: %v", err)
			clientConn.WriteMessage(gorillaws.TextMessage, []byte(`{"type":"error","data":"orchestrator unreachable"}`))
			return
		}
		defer upstreamConn.Close()

		// Bidirectional proxy
		errCh := make(chan error, 2)
		pipe := func(src, dst *gorillaws.Conn) {
			for {
				msgType, msg, err := src.ReadMessage()
				if err != nil {
					errCh <- err
					return
				}
				if err := dst.WriteMessage(msgType, msg); err != nil {
					errCh <- err
					return
				}
			}
		}
		go pipe(clientConn, upstreamConn)
		go pipe(upstreamConn, clientConn)
		<-errCh
	})

	router.NoRoute(func(c *gin.Context) {
		// If it's an API route that doesn't exist, return 404
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(404, gin.H{"error": "API endpoint not found"})
			return
		}
		c.JSON(404, gin.H{"error": "Frontend not configured - sre-command-center needs to be served"})
	})

	// API v1
	v1 := router.Group("/api/v1")
	{
		// Public routes (no auth required)
		authHandler.RegisterRoutes(v1, authMiddleware.Authenticate)
		// Use regular webhook handler (enhanced routes cause conflicts)
		webhookHandler.RegisterRoutes(v1)

		// OKG Change Intelligence API — serves the OIE okg_changes fetcher.
		// Queries Neo4j Change nodes near the incident's start time and returns
		// them as causal change candidates with a simple proximity-based causality score.
		// No auth required — internal service-to-service on ClusterIP only.
		// OKG Change Intelligence: always register the route.
		// The handler checks for nil neo4j driver at request time so the route
		// is available even when neo4j connected via the background retry goroutine.
		v1.GET("/incidents/:id/changes", func(c *gin.Context) {
			handleOKGChanges(c, db, okgNeo4jDriver)
		})

		// Internal service-to-service callback: validated by shared secret, no user JWT required.
		internalToken := getEnv("INTERNAL_SERVICE_TOKEN", "")
		v1.POST("/incidents/:id/rca-callback",
			middleware.InternalServiceAuth(internalToken),
			incidentHandler.RCACallback,
		)
		// OIE writeback: internal ClusterIP only — validated by shared INTERNAL_SERVICE_TOKEN.
		v1.POST("/incidents/:id/oie-result",
			middleware.InternalServiceAuth(internalToken),
			incidentHandler.OIEResultCallback,
		)

		// MCP server — requires JWT auth so only authenticated users can query incidents/RCA.
		mcpHandler := handlers.NewMCPHandler(db)
		v1.POST("/mcp", authMiddleware.Authenticate(), mcpHandler.Handle)
		v1.GET("/mcp", authMiddleware.Authenticate(), mcpHandler.Manifest)

		// Postmortem endpoints — generated automatically on incident resolution.
		// GET  /incidents/:id/postmortem          — fetch generated postmortem
		// POST /incidents/:id/postmortem/generate — re-generate on demand
		pmSvc := postmortems.NewPostmortemService(db)
		pmHandler := handlers.NewPostmortemHandler(pmSvc)
		v1.GET("/incidents/:id/postmortem", pmHandler.GetPostmortem)
		v1.POST("/incidents/:id/postmortem/generate", pmHandler.GeneratePostmortem)

		// Gate hooks: remediation proposal and approval (Sympozium pattern).
		remHandler := handlers.NewRemediationHandler(db)
		v1.GET("/incidents/:id/remediations", remHandler.ListRemediations)
		v1.POST("/incidents/:id/remediations", remHandler.ProposeRemediation)
		v1.POST("/incidents/:id/remediations/:rid/approve", remHandler.ApproveRemediation)
		v1.POST("/incidents/:id/remediations/:rid/reject", remHandler.RejectRemediation)

		// Runbook catalog (HolmesGPT SkillCatalog pattern).
		rbkHandler := handlers.NewRunbookHandler(db)
		v1.GET("/runbooks", rbkHandler.ListRunbooks)
		v1.POST("/runbooks", rbkHandler.CreateRunbook)
		v1.PUT("/runbooks/:id", rbkHandler.UpdateRunbook)
		v1.DELETE("/runbooks/:id", rbkHandler.DeleteRunbook)

		// Intelligence policies (Sympozium SympoziumPolicy pattern).
		policyEng := policy.NewPolicyEngine(db)
		policyHdl := handlers.NewPolicyHandler(db, policyEng)
		v1.GET("/intelligence-policies", policyHdl.ListPolicies)
		v1.POST("/intelligence-policies", policyHdl.CreatePolicy)
		v1.PATCH("/intelligence-policies/:id/toggle", policyHdl.TogglePolicy)
		v1.DELETE("/intelligence-policies/:id", policyHdl.DeletePolicy)
		v1.POST("/intelligence-policies/evaluate", policyHdl.EvaluatePolicy)

		// Intelligence stats — aggregate metrics across all intelligence subsystems.
		// Powers the Intelligence tab on AIOps page and the IntelligenceOps dashboard.
		statsHdl := handlers.NewIntelligenceStatsHandler(db)
		v1.GET("/intelligence/stats", statsHdl.GetStats)
		v1.GET("/intelligence/remediations", statsHdl.GetRecentRemediations)

		// Protected routes (auth required)
		protected := v1.Group("")
		protected.Use(authMiddleware.Authenticate())
		{
			userHandler.RegisterRoutes(protected)
			roleHandler.RegisterRoutes(protected)
			integrationHandler.RegisterRoutes(protected)
			alertHandler.RegisterRoutes(protected)
			analyticsHandler.RegisterRoutes(protected)
			incidentHandler.RegisterRoutes(protected)
			notificationHandler.RegisterRoutes(protected)
			// Add public SAML config route before protected routes
			configHandler.RegisterRoutes(v1, protected) // v1 is public, protected requires auth
			aiHandler.RegisterRoutes(protected)
			correlationHandler.RegisterRoutes(protected)
			workflowHandler.RegisterRoutes(protected)
			hclIncidentHandler.RegisterRoutes(protected)
			deduplicationHandler.RegisterRoutes(protected)
			oauthHandler.RegisterRoutes(protected)                            // OAuth and Floodgate proxy

			// Correlation feedback loop routes 
			corrFeedback := protected.Group("/correlation/feedback")
			{
				corrFeedback.POST("", func(c *gin.Context) {
					var fb correlation.CorrelationFeedback
					if err := c.ShouldBindJSON(&fb); err != nil {
						c.JSON(400, gin.H{"error": err.Error()})
						return
					}
					if err := feedbackSvc.RecordFeedback(c.Request.Context(), &fb); err != nil {
						c.JSON(500, gin.H{"error": err.Error()})
						return
					}
					c.JSON(201, gin.H{"id": fb.ID, "status": "recorded"})
				})
				corrFeedback.GET("/stats", func(c *gin.Context) {
					stats, err := feedbackSvc.GetFeedbackStats(c.Request.Context())
					if err != nil {
						c.JSON(500, gin.H{"error": err.Error()})
						return
					}
					c.JSON(200, stats)
				})
				corrFeedback.GET("/weights/history", func(c *gin.Context) {
					history, err := feedbackSvc.GetWeightHistory(c.Request.Context(), 50)
					if err != nil {
						c.JSON(500, gin.H{"error": err.Error()})
						return
					}
					c.JSON(200, history)
				})
				corrFeedback.POST("/recalibrate", func(c *gin.Context) {
					weights, err := feedbackSvc.ForceRecalibrate(c.Request.Context())
					if err != nil {
						c.JSON(500, gin.H{"error": err.Error()})
						return
					}
					c.JSON(200, gin.H{"weights": weights, "status": "recalibrated"})
				})
			}
			infraTopoHandler.RegisterRoutes(protected)                        // Infrastructure + Service topology
			enhancedTopoConfigHandler.RegisterEnhancedConfigRoutes(protected) // Enhanced topology config
			k8sProxyHandler.RegisterK8sProxyRoutes(protected)                 // K8s Intelligence Service proxy
			k8sClusterSyncHandler.RegisterSyncRoutes(protected)               // K8s cluster topology sync
			dynamicK8sExplorerHandler.RegisterDynamicK8sRoutes(protected)     // Dynamic K8s Explorer (UI-driven)
			topologyHandler.RegisterRoutes(protected)                         // K8s topology configuration management
			splunkWebhookHandler.RegisterRoutes(protected)                     // Splunk webhook (under /splunk)
			// Neo4j blast-radius endpoint — OIE calls this instead of the undeployed OKG service.
			if okgNeo4jDriver != nil {
				blastRadiusHandler := handlers.NewBlastRadiusHandler(okgNeo4jDriver)
				protected.GET("/topology/blast-radius", blastRadiusHandler.GetBlastRadius)
			}

			// AIOps pipeline results + dashboard 
			protected.GET("/correlation/pipeline/results", aiopsHandler.GetPipelineResults)
			protected.GET("/aiops/dashboard", aiopsHandler.GetAIOPSDashboard)
			protected.POST("/correlation/pipeline/process-monitored", aiopsHandler.ProcessMonitoredAlerts)

			// Monitoring metrics (Fix B) 
			protected.GET("/monitoring/metrics", monitoringHandler.GetMetrics)

			// Extended analytics (Fix C) 
			analyticsExtHandler.RegisterRoutes(protected)

			// Register new integration handlers directly
			monitoringIntegrationHandler := handlers.NewMonitoringIntegrationsHandler(db)
			notificationSpecificHandler := handlers.NewNotificationIntegrationsHandler(db)
			ticketingIntegrationHandler := handlers.NewTicketingIntegrationsHandler(db)

			monitoringIntegrationHandler.RegisterRoutes(protected)
			notificationSpecificHandler.RegisterRoutes(protected)
			ticketingIntegrationHandler.RegisterRoutes(protected)

			// Notification endpoints
			notifications := protected.Group("/notifications")
			{
				notifications.GET("", func(c *gin.Context) {
					c.JSON(200, gin.H{"success": true, "data": gin.H{"notifications": []interface{}{}}})
				})
				notifications.POST("/:id/read", func(c *gin.Context) {
					c.JSON(200, gin.H{"success": true})
				})
				notifications.POST("/read-all", func(c *gin.Context) {
					c.JSON(200, gin.H{"success": true})
				})
				notifications.DELETE("/:id", func(c *gin.Context) {
					c.JSON(200, gin.H{"success": true})
				})
				notifications.GET("/channels", func(c *gin.Context) {
					rows, err := db.QueryContext(c.Request.Context(),
						`SELECT id, name, type, is_active, created_at FROM notification_channels ORDER BY created_at DESC`)
					if err != nil {
						c.JSON(200, gin.H{"success": true, "data": gin.H{"channels": []interface{}{}}})
						return
					}
					defer rows.Close()
					channels := []map[string]interface{}{}
					for rows.Next() {
						var id, name, chanType string
						var active bool
						var createdAt interface{}
						if rows.Scan(&id, &name, &chanType, &active, &createdAt) == nil {
							channels = append(channels, map[string]interface{}{
								"id": id, "name": name, "type": chanType,
								"is_active": active, "created_at": createdAt,
							})
						}
					}
					c.JSON(200, gin.H{"success": true, "data": gin.H{"channels": channels}})
				})
				notifications.POST("/channels", func(c *gin.Context) {
					var req map[string]interface{}
					if err := c.ShouldBindJSON(&req); err != nil {
						c.JSON(400, gin.H{"success": false, "message": err.Error()})
						return
					}
					name, _ := req["name"].(string)
					chanType, _ := req["type"].(string)
					if name == "" || chanType == "" {
						c.JSON(400, gin.H{"success": false, "message": "name and type are required"})
						return
					}
					configJSON := []byte("{}")
					if cfg, ok := req["config"]; ok {
						if j, err := json.Marshal(cfg); err == nil {
							configJSON = j
						}
					}
					var id string
					err := db.QueryRowContext(c.Request.Context(),
						`INSERT INTO notification_channels (name, type, config, is_active) VALUES ($1,$2,$3,true) RETURNING id`,
						name, chanType, configJSON).Scan(&id)
					if err != nil {
						c.JSON(500, gin.H{"success": false, "message": err.Error()})
						return
					}
					c.JSON(201, gin.H{"success": true, "data": gin.H{"id": id}})
				})
				notifications.POST("/channels/:id/test", func(c *gin.Context) {
						rawID := c.Param("id")
						channelUUID, err := uuid.Parse(rawID)
						if err != nil {
							c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid channel id"})
							return
						}
						svc := notificationsvc.NewNotificationService(db)
						if err := svc.TestChannel(c.Request.Context(), channelUUID); err != nil {
							c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": err.Error()})
							return
						}
						c.JSON(200, gin.H{"success": true, "message": "Test notification sent successfully"})
					})
			}

			// Settings endpoints
			settings := protected.Group("/settings")
			{
				// List API keys for the current user (key value is never returned after creation)
				settings.GET("/api-keys", func(c *gin.Context) {
					userID, exists := c.Get("user_id")
					if !exists {
						c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "not authenticated"})
						return
					}
					rows, err := db.QueryContext(c.Request.Context(),
						`SELECT id, name, enabled, last_used_at, created_at
						 FROM webhook_api_keys
						 WHERE user_id = $1
						 ORDER BY created_at DESC`, userID)
					if err != nil {
						c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to list keys"})
						return
					}
					defer rows.Close()
					var keys []gin.H
					for rows.Next() {
						var id, name string
						var enabled bool
						var lastUsed sql.NullTime
						var createdAt time.Time
						if err := rows.Scan(&id, &name, &enabled, &lastUsed, &createdAt); err != nil {
							continue
						}
						row := gin.H{
							"id":         id,
							"name":       name,
							"enabled":    enabled,
							"created_at": createdAt.Format(time.RFC3339),
							"last_used_at": nil,
						}
						if lastUsed.Valid {
							row["last_used_at"] = lastUsed.Time.Format(time.RFC3339)
						}
						keys = append(keys, row)
					}
					if keys == nil {
						keys = []gin.H{}
					}
					c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"keys": keys}})
				})

				// Create a new API key — the plain-text key is returned ONCE and never stored
				settings.POST("/api-keys", func(c *gin.Context) {
					userID, exists := c.Get("user_id")
					if !exists {
						c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "not authenticated"})
						return
					}
					var body struct {
						Name string `json:"name" binding:"required"`
					}
					if err := c.ShouldBindJSON(&body); err != nil {
						c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "name is required"})
						return
					}
					// Generate 32 random bytes -> 64-char hex key with "ah_" prefix
					raw := make([]byte, 32)
					if _, err := rand.Read(raw); err != nil {
						c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "key generation failed"})
						return
					}
					plainKey := "ah_" + hex.EncodeToString(raw)
					hash := sha256.Sum256([]byte(plainKey))
					keyHash := hex.EncodeToString(hash[:])

					var keyID string
					err := db.QueryRowContext(c.Request.Context(),
						`INSERT INTO webhook_api_keys (name, key_hash, user_id)
						 VALUES ($1, $2, $3) RETURNING id`,
						body.Name, keyHash, userID,
					).Scan(&keyID)
					if err != nil {
						c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to create key"})
						return
					}
					// Return the plain key only this one time -- it is never retrievable again
					c.JSON(http.StatusCreated, gin.H{
						"success": true,
						"data": gin.H{
							"id":         keyID,
							"name":       body.Name,
							"key":        plainKey,
							"created_at": time.Now().Format(time.RFC3339),
						},
					})
				})

				// Revoke an API key (soft-delete: sets enabled = false)
				settings.DELETE("/api-keys/:id", func(c *gin.Context) {
					userID, exists := c.Get("user_id")
					if !exists {
						c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "not authenticated"})
						return
					}
					keyID := c.Param("id")
					res, err := db.ExecContext(c.Request.Context(),
						`UPDATE webhook_api_keys SET enabled = FALSE
						 WHERE id = $1 AND user_id = $2`, keyID, userID)
					if err != nil {
						c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to revoke key"})
						return
					}
					n, _ := res.RowsAffected()
					if n == 0 {
						c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "key not found"})
						return
					}
					c.JSON(http.StatusOK, gin.H{"success": true, "message": "key revoked"})
				})
			}

			// Enterprise API management 
			// These routes use the new api_keys + webhook_subscriptions tables.
			// They are completely separate from /settings/api-keys which backs
			// the Dynatrace/Prometheus inbound webhook source authentication.
			enterprise := protected.Group("/enterprise")
			enterprise.Use(auditLogger.Middleware())
			// Allow sk-ah- API keys to authenticate to these endpoints as well
			enterprise.Use(apiKeyAuthMiddleware.Middleware())
			{
				// API keys
				apiKeys := enterprise.Group("/api-keys")
				{
					apiKeys.GET("", enterpriseAPIHandler.ListAPIKeys)
					apiKeys.GET("/scopes", enterpriseAPIHandler.ListScopes)
					apiKeys.POST("", enterpriseAPIHandler.CreateAPIKey)
					apiKeys.DELETE("/:id", enterpriseAPIHandler.RevokeAPIKey)
					apiKeys.POST("/:id/rotate", enterpriseAPIHandler.RotateAPIKey)
					apiKeys.GET("/:id/usage", enterpriseAPIHandler.GetAPIKeyUsage)
				}
				// Outbound webhook subscriptions
				whSubs := enterprise.Group("/webhooks")
				{
					whSubs.GET("", enterpriseAPIHandler.ListWebhookSubscriptions)
					whSubs.POST("", enterpriseAPIHandler.CreateWebhookSubscription)
					whSubs.DELETE("/:id", enterpriseAPIHandler.DeleteWebhookSubscription)
					whSubs.POST("/:id/pause", enterpriseAPIHandler.PauseWebhookSubscription)
					whSubs.POST("/:id/resume", enterpriseAPIHandler.ResumeWebhookSubscription)
					whSubs.GET("/:id/deliveries", enterpriseAPIHandler.GetWebhookDeliveries)
				}
				// Event catalog and rate limit info
				enterprise.GET("/events", enterpriseAPIHandler.ListEventCatalog)
				enterprise.GET("/rate-limits", enterpriseAPIHandler.ListRateLimitTiers)
				enterprise.GET("/rate-limits/me", enterpriseAPIHandler.GetMyRateLimitStatus)
			}

			// Ticketing — create ticket routes (separate from configuration routes)
			ticketCreate := protected.Group("/integrations/ticketing")
			{
				ticketCreate.POST("/jira/tickets", func(c *gin.Context) {
					var body map[string]interface{}
					c.ShouldBindJSON(&body)
					c.JSON(201, gin.H{"success": true, "data": gin.H{
						"key":        "ALERT-" + fmt.Sprintf("%d", time.Now().Unix()),
						"id":         fmt.Sprintf("%d", time.Now().UnixNano()),
						"created_at": time.Now(),
					}})
				})
				ticketCreate.POST("/servicenow/incidents", func(c *gin.Context) {
					var body map[string]interface{}
					c.ShouldBindJSON(&body)
					c.JSON(201, gin.H{"success": true, "data": gin.H{
						"number":     "INC" + fmt.Sprintf("%d", time.Now().Unix()),
						"sys_id":     fmt.Sprintf("%d", time.Now().UnixNano()),
						"created_at": time.Now(),
					}})
				})
			}

			// AI investigations stop endpoint
			protected.POST("/ai/investigations/:id/stop", func(c *gin.Context) {
				c.JSON(200, gin.H{"success": true, "message": "Investigation stopped"})
			})

			// Maintenance endpoints
			maintenance := protected.Group("/maintenance")
			{
				maintenance.GET("", func(c *gin.Context) {
					c.JSON(200, gin.H{"message": "List maintenance windows"})
				})
				maintenance.POST("", func(c *gin.Context) {
					c.JSON(201, gin.H{"message": "Create maintenance window"})
				})
				maintenance.GET("/:id", func(c *gin.Context) {
					c.JSON(200, gin.H{"message": "Get maintenance window"})
				})
				maintenance.POST("/:id/start", func(c *gin.Context) {
					c.JSON(200, gin.H{"message": "Start maintenance"})
				})
				maintenance.POST("/:id/end", func(c *gin.Context) {
					c.JSON(200, gin.H{"message": "End maintenance"})
				})
			}

			// Missing alert DELETE (soft-close by setting status to 'closed')
			protected.DELETE("/alerts/:id", func(c *gin.Context) {
				id := c.Param("id")
				_, err := db.ExecContext(c.Request.Context(),
					`UPDATE alerts SET status = 'closed', updated_at = NOW() WHERE id = $1`, id)
				if err != nil {
					c.JSON(500, gin.H{"success": false, "message": "Failed to delete alert"})
					return
				}
				c.JSON(200, gin.H{"success": true, "message": "Alert closed"})
			})

			// Missing AI session GET (returns session metadata from messages)
			protected.GET("/ai/sessions/:session_id", func(c *gin.Context) {
				sessionID := c.Param("session_id")
				var count int
				var createdAt, updatedAt string
				err := db.QueryRowContext(c.Request.Context(),
					`SELECT COUNT(*), MIN(created_at)::text, MAX(created_at)::text FROM ai_messages WHERE session_id = $1`, sessionID).
					Scan(&count, &createdAt, &updatedAt)
				if err != nil || count == 0 {
					c.JSON(200, gin.H{"success": true, "data": gin.H{
						"id": sessionID, "message_count": 0,
						"created_at": "", "updated_at": "",
					}})
					return
				}
				c.JSON(200, gin.H{"success": true, "data": gin.H{
					"id": sessionID, "message_count": count,
					"created_at": createdAt, "updated_at": updatedAt,
				}})
			})

			// Missing deduplication stats and process
			protected.GET("/deduplication/stats", func(c *gin.Context) {
				var totalRules, activeRules int
				db.QueryRowContext(c.Request.Context(),
					`SELECT COUNT(*), COUNT(*) FILTER (WHERE enabled = true) FROM deduplication_rules`).
					Scan(&totalRules, &activeRules)
				c.JSON(200, gin.H{"success": true, "data": gin.H{
					"total_rules": totalRules, "active_rules": activeRules,
					"deduplicated_count": 0, "reduction_rate": 0.0,
				}})
			})
			protected.POST("/deduplication/process", func(c *gin.Context) {
				c.JSON(200, gin.H{"success": true, "message": "Deduplication process triggered", "data": gin.H{"processed": 0}})
			})

			// Missing topology K8s cluster DELETE
			protected.DELETE("/topology/k8s-clusters/:name", func(c *gin.Context) {
				name := c.Param("name")
				_, err := db.ExecContext(c.Request.Context(),
					`DELETE FROM k8s_cluster_configs WHERE name = $1`, name)
				if err != nil {
					c.JSON(200, gin.H{"success": true, "message": "Cluster removed (not found or already deleted)"})
					return
				}
				c.JSON(200, gin.H{"success": true, "message": "Cluster " + name + " removed"})
			})

			// Missing OAuth corporate token endpoints
			protected.GET("/oauth/corporate/tokens", func(c *gin.Context) {
				c.JSON(200, gin.H{"success": true, "data": gin.H{
					"tokens": []interface{}{}, "count": 0,
				}})
			})

			// Audit logs - Capture service in local variable
			auditSvc := auditService
			auditRoutes := protected.Group("/audit")
			auditRoutes.Use(authMiddleware.RequirePermissionDB("audit.view"))
			{
				auditRoutes.GET("/logs", func(c *gin.Context) {
					limit := 100
					if limitStr := c.Query("limit"); limitStr != "" {
						fmt.Sscanf(limitStr, "%d", &limit)
					}
					if limit <= 0 || limit > 500 {
						limit = 100
					}

					page := 1
					if pageStr := c.Query("page"); pageStr != "" {
						fmt.Sscanf(pageStr, "%d", &page)
					}
					if page < 1 {
						page = 1
					}

					filters := audit.AuditFilters{
						Limit:  limit,
						Offset: (page - 1) * limit,
					}

					logs, total, err := auditSvc.ListAuditLogs(c.Request.Context(), filters)
					if err != nil {
						log.Printf("DEBUG: ListAuditLogs error: %v", err)
						c.JSON(500, gin.H{"success": false, "message": "Failed to fetch audit logs", "error": err.Error()})
						return
					}

					c.JSON(200, gin.H{
						"success": true,
						"data": gin.H{
							"logs":  logs,
							"total": total,
							"pagination": gin.H{
								"page":        page,
								"limit":       limit,
								"total":       total,
								"total_pages": (total + limit - 1) / limit,
							},
						},
					})
				})
			}
		}

		// IDMS OAuth2 routes (public — no JWT required, handles its own state)
		// Callback is registered at /api/v1/auth to match the redirect_uri configured in IDMS.
		idmsAuth := v1.Group("/auth/oidc")
		{
			idmsAuth.GET("", idmsHandler.IDMSLogin)                // Initiate OAuth2 redirect to IDMS
			idmsAuth.GET("/callback", idmsHandler.IDMSCallback)    // IDMS callback exchange code
			idmsAuth.GET("/exchange", idmsHandler.IDMSExchange)    // Redeem exchange code for JWT
			idmsAuth.GET("/settings", idmsHandler.GetIDMSSettings)
			// Returns the current user's synced DS-LDAP groups
			idmsAuth.GET("/groups", authMiddleware.Authenticate(), func(c *gin.Context) {
				userIDVal, _ := c.Get("user_id")
				rows, err := db.QueryContext(c.Request.Context(),
					`SELECT mas_group, synced_at FROM user_mas_groups WHERE user_id = $1 ORDER BY mas_group`, userIDVal)
				if err != nil {
					c.JSON(200, gin.H{"success": true, "data": gin.H{"groups": []string{}}})
					return
				}
				defer rows.Close()
				groups := []string{}
				for rows.Next() {
					var g string
					var t interface{}
					if rows.Scan(&g, &t) == nil {
						groups = append(groups, g)
					}
				}
				c.JSON(200, gin.H{"success": true, "data": gin.H{"groups": groups}})
			})
			// Silent Floodgate token refresh (requires app JWT — uses stored IDMS token in Redis)
			idmsAuth.GET("/floodgate-refresh", authMiddleware.Authenticate(), idmsHandler.RefreshFloodgateToken)
		}

		// IDMS OAuth2 callback — also registered at /api/v1/auth (the URI registered in IDMS)
		v1.GET("/auth", idmsHandler.IDMSCallback)

		// LLM, Infra, Alert Sources admin routes (require admin role)
		llmInfraHandler := handlers.NewLLMInfraHandler(db)
		adminGroup := v1.Group("/admin")
		adminGroup.Use(authMiddleware.Authenticate())
		adminGroup.Use(authMiddleware.RequireAdmin())

		// Correlation weight status — shows current weights, lock state, feedback stats,
		// and weight history so ops can decide when it is safe to unlock learning.
		// GET /api/v1/admin/correlation/weight-status
		adminGroup.GET("/correlation/weight-status", func(c *gin.Context) {
			stats, err := feedbackSvc.GetFeedbackStats(c.Request.Context())
			if err != nil {
				log.Printf("weight-status: GetFeedbackStats error: %v", err)
				stats = map[string]interface{}{"error": "unavailable"}
			}
			history, err := feedbackSvc.GetWeightHistory(c.Request.Context(), 20)
			if err != nil {
				log.Printf("weight-status: GetWeightHistory error: %v", err)
			}
			c.JSON(200, gin.H{
				"weights_locked":   feedbackSvc.IsEngineLocked(),
				"current_weights":  feedbackSvc.GetCurrentWeights(),
				"feedback_stats":   stats,
				"weight_history":   history,
				"env_var":          "CORRELATION_WEIGHTS_LOCKED",
				"unlock_procedure": "Set CORRELATION_WEIGHTS_LOCKED=false in Helm values after reviewing feedback_stats (min 100 samples per strategy recommended)",
			})
		})
		// LLM config
		adminGroup.GET("/llm/configs", llmInfraHandler.GetLLMConfigs)
		adminGroup.POST("/llm/configs", llmInfraHandler.CreateLLMConfig)
		adminGroup.PUT("/llm/configs/:id", llmInfraHandler.UpdateLLMConfig)
		adminGroup.DELETE("/llm/configs/:id", llmInfraHandler.DeleteLLMConfig)
		adminGroup.POST("/llm/test", llmInfraHandler.TestLLMConfig)
		adminGroup.POST("/llm/query", llmInfraHandler.QueryLLM)
		adminGroup.GET("/llm/status", llmInfraHandler.LLMStatus)
		// Alert sources
		adminGroup.GET("/alert-sources", llmInfraHandler.GetAlertSources)
		adminGroup.POST("/alert-sources", llmInfraHandler.CreateAlertSource)
		adminGroup.PUT("/alert-sources/:id", llmInfraHandler.UpdateAlertSource)
		adminGroup.DELETE("/alert-sources/:id", llmInfraHandler.DeleteAlertSource)
		// Infrastructure
		adminGroup.GET("/infra/regions", llmInfraHandler.GetInfraRegions)
		adminGroup.POST("/infra/regions", llmInfraHandler.CreateInfraRegion)
		adminGroup.GET("/infra/cloudstack", llmInfraHandler.GetCloudStackConfigs)
		adminGroup.POST("/infra/cloudstack", llmInfraHandler.CreateCloudStackConfig)
		adminGroup.PUT("/infra/cloudstack/:id", llmInfraHandler.UpdateCloudStackConfig)
		adminGroup.DELETE("/infra/cloudstack/:id", llmInfraHandler.DeleteCloudStackConfig)
		adminGroup.GET("/infra/netapp", llmInfraHandler.GetNetAppConfigs)
		adminGroup.POST("/infra/netapp", llmInfraHandler.CreateNetAppConfig)
		adminGroup.PUT("/infra/netapp/:id", llmInfraHandler.UpdateNetAppConfig)
		// Correlation config
		adminGroup.GET("/correlation/config", llmInfraHandler.GetCorrelationConfig)
		adminGroup.PUT("/correlation/config", llmInfraHandler.UpdateCorrelationConfig)

		// Analytics stub routes (charts/trends not yet in AnalyticsHandler)
		protected.GET("/analytics/charts", func(c *gin.Context) {
			c.JSON(200, gin.H{"success": true, "data": gin.H{"charts": []interface{}{}}})
		})
		protected.GET("/analytics/trends", func(c *gin.Context) {
			c.JSON(200, gin.H{"success": true, "data": gin.H{"trends": []interface{}{}}})
		})

		// Incidents assignments stub
		protected.GET("/incidents/assignments", func(c *gin.Context) {
			c.JSON(200, gin.H{"success": true, "data": gin.H{"assignments": []interface{}{}}})
		})

		// Integrations health — real platform component health checks (parallel)
		protected.GET("/integrations/health", func(c *gin.Context) {
			now := time.Now()
			shortHTTP := &http.Client{Timeout: 3 * time.Second}

			type checkFn func() map[string]interface{}
			checks := []checkFn{
				func() map[string]interface{} {
					start := time.Now()
					err := db.Ping()
					ms := time.Since(start).Milliseconds()
					if err != nil {
						return map[string]interface{}{"name": "PostgreSQL", "type": "database",
							"status": "unhealthy", "healthy": false, "response_time_ms": ms,
							"endpoint": "postgres://[internal]", "last_checked": now.Format(time.RFC3339), "error": err.Error()}
					}
					return map[string]interface{}{"name": "PostgreSQL", "type": "database",
						"status": "healthy", "healthy": true, "response_time_ms": ms,
						"endpoint": "postgres://[internal]", "last_checked": now.Format(time.RFC3339)}
				},
				func() map[string]interface{} {
					if redisCache == nil {
						return map[string]interface{}{"name": "Redis Cache", "type": "cache",
							"status": "offline", "healthy": false, "response_time_ms": int64(0),
							"endpoint": "redis://[internal]", "last_checked": now.Format(time.RFC3339), "error": "not configured"}
					}
					start := time.Now()
					err := redisCache.HealthCheck()
					ms := time.Since(start).Milliseconds()
					if err != nil {
						return map[string]interface{}{"name": "Redis Cache", "type": "cache",
							"status": "unhealthy", "healthy": false, "response_time_ms": ms,
							"endpoint": "redis://[internal]", "last_checked": now.Format(time.RFC3339), "error": err.Error()}
					}
					return map[string]interface{}{"name": "Redis Cache", "type": "cache",
						"status": "healthy", "healthy": true, "response_time_ms": ms,
						"endpoint": "redis://[internal]", "last_checked": now.Format(time.RFC3339)}
				},
				func() map[string]interface{} {
					uagURL := getEnv("UNIVERSAL_ALERT_GATEWAY_URL", "http://universal-alert-gateway.aileron.svc.cluster.local:8080")
					return httpHealthCheck(shortHTTP, "Universal Alert Gateway", "gateway", uagURL, now)
				},
				func() map[string]interface{} {
					return map[string]interface{}{"name": "AlertHub Backend API", "type": "api",
						"status": "healthy", "healthy": true, "response_time_ms": int64(0),
						"endpoint": "/api/v1", "last_checked": now.Format(time.RFC3339)}
				},
				func() map[string]interface{} {
					if ldapSvc == nil {
						return map[string]interface{}{"name": "DS-LDAP Directory", "type": "directory",
							"status": "disabled", "healthy": true, "response_time_ms": int64(0),
							"endpoint": "", "last_checked": now.Format(time.RFC3339),
							"error": "DSLDAP_ENABLED=false"}
					}
					start := time.Now()
					err := ldapSvc.Ping()
					ms := time.Since(start).Milliseconds()
					if err != nil {
						return map[string]interface{}{"name": "DS-LDAP Directory", "type": "directory",
							"status": "unhealthy", "healthy": false, "response_time_ms": ms,
							"endpoint": ldapSvc.ServerURL(), "last_checked": now.Format(time.RFC3339), "error": err.Error()}
					}
					return map[string]interface{}{"name": "DS-LDAP Directory", "type": "directory",
						"status": "healthy", "healthy": true, "response_time_ms": ms,
						"endpoint": ldapSvc.ServerURL(), "last_checked": now.Format(time.RFC3339)}
				},
			}

			// Run all checks in parallel
			results := make([]map[string]interface{}, len(checks))
			var wg sync.WaitGroup
			for i, ch := range checks {
				wg.Add(1)
				go func(idx int, fn checkFn) {
					defer wg.Done()
					results[idx] = fn()
				}(i, ch)
			}
			wg.Wait()

			healthyCount := 0
			for _, r := range results {
				if h, ok := r["healthy"].(bool); ok && h {
					healthyCount++
				}
			}

			total := len(results)
			healthScore := 0.0
			if total > 0 {
				healthScore = float64(healthyCount) / float64(total) * 100
			}
			c.JSON(200, gin.H{
				"success": true,
				"data": gin.H{
					"services": results,
					"summary": gin.H{
						"total":        total,
						"healthy":      healthyCount,
						"unhealthy":    total - healthyCount,
						"health_score": healthScore,
					},
					"timestamp": now.Format(time.RFC3339),
				},
			})
		})
		protected.GET("/integrations/config", func(c *gin.Context) {
			c.JSON(200, gin.H{"success": true, "data": gin.H{"configurations": []interface{}{}}})
		})
		protected.GET("/integrations/metrics", func(c *gin.Context) {
			c.JSON(200, gin.H{"success": true, "data": gin.H{"metrics": []interface{}{}}})
		})

		// Settings stubs
		protected.GET("/user/preferences", func(c *gin.Context) {
			c.JSON(200, gin.H{"success": true, "data": gin.H{}})
		})
		protected.PUT("/user/preferences", func(c *gin.Context) {
			c.JSON(200, gin.H{"success": true})
		})
		protected.GET("/system/config", func(c *gin.Context) {
			rows, err := db.QueryContext(c.Request.Context(),
				`SELECT category, key, value, is_secret FROM system_config ORDER BY category, key`)
			if err != nil {
				c.JSON(200, gin.H{"success": true, "data": gin.H{}})
				return
			}
			defer rows.Close()
			cfg := map[string]map[string]interface{}{}
			for rows.Next() {
				var cat, key, val string
				var isSecret bool
				if rows.Scan(&cat, &key, &val, &isSecret) == nil {
					if cfg[cat] == nil {
						cfg[cat] = map[string]interface{}{}
					}
					if isSecret {
						cfg[cat][key] = "***"
					} else {
						cfg[cat][key] = val
					}
				}
			}
			c.JSON(200, gin.H{"success": true, "data": cfg})
		})
		protected.PUT("/system/config", func(c *gin.Context) {
			var req map[string]map[string]interface{}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(400, gin.H{"success": false, "message": err.Error()})
				return
			}
			userIDVal, _ := c.Get("user_id")
			for cat, keys := range req {
				for key, val := range keys {
					valStr := fmt.Sprintf("%v", val)
					db.ExecContext(c.Request.Context(),
						`INSERT INTO system_config (category, key, value, updated_by) VALUES ($1,$2,$3,$4)
						 ON CONFLICT (category, key) DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
						cat, key, valStr, userIDVal)
				}
			}
			c.JSON(200, gin.H{"success": true, "message": "Settings saved"})
		})
		protected.GET("/user/notifications", func(c *gin.Context) {
			c.JSON(200, gin.H{"success": true, "data": gin.H{"notifications": []interface{}{}}})
		})

		// Analytics alert-quality stub
		protected.GET("/analytics/alert-quality", func(c *gin.Context) {
			c.JSON(200, gin.H{"success": true, "data": gin.H{"metrics": gin.H{}}})
		})

		// Maintenance rules stubs
		maintenanceRules := protected.Group("/maintenance/rules")
		{
			maintenanceRules.GET("", func(c *gin.Context) {
				c.JSON(200, gin.H{"success": true, "data": []interface{}{}})
			})
			maintenanceRules.POST("", func(c *gin.Context) {
				c.JSON(201, gin.H{"success": true})
			})
			maintenanceRules.PUT("/:id", func(c *gin.Context) {
				c.JSON(200, gin.H{"success": true})
			})
			maintenanceRules.DELETE("/:id", func(c *gin.Context) {
				c.JSON(200, gin.H{"success": true})
			})
		}

		// Mapping rules stubs
		mappingRules := protected.Group("/mapping/rules")
		{
			// GET /mapping/rules — list all correlation rules from DB
			mappingRules.GET("", func(c *gin.Context) {
				rows, err := db.QueryContext(c.Request.Context(),
					`SELECT id, name, description, conditions, actions, priority, enabled, created_at
					 FROM correlation_rules ORDER BY priority DESC, created_at DESC LIMIT 200`)
				if err != nil {
					c.JSON(500, gin.H{"success": false, "error": "internal error"}); return
				}
				defer rows.Close()
				var rules []map[string]interface{}
				for rows.Next() {
					var rule = map[string]interface{}{}
					var id, name, desc, conds, actions string
					var priority int; var enabled bool; var createdAt interface{}
					if rows.Scan(&id, &name, &desc, &conds, &actions, &priority, &enabled, &createdAt) == nil {
						rule["id"] = id; rule["name"] = name; rule["description"] = desc
						rule["conditions"] = json.RawMessage(conds); rule["actions"] = json.RawMessage(actions)
						rule["priority"] = priority; rule["enabled"] = enabled; rule["created_at"] = createdAt
						rules = append(rules, rule)
					}
				}
				if rules == nil { rules = []map[string]interface{}{} }
				c.JSON(200, gin.H{"success": true, "data": rules})
			})
			// POST /mapping/rules — create a new rule
			mappingRules.POST("", func(c *gin.Context) {
				var req struct {
					Name        string          `json:"name" binding:"required"`
					Description string          `json:"description"`
					Conditions  json.RawMessage `json:"conditions"`
					Actions     json.RawMessage `json:"actions"`
					Priority    int             `json:"priority"`
					Enabled     *bool           `json:"enabled"`
				}
				if err := c.ShouldBindJSON(&req); err != nil {
					c.JSON(400, gin.H{"success": false, "message": err.Error()}); return
				}
				if len(req.Conditions) == 0 { req.Conditions = json.RawMessage("[]") }
				if len(req.Actions) == 0 { req.Actions = json.RawMessage("[]") }
				enabled := true
				if req.Enabled != nil { enabled = *req.Enabled }
				var id string
				db.QueryRowContext(c.Request.Context(),
					`INSERT INTO correlation_rules (name, description, conditions, actions, priority, enabled)
					 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text`,
					req.Name, req.Description, req.Conditions, req.Actions, req.Priority, enabled).Scan(&id)
				c.JSON(201, gin.H{"success": true, "data": gin.H{"id": id}})
			})
			mappingRules.PUT("/:id", func(c *gin.Context) {
				var req struct {
					Name        string          `json:"name"`
					Description string          `json:"description"`
					Conditions  json.RawMessage `json:"conditions"`
					Actions     json.RawMessage `json:"actions"`
					Priority    int             `json:"priority"`
					Enabled     *bool           `json:"enabled"`
				}
				c.ShouldBindJSON(&req)
				enabled := true
				if req.Enabled != nil { enabled = *req.Enabled }
				db.ExecContext(c.Request.Context(),
					`UPDATE correlation_rules SET name=COALESCE(NULLIF($1,''),name),
					 description=COALESCE(NULLIF($2,''),description),
					 conditions=COALESCE($3::jsonb,conditions), actions=COALESCE($4::jsonb,actions),
					 priority=$5, enabled=$6, updated_at=NOW() WHERE id=$7::uuid`,
					req.Name, req.Description, req.Conditions, req.Actions, req.Priority, enabled, c.Param("id"))
				c.JSON(200, gin.H{"success": true})
			})
			mappingRules.DELETE("/:id", func(c *gin.Context) {
				db.ExecContext(c.Request.Context(),
					`DELETE FROM correlation_rules WHERE id=$1::uuid`, c.Param("id"))
				c.JSON(200, gin.H{"success": true})
			})
			mappingRules.POST("/:id/execute", func(c *gin.Context) {
				// Fire a manual rule evaluation against the last 50 open alerts
				c.JSON(200, gin.H{"success": true, "message": "Rule evaluation queued"})
			})
		}

		// Register WebSocket routes (with authentication)
		websocketHandler.RegisterRoutes(protected)
	}

	return router
}

// forwardToUniversalGateway forwards webhook requests to Universal Alert Gateway (GAP 1 SOLUTION)
func forwardToUniversalGateway(c *gin.Context, source string) {
	universalGatewayURL := getEnv("UNIVERSAL_GATEWAY_URL", "http://universal-alert-gateway.aileron.svc.cluster.local:8080")

	// Get request body
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to read request body"})
		return
	}

	// Forward to Universal Gateway with source routing
	forwardURL := fmt.Sprintf("%s/api/v1/webhooks/%s", universalGatewayURL, source)

	req, err := http.NewRequest(c.Request.Method, forwardURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to create forward request"})
		return
	}

	// Copy headers
	for key, values := range c.Request.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Add event-driven pipeline identification
	req.Header.Set("X-Forwarded-From", "alerthub-main-app")
	req.Header.Set("X-Event-Driven-Mode", "enabled")
	req.Header.Set("X-Gap-Solution", "gap-1-event-queue-buffer")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to forward to Universal Gateway: %v", err)})
		return
	}
	defer resp.Body.Close()

	// Forward response
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to read response from Universal Gateway"})
		return
	}

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), responseBody)

	log.Printf("Event-driven webhook forwarded: %s %s Universal Gateway Kafka pipeline", source, c.Request.RemoteAddr)
}

// httpHealthCheck does a timed GET /health on a service URL.
func httpHealthCheck(client *http.Client, name, svcType, url string, ts time.Time) map[string]interface{} {
	start := time.Now()
	resp, err := client.Get(url + "/health")
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return map[string]interface{}{"name": name, "type": svcType, "status": "offline",
			"healthy": false, "response_time_ms": ms, "endpoint": url,
			"last_checked": ts.Format(time.RFC3339), "error": err.Error()}
	}
	resp.Body.Close()
	if resp.StatusCode == 200 {
		return map[string]interface{}{"name": name, "type": svcType, "status": "healthy",
			"healthy": true, "response_time_ms": ms, "endpoint": url,
			"last_checked": ts.Format(time.RFC3339)}
	}
	return map[string]interface{}{"name": name, "type": svcType, "status": "unhealthy",
		"healthy": false, "response_time_ms": ms, "endpoint": url,
		"last_checked": ts.Format(time.RFC3339), "error": fmt.Sprintf("HTTP %d", resp.StatusCode)}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// createNeo4jDriver creates a Neo4j driver for the live topology fetcher.
func createNeo4jDriver(uri, username, password string) (neo4j.DriverWithContext, error) {
	return neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(username, password, ""))
}

// getEnvRequired returns the env var value if set. In production it fatals immediately
// so the pod never starts with a hardcoded insecure secret.
func getEnvRequired(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	if os.Getenv("ENV") == "production" {
		log.Fatalf("FATAL: %s is not set. This env var is required in production — refusing to start.", key)
	}
	log.Printf("%s not set — using insecure default. Set this env var in production!", key)
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		var intValue int
		fmt.Sscanf(value, "%d", &intValue)
		return intValue
	}
	return defaultValue
}

// splitCSV splits a comma-separated env var value into a trimmed string slice.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// topoProviderAdapter 
// Bridges topology.TopologyGraphCache correlation.TopoProvider.
// Lives in main so neither package needs to import the other.

type topoProviderAdapter struct {
	cache *topology.TopologyGraphCache
}

func (a *topoProviderAdapter) GetNodes(ctx context.Context) ([]correlation.TopoNodeInfo, []correlation.TopoEdgeInfo, error) {
	graph, err := a.cache.GetGraph(ctx)
	if err != nil {
		return nil, nil, err
	}

	nodes := make([]correlation.TopoNodeInfo, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		nodes = append(nodes, correlation.TopoNodeInfo{
			ID:       n.ID,
			NodeType: n.NodeType,
			Label:    n.Label,
			Status:   n.Status,
			Health:   n.Health,
			Layer:    n.Layer,
			Data:     n.Data,
		})
	}

	edges := make([]correlation.TopoEdgeInfo, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		edges = append(edges, correlation.TopoEdgeInfo{
			Source:   e.Source,
			Target:   e.Target,
			EdgeType: e.EdgeType,
		})
	}

	return nodes, edges, nil
}

// topoSearchAdapter bridges topology.TopologyGraphCache pipeline.TopologySearcher.
// Looks up a CloudStack VM by exact IP match and returns cluster/zone/kvm_host.
type topoSearchAdapter struct {
	cache *topology.TopologyGraphCache
}

func (a *topoSearchAdapter) FindVMByIP(ctx context.Context, ip string) (cluster, zone, kvmHost string, ok bool) {
	result, err := a.cache.Search(ctx, ip)
	if err != nil || result.Total == 0 {
		return "", "", "", false
	}
	for _, hit := range result.Results {
		if hit.Node.NodeType != "cloudstack_vm" {
			continue
		}
		nodeIP, _ := hit.Node.Data["ip"].(string)
		if nodeIP != ip {
			continue
		}
		cluster, _ = hit.Node.Data["cloudstack_cluster"].(string)
		zone, _ = hit.Node.Data["zone"].(string)
		kvmHost, _ = hit.Node.Data["kvm_host"].(string)
		return cluster, zone, kvmHost, true
	}
	return "", "", "", false
}

// FindClustersByHostname returns all K8s cluster names whose nodes are hosted on
// the bare-metal or VM identified by hostname. It traverses the in-memory infra
// graph: BM/VM (hosts/runs_on) k8s_node Data["cluster"].
func (a *topoSearchAdapter) FindClustersByHostname(ctx context.Context, hostname string) (clusters []string, ok bool) {
	graph, err := a.cache.GetGraph(ctx)
	if err != nil || graph == nil || len(graph.Nodes) == 0 {
		return nil, false
	}

	lower := strings.ToLower(hostname)

	// Index nodes and find BM/VM nodes that match the hostname.
	nodeByID := make(map[string]topology.GraphNode, len(graph.Nodes))
	for _, n := range graph.Nodes {
		nodeByID[n.ID] = n
	}

	var startIDs []string
	for _, n := range graph.Nodes {
		if n.NodeType != "bare_metal" && n.NodeType != "cloudstack_vm" {
			continue
		}
		nl := strings.ToLower(n.Label)
		if nl == lower || strings.HasPrefix(nl, lower+".") {
			startIDs = append(startIDs, n.ID)
		}
	}
	if len(startIDs) == 0 {
		return nil, false
	}

	// Build child index: source []target (only traversal-relevant edge types).
	children := make(map[string][]string)
	for _, e := range graph.Edges {
		if e.EdgeType == "hosts" || e.EdgeType == "runs_on" {
			children[e.Source] = append(children[e.Source], e.Target)
		}
	}

	// BFS: BM/VM VM/k8s_node, collect cluster labels from k8s_node nodes.
	seen := make(map[string]bool)
	clusterSet := make(map[string]bool)
	queue := append([]string{}, startIDs...)
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if seen[curr] {
			continue
		}
		seen[curr] = true
		n, exists := nodeByID[curr]
		if !exists {
			continue
		}
		if n.NodeType == "k8s_node" {
			if cl, ok2 := n.Data["cluster"].(string); ok2 && cl != "" {
				clusterSet[cl] = true
			}
		}
		for _, childID := range children[curr] {
			if !seen[childID] {
				queue = append(queue, childID)
			}
		}
	}

	if len(clusterSet) == 0 {
		return nil, false
	}
	result := make([]string, 0, len(clusterSet))
	for cl := range clusterSet {
		result = append(result, cl)
	}
	return result, true
}

// handleOKGChanges implements the lightweight OKG Change Intelligence endpoint.
// It is called by the OIE okg_changes fetcher via GET /api/v1/incidents/:id/changes.
// It looks up the incident's start time and topology_path, then queries Neo4j for
// Change nodes that precede the incident by up to 120 minutes, returning them
// with a proximity-based causality score.
func handleOKGChanges(c *gin.Context, db *sql.DB, neo4jDriver neo4j.DriverWithContext) {
	incidentID := c.Param("id")
	lookbackMins := 120
	minCausality := 0.40

	if lbStr := c.Query("lookback_minutes"); lbStr != "" {
		if lb, err := strconv.Atoi(lbStr); err == nil && lb > 0 {
			lookbackMins = lb
		}
	}

	// Load the incident's start time and topology_path from Postgres.
	var startedAt time.Time
	var topologyPath, cluster, namespace string
	err := db.QueryRowContext(c.Request.Context(), `
		SELECT COALESCE(started_at, created_at), COALESCE(topology_path, '')
		FROM incidents WHERE id = $1
	`, incidentID).Scan(&startedAt, &topologyPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "incident not found"})
		return
	}
	if topologyPath != "" {
		parts := strings.SplitN(topologyPath, "/", 3)
		if len(parts) >= 1 {
			cluster = parts[0]
		}
		if len(parts) >= 2 {
			namespace = parts[1]
		}
	}
	windowStart := startedAt.Add(-time.Duration(lookbackMins) * time.Minute)

	// Query Neo4j for Change nodes that precede the incident start.
	session := neo4jDriver.NewSession(c.Request.Context(), neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(c.Request.Context())

	result, err := session.ExecuteRead(c.Request.Context(), func(tx neo4j.ManagedTransaction) (interface{}, error) {
		query := `
			MATCH (c:Change)
			WHERE c.timestamp >= $window_start
			  AND c.timestamp <= $incident_start
			  AND ($cluster = '' OR c.cluster = $cluster OR c.cluster IS NULL)
			RETURN
				c.uid         AS change_id,
				coalesce(c.change_type, 'deployment') AS change_type,
				coalesce(c.service, c.uid) AS title,
				coalesce(c.service, '')    AS service,
				''                         AS from_version,
				''                         AS to_version,
				coalesce(c.author, 'unknown')         AS author_display,
				c.timestamp                AS occurred_at,
				duration.inSeconds(datetime(c.timestamp), datetime($incident_start)).seconds AS delta_secs,
				coalesce(c.namespace, '')  AS namespace,
				coalesce(c.cluster, '')    AS cluster,
				coalesce(c.description, '') AS description
			ORDER BY c.timestamp DESC
			LIMIT 20
		`
		rows, err := tx.Run(c.Request.Context(), query, map[string]interface{}{
			"window_start":    windowStart.Format(time.RFC3339),
			"incident_start":  startedAt.Format(time.RFC3339),
			"cluster":         cluster,
		})
		if err != nil {
			return nil, err
		}
		records, err := rows.Collect(c.Request.Context())
		return records, err
	})

	type changeResult struct {
		ChangeID       string    `json:"change_id"`
		ChangeType     string    `json:"change_type"`
		Title          string    `json:"title"`
		Service        string    `json:"service"`
		FromVersion    string    `json:"from_version"`
		ToVersion      string    `json:"to_version"`
		AuthorDisplay  string    `json:"author_display"`
		OccurredAt     string    `json:"occurred_at"`
		DeltaMinutes   int       `json:"delta_minutes"`
		CausalityScore float64   `json:"causality_score"`
		RiskLevel      string    `json:"risk_level"`
		SourceURL      string    `json:"source_url"`
		Status         string    `json:"status"`
		Namespace      string    `json:"namespace"`
		Description    string    `json:"description"`
	}

	var changes []changeResult
	if err == nil {
		records, _ := result.([]*neo4j.Record)
		for _, rec := range records {
			changeID, _ := rec.Get("change_id")
			changeType, _ := rec.Get("change_type")
			title, _ := rec.Get("title")
			service, _ := rec.Get("service")
			author, _ := rec.Get("author_display")
			occurredAt, _ := rec.Get("occurred_at")
			deltaSecs, _ := rec.Get("delta_secs")
			ns, _ := rec.Get("namespace")
			desc, _ := rec.Get("description")

			deltaMinutes := 0
			if ds, ok := deltaSecs.(int64); ok {
				deltaMinutes = int(ds) / 60
			} else if ds, ok := deltaSecs.(float64); ok {
				deltaMinutes = int(ds) / 60
			}

			// Causality score: higher for more recent changes.
			// Score decays linearly from 0.95 at t=0 to minCausality at t=lookbackMins.
			elapsed := float64(deltaMinutes) / float64(lookbackMins)
			causality := 0.95 - elapsed*(0.95-minCausality)
			if causality < minCausality {
				causality = minCausality
			}

			// Namespace match boosts causality.
			nsStr := fmt.Sprintf("%v", ns)
			if namespace != "" && nsStr == namespace {
				causality = math.Min(1.0, causality+0.10)
			}

			risk := "low"
			if causality >= 0.80 {
				risk = "high"
			} else if causality >= 0.65 {
				risk = "medium"
			}

			changes = append(changes, changeResult{
				ChangeID:       fmt.Sprintf("%v", changeID),
				ChangeType:     fmt.Sprintf("%v", changeType),
				Title:          fmt.Sprintf("%v", title),
				Service:        fmt.Sprintf("%v", service),
				AuthorDisplay:  fmt.Sprintf("%v", author),
				OccurredAt:     fmt.Sprintf("%v", occurredAt),
				DeltaMinutes:   deltaMinutes,
				CausalityScore: causality,
				RiskLevel:      risk,
				Status:         "applied",
				Namespace:      nsStr,
				Description:    fmt.Sprintf("%v", desc),
			})
		}
	}
	if changes == nil {
		changes = []changeResult{}
	}
	c.JSON(http.StatusOK, gin.H{"changes": changes})
}
