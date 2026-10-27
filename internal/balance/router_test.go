package balance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

func policy(t *testing.T) config.BalanceConfig {
	t.Helper()
	reg := registry.GetGlobalRegistry()
	id := "balance-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	reg.RegisterClient(id, "openai", []*registry.ModelInfo{
		{ID: id + "-quality", Thinking: &registry.ThinkingSupport{Levels: []string{"low", "medium", "high"}}, SupportedInputModalities: []string{"text", "image"}},
		{ID: id + "-cheap", Thinking: &registry.ThinkingSupport{Levels: []string{"low", "medium", "high"}}},
	})
	t.Cleanup(func() { reg.UnregisterClient(id) })
	return config.BalanceConfig{Enabled: true, MinConfidence: .65, Fallback: id + "-quality", CostWeight: .1, LatencyWeight: .05, Candidates: []config.BalanceCandidate{
		{Model: id + "-quality", Tasks: []string{"*"}, Quality: 95, Cost: 70, Latency: 60, Tools: true, LongContext: true},
		{Model: id + "-cheap", Tasks: []string{"general", "coding"}, Difficulties: []string{"trivial", "easy"}, Quality: 95, Cost: 30, Latency: 20},
	}}
}

func classified(t *testing.T, c Classification) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(c) }))
	t.Cleanup(s.Close)
	return s.URL
}

func TestRoutingQualityCostDifficultyAndExplicitReasoning(t *testing.T) {
	for _, tt := range []struct {
		name, difficulty, body, requested, wantSuffix string
		cheap                                         bool
	}{
		{"easy", "easy", `{"messages":[{"role":"user","content":"hello"}]}`, "balance", "(low)", true},
		{"hard", "hard", `{"messages":[{"role":"user","content":"prove it"}]}`, "balance", "(high)", false},
		{"explicit-body", "hard", `{"reasoning_effort":"low","messages":[{"role":"user","content":"prove it"}]}`, "balance", "", false},
		{"explicit-suffix", "hard", `{"messages":[{"role":"user","content":"prove it"}]}`, "balance(low)", "(low)", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := policy(t)
			cfg.ClassifierURL = classified(t, Classification{Category: "coding", Confidence: .95, Difficulty: tt.difficulty})
			d, err := New(cfg).Route(context.Background(), "openai", tt.requested, []byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			want := cfg.Candidates[0].Model
			if tt.cheap {
				want = cfg.Candidates[1].Model
			}
			want += tt.wantSuffix
			if d.Model != want || d.Source != "classifier" {
				t.Fatalf("got %+v want %s", d, want)
			}
		})
	}
}

func TestFallbackAndRequirements(t *testing.T) {
	cfg := policy(t)
	cfg.ClassifierURL = classified(t, Classification{Category: "coding", Confidence: .2, Difficulty: "easy"})
	r := New(cfg)
	for _, raw := range []string{`{"input":"hello"}`, `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:secret"}}]}]}`, `{"input":"hi","tools":[{"type":"function","name":"hello"}]}`} {
		d, err := r.Route(context.Background(), "openai-response", "balance", []byte(raw))
		if err != nil || d.Model != cfg.Fallback+"(medium)" || d.Source != "fallback" {
			t.Fatalf("got %+v %v", d, err)
		}
	}
	cfg.Candidates = cfg.Candidates[1:]
	cfg.Fallback = cfg.Candidates[0].Model
	_, err := New(cfg).Route(context.Background(), "openai", "balance", []byte(`{"messages":[{"role":"user","content":[{"type":"image_url"}]}]}`))
	if err == nil {
		t.Fatal("vision request accepted by text-only fallback")
	}
}

func TestSpecialtyRuleOverridesLowConfidence(t *testing.T) {
	cfg := policy(t)
	cfg.ClassifierURL = classified(t, Classification{Category: "coding", Confidence: .2, Difficulty: "easy"})
	cfg.Rules = []config.BalanceRule{{Task: "browser", Any: []string{"use the browser"}}}
	cfg.Candidates[1].Tasks = []string{"browser"}
	cfg.Candidates[1].Difficulties = nil
	d, err := New(cfg).Route(context.Background(), "openai", "balance", []byte(`{"input":"Use the browser to compare prices"}`))
	if err != nil || d.Model != cfg.Candidates[1].Model+"(medium)" || d.Source != "policy-rule" {
		t.Fatalf("got %+v %v", d, err)
	}
}

func TestNativeToolAndMissingModelFailClosed(t *testing.T) {
	cfg := policy(t)
	cfg.ClassifierURL = classified(t, Classification{Category: "coding", Confidence: .99, Difficulty: "easy"})
	_, err := New(cfg).Route(context.Background(), "openai", "balance", []byte(`{"input":"hello","tools":[{"type":"computer"}]}`))
	if err == nil {
		t.Fatal("unapproved native tool accepted")
	}
	cfg.Candidates[0].Model = "not-registered"
	cfg.Candidates = cfg.Candidates[:1]
	_, err = New(cfg).Route(context.Background(), "openai", "balance", []byte(`{"input":"hello"}`))
	if err == nil {
		t.Fatal("unregistered target accepted")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTimeoutBusyMalformedFallback(t *testing.T) {
	for _, kind := range []string{"timeout", "busy", "malformed", "oversized", "status", "nan-confidence", "abstain"} {
		t.Run(kind, func(t *testing.T) {
			cfg := policy(t)
			cfg.TimeoutMS = 1
			cfg.ClassifierURL = classified(t, Classification{})
			r := New(cfg)
			switch kind {
			case "timeout":
				r.client.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
					<-req.Context().Done()
					return nil, req.Context().Err()
				})
			case "busy":
				r.busy <- struct{}{}
			default:
				r.client.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
					body := "invalid"
					status := 200
					switch kind {
					case "oversized":
						body = strings.Repeat("x", 9000)
					case "status":
						status = 503
					case "abstain":
						body = `{"category":"coding","difficulty":"easy","category_confidence":0.99,"abstain":true}`
					case "nan-confidence":
						body = `{"category":"general","difficulty":"easy","category_confidence":1.5}`
					}
					return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
				})
			}
			d, err := r.Route(context.Background(), "openai", "balance", []byte(`{"input":"hello"}`))
			if err != nil || d.Source != "fallback" || d.Model != cfg.Fallback+"(medium)" {
				t.Fatalf("got %+v %v", d, err)
			}
		})
	}
}

func TestInvalidEndpointAndAliasLoop(t *testing.T) {
	for _, url := range []string{"http://example.com/classify", "http://localhost:8318/classify", "https://127.0.0.1/classify", "http://user@127.0.0.1/classify", "http://127.0.0.1/classify?secret=x"} {
		cfg := policy(t)
		cfg.ClassifierURL = url
		if New(cfg).invalid == nil {
			t.Fatalf("accepted %s", url)
		}
	}
	cfg := policy(t)
	cfg.Candidates[0].Model = "balance(high)"
	if New(cfg).invalid == nil {
		t.Fatal("accepted recursion")
	}
}

func TestKnownExhaustedPoolIsSkipped(t *testing.T) {
	cfg := policy(t)
	cfg.ClassifierURL = classified(t, Classification{Category: "coding", Confidence: .99, Difficulty: "easy"})
	id := "balance-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	registry.GetGlobalRegistry().SuspendClientModel(id, cfg.Candidates[1].Model, "quota")
	d, err := New(cfg).Route(context.Background(), "openai", "balance", []byte(`{"input":"write code"}`))
	if err != nil || d.Model != cfg.Candidates[0].Model+"(low)" {
		t.Fatalf("selected exhausted pool: %+v %v", d, err)
	}
}

// Optional real-weights smoke test. CI unit tests use deterministic protocol fixtures.
func TestBalanceLocalModelSmoke(t *testing.T) {
	endpoint := os.Getenv("BALANCE_CLASSIFIER_TEST_URL")
	if endpoint == "" {
		t.Skip("local decision service not requested")
	}
	cfg := policy(t)
	cfg.ClassifierURL = endpoint
	cfg.TimeoutMS = 250
	d, err := New(cfg).Route(context.Background(), "openai", "balance", []byte(`{"messages":[{"role":"user","content":"Hello, how are you?"}]}`))
	if err != nil || d.Source != "classifier" || d.Task != "general" {
		t.Fatalf("real model route: %+v %v", d, err)
	}
}
