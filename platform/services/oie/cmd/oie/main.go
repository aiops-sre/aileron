package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	appinv "github.com/aileron-platform/aileron/platform/services/oie/internal/application/investigation"
	appevidence "github.com/aileron-platform/aileron/platform/services/oie/internal/application/evidence"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/config"
	pgEvidence "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/postgres/evidence"
	pgInvestigation "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/postgres/investigation"
	pgHypothesis "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/postgres/hypothesis"
	approca "github.com/aileron-platform/aileron/platform/services/oie/internal/application/rca"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/ollama"
	apphypothesis "github.com/aileron-platform/aileron/platform/services/oie/internal/application/hypothesis"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/kafka"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/circuitbreaker"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
	fetchk8s "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers/kubernetes"
	fetchcs "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers/cloudstack"
	fetchnetapp "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers/netapp"
	fetchokg "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers/okg"
	fetcheirs "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers/eirs"
	fetchks "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers/kubesense"
	fetchpast "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers/pastinvestigations"
	fetchrb "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers/runbooks"
	httpports "github.com/aileron-platform/aileron/platform/services/oie/internal/ports/http"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/tracing"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: true,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	shutdownTracer, err := tracing.InitTracer(ctx, tracing.TracerConfig{
		ServiceName:    cfg.ServiceName,
		ServiceVersion: cfg.ServiceVersion,
		JaegerEndpoint: cfg.JaegerEndpoint,
		Environment:    cfg.Environment,
	})
	if err != nil {
		logger.Error("failed to initialize tracer", "error", err)
		os.Exit(1)
	}

	db, err := openDB(cfg)
	if err != nil {
		logger.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Apply idempotent schema additions — catches columns added after initial deploy.
	// These mirror the migration files in services/oie/migrations/ and are safe to
	// run on every startup (IF NOT EXISTS / ADD COLUMN IF NOT EXISTS).
	for _, stmt := range []string{
		`ALTER TABLE investigations ADD COLUMN IF NOT EXISTS citations_json JSONB DEFAULT NULL`,
	} {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			logger.Warn("schema migration warning (non-fatal)", "stmt", stmt[:40], "error", err)
		}
	}

	var redisClient *redis.Client
	if cfg.RedisAddr != "" {
		redisClient = redis.NewClient(&redis.Options{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
			DB:       0,
		})
		if pingErr := redisClient.Ping(ctx).Err(); pingErr != nil {
			logger.Warn("redis unavailable — circuit breakers in local-only mode", "error", pingErr)
			redisClient = nil
		} else {
			defer redisClient.Close()
		}
	}

	invRepo := pgInvestigation.NewRepository(db)
	evRepo := pgEvidence.NewRepository(db)
	cb := circuitbreaker.NewCircuitBreaker(redisClient, logger)

	fetcherMap := buildFetcherMap(cfg, db, logger)
	playbook := appevidence.NewPlaybookRegistry()
	evidenceBus := appevidence.NewBus(fetcherMap, playbook, evRepo, cb, logger)
	// Wire Postgres for topology-based entity enrichment (K8s node → CloudStack VM chain).
	evidenceBus.SetDB(db)
	// Wire AlertHub base URL so the evidence bus can use the live Neo4j + Redis topology
	// cache via GET /api/v1/topology/resolve. This enriches the EntityProfile with the
	// actual pod→node→VM chain from real infrastructure, preventing empty profiles that
	// caused evidence fetchers to return no data and the scorer to pick wrong winners.
	if cfg.AlertHubBaseURL != "" {
		evidenceBus.SetAlertHubURL(cfg.AlertHubBaseURL)
		logger.Info("evidence bus: topology resolve wired via AlertHub Neo4j+Redis cache",
			"url", cfg.AlertHubBaseURL)
	}

	publisher, err := kafka.NewEventPublisher(cfg.KafkaBrokers, cfg.KafkaInvestigationsTopic, logger)
	if err != nil {
		logger.Error("failed to create kafka event publisher", "error", err)
		os.Exit(1)
	}
	defer publisher.Close()

	manager := appinv.NewManager(
		invRepo,
		evidenceBus,
		apphypothesis.NewEngine(pgHypothesis.NewRepository(db), evRepo, logger),
		buildRCAGenerator(cfg, db, evRepo, logger),
		publisher,
		logger,
		appinv.ManagerConfig{
			PodID:         cfg.PodID,
			TimeBudgetMs:  cfg.InvestigationTimeBudgetMs,
			MaxConcurrent: cfg.MaxConcurrentInvestigations,
		},
	)

	rootCtx, cancelRoot := context.WithCancel(ctx)
	defer cancelRoot()
	if cfg.AlertHubBaseURL != "" {
		manager.SetAlertHubURL(cfg.AlertHubBaseURL)
		logger.Info("AlertHub RCA writeback enabled", "url", cfg.AlertHubBaseURL)
	}
	if cfg.OllamaBaseURL != "" {
		manager.SetDB(db)
		manager.SetOllamaURL(cfg.OllamaBaseURL)
		logger.Info("semantic embeddings enabled for past-investigation matching",
			"model", "nomic-embed-text", "ollama", cfg.OllamaBaseURL)
	}
	manager.StartRecoveryLoop(rootCtx)

	consumer, err := kafka.NewConsumer(manager, invRepo, kafka.ConsumerConfig{
		Brokers:                   cfg.KafkaBrokers,
		ConsumerGroupID:           cfg.KafkaConsumerGroupID,
		AutoInvestigateSeverities: cfg.AutoInvestigateSeverities,
		// PolicyEvalURL: when AlertHubBaseURL is configured, OIE respects skip_rca
		// policies from AlertHub's intelligence_policies table before starting investigations.
		PolicyEvalURL: cfg.AlertHubBaseURL,
	}, logger)
	if err != nil {
		logger.Error("failed to create kafka consumer", "error", err)
		os.Exit(1)
	}

	go func() {
		if err := consumer.Run(rootCtx); err != nil && rootCtx.Err() == nil {
			logger.Error("kafka consumer exited unexpectedly", "error", err)
		}
	}()

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLoggingMiddleware(logger))

	handler := httpports.NewHandler(manager, invRepo, logger)
	router.GET("/healthz", handler.Healthz)
	router.GET("/readyz", handler.Readyz)
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))
	v1 := router.Group("/api/v1")
	handler.RegisterRoutes(v1)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  90 * time.Second,
	}

	go func() {
		logger.Info("oie starting", "port", cfg.Port, "pod_id", cfg.PodID)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("oie shutting down")
	cancelRoot()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", "error", err)
	}
	if err := consumer.Close(); err != nil {
		logger.Error("kafka consumer close error", "error", err)
	}
	if err := shutdownTracer(shutdownCtx); err != nil {
		logger.Error("tracer shutdown error", "error", err)
	}
	logger.Info("oie stopped")
}

func buildFetcherMap(cfg *config.Config, db *sql.DB, logger *slog.Logger) map[fetchers.FetcherID]fetchers.EvidenceFetcher {
	fm := make(map[fetchers.FetcherID]fetchers.EvidenceFetcher)

	if cfg.EIRSBaseURL != "" {
		f := fetcheirs.NewEntityContextFetcher(cfg.EIRSBaseURL)
		fm[f.ID()] = f
	}

	if cfg.OKGBaseURL != "" {
		cf := fetchokg.NewChangesFetcher(cfg.OKGBaseURL)
		fm[cf.ID()] = cf
		sf := fetchokg.NewSimilarIncidentsFetcher(cfg.OKGBaseURL)
		fm[sf.ID()] = sf
	}

	if cfg.KubeconfigsDir != "" {
		kubeconfigPaths, err := loadKubeconfigPaths(cfg.KubeconfigsDir)
		if err == nil && len(kubeconfigPaths) > 0 {
			nodeFetcher, err := fetchk8s.NewNodeConditionsFetcher(kubeconfigPaths)
			if err == nil {
				fm[nodeFetcher.ID()] = nodeFetcher
				k8sClients := nodeFetcher.Clients()
				if len(k8sClients) > 0 {
					podExit := fetchk8s.NewPodExitCodeFetcher(k8sClients)
					fm[podExit.ID()] = podExit
					podEvents := fetchk8s.NewPodEventsFetcher(k8sClients)
					fm[podEvents.ID()] = podEvents
					pvcCapacity := fetchk8s.NewPVCCapacityFetcher(k8sClients)
					fm[pvcCapacity.ID()] = pvcCapacity
					// K8sGPT pattern: PDB detector + Service-Endpoint probe
					pdbFetcher := fetchk8s.NewPDBFetcher(k8sClients)
					fm[pdbFetcher.ID()] = pdbFetcher
					endpointFetcher := fetchk8s.NewServiceEndpointFetcher(k8sClients)
					fm[endpointFetcher.ID()] = endpointFetcher
				}
			}
		}
	}

	// CloudStack: load from env vars first, then fall back to DB.
	csConfigs := loadCloudStackConfigs()
	if len(csConfigs) == 0 {
		csConfigs = loadCloudStackConfigsFromDB(db, logger)
	}
	if len(csConfigs) > 0 {
		vmFetcher := fetchcs.NewVMStateFetcher(csConfigs)
		fm[vmFetcher.ID()] = vmFetcher
		hostFetcher := fetchcs.NewHostStateFetcher(csConfigs)
		fm[hostFetcher.ID()] = hostFetcher
	}

	// NetApp ONTAP: load cluster configs from env (NETAPP_CLUSTERS JSON) + credentials.
	netappClusters := loadNetAppConfigs(cfg)
	if len(netappClusters) > 0 {
		volFetcher := fetchnetapp.NewVolumeFetcher(netappClusters)
		fm[volFetcher.ID()] = volFetcher
		aggrFetcher := fetchnetapp.NewAggregateFetcher(netappClusters)
		fm[aggrFetcher.ID()] = aggrFetcher
		svmFetcher := fetchnetapp.NewSVMFetcher(netappClusters)
		fm[svmFetcher.ID()] = svmFetcher
		logger.Info("netapp fetchers registered", "clusters", len(netappClusters))
	}

	// KubeSense intelligence fetcher: queries kubesense_* tables for pre-computed
	// health events, config violations, forecasts, and APM regressions.
	// Always register — the fetcher degrades gracefully when tables are empty.
	if db != nil {
		ksFetcher := fetchks.NewKubeSenseFetcher(db)
		fm[ksFetcher.ID()] = ksFetcher
		logger.Info("kubesense evidence fetcher registered")
	}

	// Past-investigations fetcher: queries rca_investigations for similar past
	// incidents (Aurora/Weaviate past-RCA injection pattern using Postgres).
	// Injects top-5 similar past investigations as TypeSimilarIncident context evidence.
	if db != nil {
		pastFetcher := fetchpast.NewPastInvestigationsFetcher(db)
		fm[pastFetcher.ID()] = pastFetcher
		logger.Info("past-investigations context fetcher registered")
	}

	// Runbook fetcher: injects matching investigation runbooks as context evidence
	// (HolmesGPT SkillCatalog pattern). Fetches from investigation_runbooks table.
	if db != nil {
		rbFetcher := fetchrb.NewRunbookFetcher(db)
		fm[rbFetcher.ID()] = rbFetcher
		logger.Info("runbook skill-catalog fetcher registered")
	}

	logger.Info("evidence fetchers registered", "count", len(fm))
	return fm
}

func loadKubeconfigPaths(dir string) (map[string]string, error) {
	paths := make(map[string]string)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading kubeconfigs dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" && ext != ".kubeconfig" && ext != "" {
			continue
		}
		clusterRef := name[:len(name)-len(ext)]
		paths[clusterRef] = filepath.Join(dir, name)
	}
	return paths, nil
}

func loadCloudStackConfigs() []fetchcs.ClientConfig {
	var configs []fetchcs.ClientConfig
	for i := 1; i <= 10; i++ {
		endpoint := os.Getenv(fmt.Sprintf("OIE_CS_CLUSTER_%d_ENDPOINT", i))
		if endpoint == "" {
			break
		}
		clusterRef := os.Getenv(fmt.Sprintf("OIE_CS_CLUSTER_%d_REF", i))
		if clusterRef == "" {
			clusterRef = fmt.Sprintf("cloudstack-%d", i)
		}
		configs = append(configs, fetchcs.ClientConfig{
			Endpoint:   endpoint,
			APIKey:     os.Getenv(fmt.Sprintf("OIE_CS_CLUSTER_%d_API_KEY", i)),
			SecretKey:  os.Getenv(fmt.Sprintf("OIE_CS_CLUSTER_%d_SECRET_KEY", i)),
			ClusterRef: clusterRef,
		})
	}
	return configs
}

// loadCloudStackConfigsFromDB reads CloudStack configurations from the AlertHub database.
// Used as a fallback when env-var credentials are not present.
func loadCloudStackConfigsFromDB(db *sql.DB, logger *slog.Logger) []fetchcs.ClientConfig {
	rows, err := db.QueryContext(context.Background(), `
		SELECT name, api_url, api_key, secret_key
		FROM cloudstack_configs
		WHERE enabled = true
		  AND LENGTH(api_key) > 0
		  AND LENGTH(secret_key) > 0
		ORDER BY name`)
	if err != nil {
		logger.Warn("failed to load CloudStack configs from DB", "error", err)
		return nil
	}
	defer rows.Close()

	var configs []fetchcs.ClientConfig
	for rows.Next() {
		var cfg fetchcs.ClientConfig
		if err := rows.Scan(&cfg.ClusterRef, &cfg.Endpoint, &cfg.APIKey, &cfg.SecretKey); err != nil {
			continue
		}
		configs = append(configs, cfg)
	}
	if len(configs) > 0 {
		logger.Info("loaded CloudStack configs from DB", "count", len(configs))
	}
	return configs
}

// loadNetAppConfigs parses NetApp cluster config from the NETAPP_CLUSTERS env var
// (same JSON format used by the AlertHub backend) and pairs with credentials.
// NETAPP_CLUSTERS: [{"cluster":"<ip>","name":"<name>","region":"<mdn|rno>"}]
func loadNetAppConfigs(cfg *config.Config) []fetchnetapp.ClusterConfig {
	if cfg.NetAppClustersJSON == "" || cfg.NetAppPassword == "" {
		return nil
	}

	var clusters []struct {
		Cluster string `json:"cluster"` // IP
		Name    string `json:"name"`
		Region  string `json:"region"`
	}
	if err := json.Unmarshal([]byte(cfg.NetAppClustersJSON), &clusters); err != nil {
		return nil
	}

	var out []fetchnetapp.ClusterConfig
	for _, c := range clusters {
		out = append(out, fetchnetapp.ClusterConfig{
			Name:     c.Name,
			Host:     c.Cluster,
			User:     cfg.NetAppUser,
			Password: cfg.NetAppPassword,
			Region:   c.Region,
		})
	}
	return out
}

func openDB(cfg *config.Config) (*sql.DB, error) {
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("opening postgres connection: %w", err)
	}
	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(c); err != nil {
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}
	return db, nil
}

func requestLoggingMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		c.Next()
		logger.Info("http request",
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}

// buildRCAGenerator constructs the RCA Generator with all dependencies.
func buildRCAGenerator(
	cfg *config.Config,
	db interface{}, // *sql.DB, passed as interface to avoid import cycle at package level
	evRepo interface{},
	logger *slog.Logger,
) *approca.Generator {
	// Resolve concrete types.
	sqlDB := db.(*sql.DB)
	evidenceRepo := pgEvidence.NewRepository(sqlDB)
	hypothesisRepo := pgHypothesis.NewRepository(sqlDB)

	causalBuilder := approca.NewCausalChainBuilder(cfg.EIRSBaseURL)

	var ollamaClient *ollama.Client
	if cfg.OllamaBaseURL != "" {
		narrativeModel := cfg.OllamaModelNarrative
		if narrativeModel == "" {
			narrativeModel = cfg.OllamaModel
		}
		ollamaClient = ollama.NewClient(cfg.OllamaBaseURL, narrativeModel)
	}

	narrator := approca.NewNarrator(ollamaClient, logger)

	// Wire separate triage/fast Ollama client for evidence compaction
	// (HolmesGPT fast-model pattern). Uses OllamaModelTriage when set.
	if cfg.OllamaBaseURL != "" && cfg.OllamaModelTriage != "" && cfg.OllamaModelTriage != cfg.OllamaModelNarrative {
		triageClient := ollama.NewClient(cfg.OllamaBaseURL, cfg.OllamaModelTriage)
		narrator.SetTriageClient(triageClient)
		logger.Info("evidence compaction triage model configured",
			"triage_model", cfg.OllamaModelTriage,
			"narrative_model", ollamaClient.Model())
	}

	return approca.NewGenerator(hypothesisRepo, evidenceRepo, causalBuilder, narrator, cfg.AlertHubBaseURL, logger)
}
