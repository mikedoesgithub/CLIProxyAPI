package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

type balanceCaptureExecutor struct{ modelExecutionCaptureExecutor }

func (e *balanceCaptureExecutor) CountTokens(ctx context.Context, a *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	e.capture(req, opts)
	return coreexecutor.Response{Payload: []byte("0")}, nil
}

func TestBalanceAllHTTPExecutionPaths(t *testing.T) {
	for _, path := range []string{"nonstream", "stream", "count", "prepared-stream"} {
		t.Run(path, func(t *testing.T) {
			var calls atomic.Int64
			classifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["request"] != "hello" {
					t.Errorf("classifier got %+v", body)
				}
				_, _ = w.Write([]byte(`{"category":"general","category_confidence":0.98,"difficulty":"easy","stakes":"negligible"}`))
			}))
			defer classifier.Close()
			model := "balance-handler-" + path
			cfg := &sdkconfig.SDKConfig{Balance: sdkconfig.BalanceConfig{Enabled: true, ClassifierURL: classifier.URL, Fallback: model, Candidates: []sdkconfig.BalanceCandidate{{Model: model, Tasks: []string{"*"}, Quality: 100}}}}
			manager := coreauth.NewManager(nil, nil, nil)
			exec := &balanceCaptureExecutor{modelExecutionCaptureExecutor: modelExecutionCaptureExecutor{provider: "openai"}}
			exec.stream = func(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
				chunks := make(chan coreexecutor.StreamChunk, 1)
				chunks <- coreexecutor.StreamChunk{Payload: []byte("data: {\"ok\":true}\n\n")}
				close(chunks)
				return &coreexecutor.StreamResult{Chunks: chunks}, nil
			}
			manager.RegisterExecutor(exec)
			auth := &coreauth.Auth{ID: model, Provider: "openai", Status: coreauth.StatusActive}
			if _, err := manager.Register(context.Background(), auth); err != nil {
				t.Fatal(err)
			}
			registry.GetGlobalRegistry().RegisterClient(model, "openai", []*registry.ModelInfo{{ID: model, Thinking: &registry.ThinkingSupport{Levels: []string{"low", "medium", "high"}}}})
			defer registry.GetGlobalRegistry().UnregisterClient(model)
			handler := NewBaseAPIHandlers(cfg, manager)
			payload := []byte(`{"model":"balance","messages":[{"role":"system","content":"secret"},{"role":"user","content":"hello"}]}`)
			ctx := context.Background()
			switch path {
			case "nonstream":
				_, headers, err := handler.ExecuteWithAuthManager(ctx, "openai", "balance", payload, "")
				if headers.Get("X-Balance-Model") != model+"(low)" {
					t.Fatal(headers)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "count":
				_, headers, err := handler.ExecuteCountWithAuthManager(ctx, "openai", "balance", payload, "")
				if headers.Get("X-Balance-Model") != model+"(low)" {
					t.Fatal(headers)
				}
				if err != nil {
					t.Fatal(err)
				}
			default:
				if path == "prepared-stream" {
					var override bool
					ctx, override = handler.PrepareStreamModelRoute(ctx, "openai", "balance", payload)
					if !override {
						t.Fatal("virtual route not marked as override")
					}
				}
				data, headers, errors := handler.ExecuteStreamWithAuthManager(ctx, "openai", "balance", payload, "")
				if headers.Get("X-Balance-Model") != model+"(low)" {
					t.Fatal(headers)
				}
				for range data {
				}
				for err := range errors {
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			req, opts := exec.captured()
			if req.Model != model+"(low)" || string(req.Payload) != string(payload) {
				t.Fatalf("executor got %+v", req)
			}
			if opts.Metadata[coreexecutor.RequestedModelMetadataKey] != "balance" || opts.Metadata["balance.model"] != model+"(low)" || opts.Metadata["balance.source"] != "classifier" {
				t.Fatalf("metadata: %+v", opts.Metadata)
			}
			if calls.Load() != 1 {
				t.Fatalf("classifier called %d times", calls.Load())
			}
		})
	}
}

func TestBalanceOptInAndHotReload(t *testing.T) {
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	if _, handled := handler.applyBalanceRouter(context.Background(), "openai", "balance", []byte(`{"input":"hi"}`), modelExecutionOptions{}); handled {
		t.Fatal("disabled balance intercepted")
	}
	for i := 0; i < 2; i++ {
		model := fmt.Sprintf("balance-hot-reload-%d", i)
		registry.GetGlobalRegistry().RegisterClient(model, "openai", []*registry.ModelInfo{{ID: model}})
		defer registry.GetGlobalRegistry().UnregisterClient(model)
		handler.UpdateClients(&sdkconfig.SDKConfig{Balance: sdkconfig.BalanceConfig{Enabled: true, Fallback: model, Candidates: []sdkconfig.BalanceCandidate{{Model: model}}}})
		// No text invokes immediate fallback without making a network request.
		d, handled := handler.applyBalanceRouter(context.Background(), "openai", "balance", []byte(`{}`), modelExecutionOptions{})
		if !handled || d.Error != nil || d.Model != model {
			t.Fatalf("got %+v", d)
		}
	}
	d, handled := handler.applyBalanceRouter(context.Background(), "openai", "balance", []byte(`{"previous_response_id":"opaque"}`), modelExecutionOptions{})
	if !handled || d.Error == nil || d.Error.StatusCode != 400 {
		t.Fatal("continuation was not rejected")
	}
	d, _ = handler.applyBalanceRouter(context.Background(), "speech", "balance", []byte(`{}`), modelExecutionOptions{})
	if d.Error == nil || d.Error.StatusCode != 400 {
		t.Fatal("unsupported endpoint was accepted")
	}
	d, _ = handler.applyBalanceRouter(context.Background(), "openai", "balance", []byte(`{}`), modelExecutionOptions{ForcedProvider: "codex"})
	if d.Error == nil {
		t.Fatal("provider-pinned call accepted")
	}
}

func TestBalanceCatalogOptIn(t *testing.T) {
	h := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	if len(h.WithBalanceModel(nil, "openai")) != 0 {
		t.Fatal("advertised disabled model")
	}
	h.UpdateClients(&sdkconfig.SDKConfig{Balance: sdkconfig.BalanceConfig{Enabled: true}})
	for _, format := range []string{"openai", "claude", "gemini"} {
		models := h.WithBalanceModel(nil, format)
		if len(models) != 1 {
			t.Fatal(models)
		}
		if len(h.WithBalanceModel(models, format)) != 1 {
			t.Fatal("duplicate balance model")
		}
	}
}
