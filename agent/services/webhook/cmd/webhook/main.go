// kubesense-webhook — ValidatingAdmissionWebhook server.
//
// Evaluates Kubernetes manifests BEFORE they are admitted to the cluster using:
//   - Config validation (health probes, resource limits, image tags)
//   - Security analysis (CIS benchmarks, container security, RBAC)
//   - Historical incident risk scoring (what patterns caused outages before)
//
// TLS: auto-generates a self-signed cert on startup unless WEBHOOK_TLS_CERT_FILE
// and WEBHOOK_TLS_KEY_FILE are provided (recommended for production).
//
// Start in DRY_RUN=true mode to observe findings without blocking deployments.
// Then enable DENY_ON_CRITICAL_SECURITY and DENY_ON_HIGH_RISK progressively.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aileron-platform/aileron/agent/services/core/internal/risk"
	"github.com/aileron-platform/aileron/agent/services/webhook/internal/handler"
)

func main() {
	port         := envOrDefault("PORT", "8443")
	tlsCertFile  := envOrDefault("WEBHOOK_TLS_CERT_FILE", "")
	tlsKeyFile   := envOrDefault("WEBHOOK_TLS_KEY_FILE", "")
	dryRun       := envOrDefault("DRY_RUN", "true") == "true"
	denyCritical := envOrDefault("DENY_ON_CRITICAL_SECURITY", "false") == "true"
	denyHighRisk := envOrDefault("DENY_ON_HIGH_RISK", "false") == "true"
	serviceName  := envOrDefault("SERVICE_NAME", "kubesense-webhook")
	namespace    := envOrDefault("NAMESPACE", "aileron-agent")

	log.Printf("kubesense-webhook starting: port=%s dry_run=%v deny_critical=%v deny_high_risk=%v",
		port, dryRun, denyCritical, denyHighRisk)

	// Risk engine (no historical data at startup; incidents fed via Kafka consumer)
	riskEng := risk.NewEngine()

	cfg := handler.Config{
		DenyOnCriticalSecurity: denyCritical,
		DenyOnHighRisk:         denyHighRisk,
		DryRun:                 dryRun,
		RiskEngine:             riskEng,
	}
	h := handler.NewHandler(cfg)

	mux := http.NewServeMux()
	mux.Handle("/validate", h)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	tlsCfg, err := buildTLSConfig(tlsCertFile, tlsKeyFile, serviceName, namespace)
	if err != nil {
		log.Fatalf("kubesense-webhook: TLS setup: %v", err)
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		TLSConfig:    tlsCfg,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 20 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		log.Printf("kubesense-webhook: listening on :%s (HTTPS)", port)
		// ListenAndServeTLS with "" cert/key paths — TLS config is already loaded
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Printf("kubesense-webhook: server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("kubesense-webhook: shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("kubesense-webhook: shutdown error: %v", err)
	}
}

// buildTLSConfig returns a TLS configuration.
// If certFile and keyFile are provided, loads them from disk.
// Otherwise, generates a self-signed certificate valid for the service DNS names.
// Self-signed certs are suitable for development; use cert-manager in production.
func buildTLSConfig(certFile, keyFile, serviceName, namespace string) (*tls.Config, error) {
	var cert tls.Certificate
	var err error

	if certFile != "" && keyFile != "" {
		cert, err = tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, err
		}
		log.Printf("kubesense-webhook: loaded TLS cert from %s", certFile)
	} else {
		cert, err = generateSelfSignedCert(serviceName, namespace)
		if err != nil {
			return nil, err
		}
		log.Printf("kubesense-webhook: using auto-generated self-signed TLS cert (set WEBHOOK_TLS_CERT_FILE + WEBHOOK_TLS_KEY_FILE for production)")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// generateSelfSignedCert creates a self-signed ECDSA P-256 certificate
// valid for the Kubernetes service DNS names of the webhook.
func generateSelfSignedCert(serviceName, namespace string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}

	// DNS SANs for the webhook service
	dnsNames := []string{
		serviceName,
		serviceName + "." + namespace,
		serviceName + "." + namespace + ".svc",
		serviceName + "." + namespace + ".svc.cluster.local",
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"KubeSense"},
			CommonName:   serviceName + "." + namespace + ".svc",
		},
		DNSNames:  dnsNames,
		NotBefore: time.Now().Add(-time.Minute),
		NotAfter:  time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return tls.X509KeyPair(certPEM, keyPEM)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Silence unused import warning — strings is used in handler package but not here directly
var _ = strings.Contains
