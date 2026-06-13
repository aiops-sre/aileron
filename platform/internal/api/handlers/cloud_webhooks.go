package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/aileron-platform/aileron/platform/internal/services/normalization"
)

// CloudWebhookHandler routes incoming webhooks from cloud monitoring platforms
// to the normalization registry and alert pipeline.
type CloudWebhookHandler struct {
	alertService interface{}          // any type with a Create method, or forward to pipeline
	registry     *normalization.Registry
	pipeline     interface{}          // AlertPipelineProcessor
}

// NewCloudWebhookHandler creates a CloudWebhookHandler pre-loaded with the
// supplied registry (or a default one when registry is nil).
func NewCloudWebhookHandler(registry *normalization.Registry) *CloudWebhookHandler {
	if registry == nil {
		registry = normalization.NewRegistry()
		// Register cloud-specific normalizers not included in the default set.
		registry.Register(normalization.AWSCloudWatchNormalizer{})
		registry.Register(normalization.AWSGuardDutyNormalizer{})
		registry.Register(normalization.AWSEventBridgeNormalizer{})
		registry.Register(normalization.GCPMonitoringNormalizer{})
		registry.Register(normalization.GCPSecurityCommandCenterNormalizer{})
		registry.Register(normalization.AzureMonitorNormalizer{})
		registry.Register(normalization.AzureSentinelNormalizer{})
		registry.Register(normalization.AliCloudCMSNormalizer{})
		registry.Register(normalization.OpsGenieNormalizer{})
	}
	return &CloudWebhookHandler{registry: registry}
}

// SetPipeline wires an AlertPipelineProcessor for downstream processing.
// The pipeline value must implement EnqueueAlert(alert interface{}).
func (h *CloudWebhookHandler) SetPipeline(p interface{}) {
	h.pipeline = p
}

// RegisterRoutes registers all /api/v1/webhooks/cloud/* routes on the provided router.
func (h *CloudWebhookHandler) RegisterRoutes(router gin.IRouter) {
	cloud := router.Group("/api/v1/webhooks/cloud")
	cloud.Use(webhookBodyLimitMiddleware(1 << 20)) // 1 MB limit

	// AWS
	cloud.POST("/aws", h.handleAWS)
	cloud.POST("/aws/cloudwatch", h.handleAWSCloudWatch)
	cloud.POST("/aws/guardduty", h.handleAWSGuardDuty)

	// GCP
	cloud.POST("/gcp", h.handleGCP)
	cloud.POST("/gcp/monitoring", h.handleGCPMonitoring)
	cloud.POST("/gcp/scc", h.handleGCPSCC)

	// Azure
	cloud.POST("/azure", h.handleAzure)
	cloud.POST("/azure/monitor", h.handleAzureMonitor)
	cloud.POST("/azure/sentinel", h.handleAzureSentinel)

	// AliCloud
	cloud.POST("/alicloud", h.handleAliCloud)

	// OpsGenie
	cloud.POST("/opsgenie", h.handleOpsGenie)
}

// ─── Route handlers ───────────────────────────────────────────────────────────

func (h *CloudWebhookHandler) handleAWS(c *gin.Context) {
	// Generic AWS: auto-detect between CloudWatch, GuardDuty, EventBridge, etc.
	h.dispatch(c, "", "WEBHOOK_SECRET_AWS", "aws")
}

func (h *CloudWebhookHandler) handleAWSCloudWatch(c *gin.Context) {
	h.dispatch(c, "aws_cloudwatch", "WEBHOOK_SECRET_AWS", "aws/cloudwatch")
}

func (h *CloudWebhookHandler) handleAWSGuardDuty(c *gin.Context) {
	h.dispatch(c, "aws_guardduty", "WEBHOOK_SECRET_AWS", "aws/guardduty")
}

func (h *CloudWebhookHandler) handleGCP(c *gin.Context) {
	// Generic GCP: auto-detect between Cloud Monitoring and SCC.
	h.dispatch(c, "", "WEBHOOK_SECRET_GCP", "gcp")
}

func (h *CloudWebhookHandler) handleGCPMonitoring(c *gin.Context) {
	h.dispatch(c, "gcp_monitoring", "WEBHOOK_SECRET_GCP", "gcp/monitoring")
}

func (h *CloudWebhookHandler) handleGCPSCC(c *gin.Context) {
	h.dispatch(c, "gcp_scc", "WEBHOOK_SECRET_GCP", "gcp/scc")
}

func (h *CloudWebhookHandler) handleAzure(c *gin.Context) {
	// Generic Azure: auto-detect between Azure Monitor and Sentinel.
	h.dispatch(c, "", "WEBHOOK_SECRET_AZURE", "azure")
}

func (h *CloudWebhookHandler) handleAzureMonitor(c *gin.Context) {
	h.dispatch(c, "azure_monitor", "WEBHOOK_SECRET_AZURE", "azure/monitor")
}

func (h *CloudWebhookHandler) handleAzureSentinel(c *gin.Context) {
	h.dispatch(c, "azure_sentinel", "WEBHOOK_SECRET_AZURE", "azure/sentinel")
}

func (h *CloudWebhookHandler) handleAliCloud(c *gin.Context) {
	h.dispatch(c, "alicloud_cms", "WEBHOOK_SECRET_ALICLOUD", "alicloud")
}

func (h *CloudWebhookHandler) handleOpsGenie(c *gin.Context) {
	// OpsGenie authenticates via token embedded in the payload; no platform-level
	// HMAC secret is configured through the env-var scheme.
	h.dispatch(c, "opsgenie", "", "opsgenie")
}

// ─── Core dispatch ────────────────────────────────────────────────────────────

// dispatch is the single shared implementation for every cloud webhook route.
//
// Parameters:
//
//	source     — explicit normalizer source key (e.g. "aws_cloudwatch").
//	             Empty string triggers registry auto-detection.
//	secretEnv  — environment variable name that holds the shared HMAC/plaintext secret.
//	             Empty string means no signature verification for this route.
//	routeLabel — human-readable label used in logs and the response body.
func (h *CloudWebhookHandler) dispatch(c *gin.Context, source, secretEnv, routeLabel string) {
	// Read raw body once — needed for both HMAC verification and JSON parsing.
	rawBody, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "failed to read request body",
			"details": err.Error(),
		})
		return
	}

	// Signature verification (skipped when secretEnv is empty or the env var is unset).
	if secretEnv != "" {
		if !h.verifySignature(c, rawBody, secretEnv) {
			// verifySignature already wrote the 401 response.
			return
		}
	}

	// Parse JSON payload.
	var raw map[string]interface{}
	if jsonErr := json.Unmarshal(rawBody, &raw); jsonErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid JSON payload",
			"details": jsonErr.Error(),
		})
		return
	}
	if raw == nil {
		raw = map[string]interface{}{}
	}

	// Normalize — explicit source or registry auto-detection.
	normalized, normErr := h.registry.Normalize(source, raw)
	if normErr != nil || normalized == nil {
		log.Printf("cloud_webhook[%s] normalization failed: %v", routeLabel, normErr)
		errDetail := ""
		if normErr != nil {
			errDetail = normErr.Error()
		}
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":   "normalization failed",
			"source":  routeLabel,
			"details": errDetail,
		})
		return
	}

	// Resolve the effective source (auto-detect routes populate it from the normalizer).
	effectiveSource := normalized.Source
	if effectiveSource == "" {
		effectiveSource = routeLabel
	}

	// Tracing header — lets callers inspect which normalizer handled the payload.
	c.Header("X-Aileron-Source", effectiveSource)

	// Forward to pipeline if wired (type-assert to the minimal interface needed).
	if h.pipeline != nil {
		type pipelineEnqueuer interface {
			EnqueueAlert(alert interface{})
		}
		if pp, ok := h.pipeline.(pipelineEnqueuer); ok {
			alert := normalization.ToAlert(normalized)
			pp.EnqueueAlert(alert)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"received":    true,
		"source":      effectiveSource,
		"fingerprint": normalized.Fingerprint,
	})
}

// ─── Signature verification ───────────────────────────────────────────────────

// verifySignature checks the inbound signature header against the shared secret
// held in the environment variable named by secretEnv.
//
// Supported schemes:
//
//	AWS  (WEBHOOK_SECRET_AWS)      — X-Hub-Signature-256: sha256=<hex>
//	GCP  (WEBHOOK_SECRET_GCP)      — X-Goog-Signature: <hex>  (falls back to X-Hub-Signature-256)
//	Azure (WEBHOOK_SECRET_AZURE)   — X-MS-Webhook-Secret: <plaintext>  (falls back to X-Hub-Signature-256)
//	Other                          — X-Hub-Signature-256: sha256=<hex>
//
// If the env var is not set the check is a no-op (returns true — permissive default
// so that integrations without a configured secret still work out of the box).
// When the secret IS configured and the signature header is absent or invalid, the
// function writes a 401 and returns false.
func (h *CloudWebhookHandler) verifySignature(c *gin.Context, body []byte, secretEnv string) bool {
	secret := os.Getenv(secretEnv)
	if secret == "" {
		return true // Not configured — skip verification.
	}

	switch {
	case strings.Contains(secretEnv, "AZURE"):
		// Azure Monitor / Sentinel: plaintext shared secret in X-MS-Webhook-Secret.
		incoming := c.GetHeader("X-MS-Webhook-Secret")
		if incoming != "" {
			if !hmac.Equal([]byte(incoming), []byte(secret)) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid webhook secret"})
				return false
			}
			return true
		}
		// Fallback: HMAC via X-Hub-Signature-256 (generic Azure integrations).
		return h.verifyHMAC(c, body, secret, "X-Hub-Signature-256")

	case strings.Contains(secretEnv, "GCP"):
		// GCP: HMAC-SHA256 hex in X-Goog-Signature.
		if gcpSig := c.GetHeader("X-Goog-Signature"); gcpSig != "" {
			return h.verifyHMACValue(c, body, secret, gcpSig)
		}
		// Fallback to X-Hub-Signature-256 for compatibility with GCP push subscriptions
		// that use the AWS-compatible signing format.
		return h.verifyHMAC(c, body, secret, "X-Hub-Signature-256")

	default:
		// AWS SNS / generic: X-Hub-Signature-256: sha256=<hex>
		return h.verifyHMAC(c, body, secret, "X-Hub-Signature-256")
	}
}

// verifyHMAC reads the given header, strips an optional "sha256=" prefix, and
// validates the HMAC-SHA256 of body against the hex value.
func (h *CloudWebhookHandler) verifyHMAC(c *gin.Context, body []byte, secret, headerName string) bool {
	sig := c.GetHeader(headerName)
	if sig == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "missing signature header: " + headerName,
		})
		return false
	}
	return h.verifyHMACValue(c, body, secret, sig)
}

// verifyHMACValue validates rawSig (which may carry a "sha256=" or "SHA256=" prefix)
// against the HMAC-SHA256 of body computed with secret.
func (h *CloudWebhookHandler) verifyHMACValue(c *gin.Context, body []byte, secret, rawSig string) bool {
	sig := rawSig
	sig = strings.TrimPrefix(sig, "sha256=")
	sig = strings.TrimPrefix(sig, "SHA256=")
	sig = strings.ToLower(strings.TrimSpace(sig))

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expected)) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid webhook signature"})
		return false
	}
	return true
}
