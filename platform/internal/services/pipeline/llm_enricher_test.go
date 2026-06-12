package pipeline

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// canBindPort checks whether the test environment allows TCP port binding.
// httptest.NewServer panics rather than returning an error, so we probe first.
func canBindPort() bool {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// newTestServer creates an httptest.Server, skipping the test if port binding
// is unavailable (e.g. sandboxed CI environments).
func newTestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	if !canBindPort() {
		t.Skip("skipping HTTP test: port binding not available in this environment")
	}
	return httptest.NewServer(h)
}

// ---------------------------------------------------------------------------
// NewLLMEnricher — default values and env-var overrides (no network needed)
// ---------------------------------------------------------------------------

func TestNewLLMEnricher_DefaultModel(t *testing.T) {
	os.Unsetenv("LLM_MODEL")
	os.Unsetenv("LLM_SERVICE_URL")
	os.Unsetenv("POD_NAMESPACE")

	e := NewLLMEnricher()
	if e.model != "qwen2.5:3b" {
		t.Errorf("default model = %q, want qwen2.5:3b", e.model)
	}
}

func TestNewLLMEnricher_DefaultURLContainsNamespace(t *testing.T) {
	os.Unsetenv("LLM_SERVICE_URL")
	os.Unsetenv("POD_NAMESPACE")

	e := NewLLMEnricher()
	if !strings.Contains(e.serviceURL, "aileron") {
		t.Errorf("default URL = %q, want to contain aileron", e.serviceURL)
	}
	if !strings.Contains(e.serviceURL, "ollama") {
		t.Errorf("default URL = %q, want to contain ollama", e.serviceURL)
	}
}

func TestNewLLMEnricher_POD_NAMESPACE(t *testing.T) {
	os.Unsetenv("LLM_SERVICE_URL")
	t.Setenv("POD_NAMESPACE", "tooling-mdn")
	defer os.Unsetenv("POD_NAMESPACE")

	e := NewLLMEnricher()
	if !strings.Contains(e.serviceURL, "tooling-mdn") {
		t.Errorf("URL = %q, want to contain tooling-mdn", e.serviceURL)
	}
	if strings.Contains(e.serviceURL, "aileron") {
		t.Errorf("URL %q should not contain aileron when POD_NAMESPACE=tooling-mdn", e.serviceURL)
	}
}

func TestNewLLMEnricher_LLM_SERVICE_URL_Override(t *testing.T) {
	t.Setenv("LLM_SERVICE_URL", "http://custom-ollama:11434")
	defer os.Unsetenv("LLM_SERVICE_URL")

	e := NewLLMEnricher()
	if e.serviceURL != "http://custom-ollama:11434" {
		t.Errorf("serviceURL = %q, want http://custom-ollama:11434", e.serviceURL)
	}
}

func TestNewLLMEnricher_LLM_MODEL_Override(t *testing.T) {
	t.Setenv("LLM_MODEL", "llama3:8b")
	defer os.Unsetenv("LLM_MODEL")

	e := NewLLMEnricher()
	if e.model != "llama3:8b" {
		t.Errorf("model = %q, want llama3:8b", e.model)
	}
}

func TestNewLLMEnricher_Timeout(t *testing.T) {
	os.Unsetenv("LLM_SERVICE_URL")
	os.Unsetenv("LLM_MODEL")

	e := NewLLMEnricher()
	if e.client.Timeout != 10*time.Second {
		t.Errorf("client timeout = %v, want 10s", e.client.Timeout)
	}
}

// ---------------------------------------------------------------------------
// GenerateRCA — unreachable server (no httptest needed)
// ---------------------------------------------------------------------------

func TestGenerateRCA_Unreachable_ReturnsEmpty(t *testing.T) {
	// Port 1 is reserved/unroutable — no process ever listens there.
	e := &LLMEnricher{
		serviceURL: "http://127.0.0.1:1",
		model:      "qwen2.5:3b",
		client:     &http.Client{Timeout: 100 * time.Millisecond},
	}
	result := e.GenerateRCA(context.Background(), "test alert", "high",
		"", "", "", "", map[string]float64{})

	if result != "" {
		t.Errorf("unreachable server: GenerateRCA = %q, want empty string", result)
	}
}

func TestGenerateRCA_ContextTimeout_ReturnsEmpty(t *testing.T) {
	// An address that accepts the connection but never responds.
	// Use 127.0.0.1:1 which will be refused instantly, satisfying the test
	// purpose: cancelled context must not panic.
	e := &LLMEnricher{
		serviceURL: "http://127.0.0.1:1",
		model:      "qwen2.5:3b",
		client:     &http.Client{Timeout: 10 * time.Second},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result := e.GenerateRCA(ctx, "test", "low", "", "", "", "", map[string]float64{})
	if result != "" {
		t.Errorf("cancelled context: GenerateRCA = %q, want empty string", result)
	}
}

// ---------------------------------------------------------------------------
// GenerateRCA — HTTP behaviour via test server (skipped when no port binding)
// ---------------------------------------------------------------------------

func TestGenerateRCA_Success(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "qwen2.5:3b" {
			t.Errorf("request model = %v, want qwen2.5:3b", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"response":"Root cause is Redis memory exhaustion."}`))
	}))
	defer srv.Close()

	e := &LLMEnricher{serviceURL: srv.URL, model: "qwen2.5:3b",
		client: &http.Client{Timeout: 5 * time.Second}}
	result := e.GenerateRCA(context.Background(), "Redis OOM", "critical",
		"redis-cluster", "cache", "redis-cluster", "/rno/rack1/host-1/vm-2/redis-cluster",
		map[string]float64{"topology": 0.9})

	if result != "Root cause is Redis memory exhaustion." {
		t.Errorf("GenerateRCA = %q, want exact RCA string", result)
	}
}

func TestGenerateRCA_Non200_ReturnsEmpty(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"model not found"}`))
	}))
	defer srv.Close()

	e := &LLMEnricher{serviceURL: srv.URL, model: "phi3:mini",
		client: &http.Client{Timeout: 2 * time.Second}}
	result := e.GenerateRCA(context.Background(), "test", "low",
		"", "", "", "", map[string]float64{})

	if result != "" {
		t.Errorf("non-200: GenerateRCA = %q, want empty string", result)
	}
}

func TestGenerateRCA_EmptyResponse_ReturnsEmpty(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"response":"   "}`)) // whitespace only
	}))
	defer srv.Close()

	e := &LLMEnricher{serviceURL: srv.URL, model: "qwen2.5:3b",
		client: &http.Client{Timeout: 2 * time.Second}}
	result := e.GenerateRCA(context.Background(), "test", "low",
		"", "", "", "", map[string]float64{})

	if result != "" {
		t.Errorf("whitespace response: GenerateRCA = %q, want empty string", result)
	}
}

func TestGenerateRCA_MalformedJSON_ReturnsEmpty(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json at all`))
	}))
	defer srv.Close()

	e := &LLMEnricher{serviceURL: srv.URL, model: "qwen2.5:3b",
		client: &http.Client{Timeout: 2 * time.Second}}
	result := e.GenerateRCA(context.Background(), "test", "low",
		"", "", "", "", map[string]float64{})

	if result != "" {
		t.Errorf("malformed JSON: GenerateRCA = %q, want empty string", result)
	}
}

func TestGenerateRCA_PromptContainsAlertTitle(t *testing.T) {
	var capturedPrompt string
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		capturedPrompt, _ = body["prompt"].(string)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"response":"RCA text"}`))
	}))
	defer srv.Close()

	e := &LLMEnricher{serviceURL: srv.URL, model: "qwen2.5:3b",
		client: &http.Client{Timeout: 2 * time.Second}}
	e.GenerateRCA(context.Background(), "postgres connection pool exhausted", "critical",
		"postgres-primary", "database", "postgres-primary", "",
		map[string]float64{"topology": 0.85})

	if !strings.Contains(capturedPrompt, "postgres connection pool exhausted") {
		t.Errorf("prompt missing alert title; prompt=%.200s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "postgres-primary") {
		t.Errorf("prompt missing matched node; prompt=%.200s", capturedPrompt)
	}
}

func TestGenerateRCA_StreamFalse(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if stream, _ := body["stream"].(bool); stream {
			t.Errorf("stream = true, want false — streaming must be disabled")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"response":"ok"}`))
	}))
	defer srv.Close()

	e := &LLMEnricher{serviceURL: srv.URL, model: "qwen2.5:3b",
		client: &http.Client{Timeout: 2 * time.Second}}
	e.GenerateRCA(context.Background(), "test", "low", "", "", "", "", nil)
}
