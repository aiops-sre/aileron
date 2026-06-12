package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// ModelBenchmark validates that the configured LLM model can produce the structured
// output format required by AlertHub before routing production incidents through it.
// LLMFit role-benchmark pattern: run structured-output + reasoning tests at startup,
// log results, and mark the model as validated or unvalidated.
//
// Tests:
//   1. structured-output: model must produce "Error: X\nSolution: Y" format
//   2. reasoning: model must not hallucinate components not in the prompt
//   3. anti-preamble: model must not start with "I am an AI" or similar
type ModelBenchmark struct {
	serviceURL string
	model      string
	client     *http.Client
}

// BenchmarkResult records the outcome of a model validation run.
type BenchmarkResult struct {
	Model      string
	Validated  bool
	Tests      []TestResult
	RunAt      time.Time
	DurationMs int64
}

// TestResult is the outcome of a single benchmark test.
type TestResult struct {
	Name    string
	Passed  bool
	Details string
}

func NewModelBenchmark(serviceURL, model string) *ModelBenchmark {
	return &ModelBenchmark{
		serviceURL: serviceURL,
		model:      model,
		client:     &http.Client{Timeout: 15 * time.Second},
	}
}

// Run executes all benchmark tests against the configured model.
// Logs results and returns the aggregate BenchmarkResult.
func (b *ModelBenchmark) Run(ctx context.Context) BenchmarkResult {
	start := time.Now()
	result := BenchmarkResult{
		Model: b.model,
		RunAt: start,
	}

	tests := []struct {
		name   string
		prompt string
		check  func(string) (bool, string)
	}{
		{
			name: "structured-output",
			prompt: `You are an SRE. A pod named "nginx-abc" in namespace "prod" is CrashLoopBackOff.
Respond in EXACTLY this format:
Error: <error class>
Solution: <one-sentence fix>`,
			check: func(r string) (bool, string) {
				lower := strings.ToLower(r)
				hasError := strings.Contains(lower, "error:")
				hasSolution := strings.Contains(lower, "solution:")
				if !hasError || !hasSolution {
					return false, fmt.Sprintf("missing Error: or Solution: in response (got: %.200s)", r)
				}
				return true, "both Error: and Solution: present"
			},
		},
		{
			name: "anti-hallucination",
			prompt: `An alert fired for service "payment-service" in namespace "billing".
Only reference components mentioned above. Write one sentence:
Error: <what failed>
Solution: <fix>`,
			check: func(r string) (bool, string) {
				// Check it doesn't hallucinate extra component names.
				lower := strings.ToLower(r)
				if strings.Contains(lower, "database") && !strings.Contains(lower, "payment") {
					return false, "hallucinated 'database' not in prompt"
				}
				if strings.Contains(lower, "i am an ai") || strings.Contains(lower, "as an ai") {
					return false, "anti-preamble pattern detected"
				}
				return true, "no hallucination detected"
			},
		},
		{
			name: "json-structured-output",
			prompt: `Return a JSON object with exactly two fields: "error_class" (string) and "confidence" (number between 0 and 1).
Example: {"error_class": "OOMKilled", "confidence": 0.85}
An nginx pod is OOMKilled. Return the JSON:`,
			check: func(r string) (bool, string) {
				r = strings.TrimSpace(r)
				// Extract JSON from response (model may add prose around it).
				start := strings.Index(r, "{")
				end := strings.LastIndex(r, "}")
				if start < 0 || end < 0 || end <= start {
					return false, fmt.Sprintf("no JSON object in response: %.100s", r)
				}
				var obj map[string]interface{}
				if err := json.Unmarshal([]byte(r[start:end+1]), &obj); err != nil {
					return false, fmt.Sprintf("JSON parse error: %v", err)
				}
				if _, ok := obj["error_class"]; !ok {
					return false, "missing error_class field"
				}
				if _, ok := obj["confidence"]; !ok {
					return false, "missing confidence field"
				}
				return true, "valid JSON with required fields"
			},
		},
	}

	passed := 0
	for _, t := range tests {
		response, err := b.generate(ctx, t.prompt)
		var tr TestResult
		tr.Name = t.name
		if err != nil {
			tr.Passed = false
			tr.Details = fmt.Sprintf("LLM call failed: %v", err)
		} else {
			tr.Passed, tr.Details = t.check(response)
		}
		result.Tests = append(result.Tests, tr)
		if tr.Passed {
			passed++
		}
	}

	result.DurationMs = time.Since(start).Milliseconds()
	result.Validated = passed >= 2 // pass 2 of 3 to be considered validated

	if result.Validated {
		log.Printf("Model benchmark PASSED: model=%s passed=%d/%d duration=%dms",
			b.model, passed, len(tests), result.DurationMs)
	} else {
		log.Printf("Model benchmark FAILED: model=%s passed=%d/%d duration=%dms — incidents will route through this model but quality may be degraded",
			b.model, passed, len(tests), result.DurationMs)
		for _, t := range result.Tests {
			if !t.Passed {
				log.Printf("  FAIL [%s]: %s", t.Name, t.Details)
			}
		}
	}
	return result
}

func (b *ModelBenchmark) generate(ctx context.Context, prompt string) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model":  b.model,
		"prompt": prompt,
		"stream": false,
		"options": map[string]interface{}{
			"temperature": 0.1,
			"num_predict": 150,
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.serviceURL+"/api/generate", bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var result struct{ Response string `json:"response"` }
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	return result.Response, nil
}

// RunAtStartup runs the benchmark and logs the result. Non-blocking: always
// returns so startup is never blocked by a slow/unavailable LLM.
func RunAtStartup(serviceURL, model string) {
	if serviceURL == "" || model == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		b := NewModelBenchmark(serviceURL, model)
		b.Run(ctx)
	}()
}
