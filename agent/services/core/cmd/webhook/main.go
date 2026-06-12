// kubesense-webhook — ValidatingAdmissionWebhook server.
// Delegates validation to services/core/internal/webhookhandler.
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
	"syscall"
	"time"

	"github.com/aileron-platform/aileron/agent/services/core/internal/risk"
	"github.com/aileron-platform/aileron/agent/services/core/internal/webhookhandler"
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

	riskEng := risk.NewEngine()
	cfg := webhookhandler.Config{
		DenyOnCriticalSecurity: denyCritical,
		DenyOnHighRisk:         denyHighRisk,
		DryRun:                 dryRun,
		RiskEngine:             riskEng,
	}
	h := webhookhandler.NewHandler(cfg)

	mux := http.NewServeMux()
	mux.Handle("/validate", h)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	tlsCfg, err := buildTLSConfig(tlsCertFile, tlsKeyFile, serviceName, namespace)
	if err != nil {
		log.Fatalf("kubesense-webhook: TLS: %v", err)
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
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Printf("kubesense-webhook: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("kubesense-webhook: shutting down")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
}

func buildTLSConfig(certFile, keyFile, serviceName, namespace string) (*tls.Config, error) {
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, err
		}
		return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}, nil
	}
	cert, err := generateSelfSignedCert(serviceName, namespace)
	if err != nil {
		return nil, err
	}
	log.Printf("kubesense-webhook: using auto-generated self-signed TLS cert")
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}, nil
}

func generateSelfSignedCert(serviceName, namespace string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	dnsNames := []string{
		serviceName,
		serviceName + "." + namespace,
		serviceName + "." + namespace + ".svc",
		serviceName + "." + namespace + ".svc.cluster.local",
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"KubeSense"}, CommonName: dnsNames[3]},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
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
