package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/balance"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
)

func (h *BaseAPIHandler) applyBalanceRouter(ctx context.Context, format, model string, raw []byte, opts modelExecutionOptions) (modelRouteDecision, bool) {
	if h == nil || thinking.ParseSuffix(model).ModelName != "balance" {
		return modelRouteDecision{}, false
	}
	h.runtimeMu.Lock()
	cfg := h.Cfg
	if cfg == nil || !cfg.Balance.Enabled {
		h.runtimeMu.Unlock()
		return modelRouteDecision{}, false
	}
	if h.balanceConfig != cfg || h.balanceRouter == nil {
		h.balanceRouter = balance.New(cfg.Balance)
		h.balanceConfig = cfg
	}
	router := h.balanceRouter
	h.runtimeMu.Unlock()
	fail := func(message string, status int) (modelRouteDecision, bool) {
		return modelRouteDecision{Error: &interfaces.ErrorMessage{StatusCode: status, Error: errors.New(message)}}, true
	}
	if h.AuthManager != nil && h.AuthManager.HomeEnabled() {
		return fail("balance is local-only and unavailable in Home mode", http.StatusBadRequest)
	}
	if opts.ForcedProvider != "" {
		return fail("balance cannot select models on a provider-pinned execution", http.StatusBadRequest)
	}
	switch format {
	case "openai", "openai-response", "claude", "gemini":
	default:
		return fail("balance supports stateless chat, responses, messages and generateContent requests", http.StatusBadRequest)
	}
	if _, err := balance.Extract(raw); err != nil {
		return fail(err.Error(), http.StatusBadRequest)
	}
	decision, err := router.Route(ctx, format, model, raw)
	if err != nil {
		return fail(err.Error(), http.StatusServiceUnavailable)
	}
	return modelRouteDecision{Model: decision.Model, BalanceTask: decision.Task, BalanceSource: decision.Source}, true
}

// WithBalanceModel exposes the virtual model without registering fake credentials.
func (h *BaseAPIHandler) WithBalanceModel(models []map[string]any, format string) []map[string]any {
	cfg := h.CurrentConfig()
	if cfg == nil || !cfg.Balance.Enabled || (h.AuthManager != nil && h.AuthManager.HomeEnabled()) {
		return models
	}
	for _, m := range models {
		if m["id"] == "balance" || m["name"] == "balance" {
			return models
		}
	}
	out := append([]map[string]any(nil), models...)
	model := map[string]any{"id": "balance", "object": "model", "created": int64(0), "owned_by": "prox", "display_name": "Balance", "type": "model"}
	if format == "gemini" {
		model = map[string]any{"name": "balance", "displayName": "Balance", "description": "Local task-aware model routing", "supportedGenerationMethods": []string{"generateContent"}}
	}
	return append(out, model)
}

// BalanceEnabled reports whether the local virtual route is active.
func (h *BaseAPIHandler) BalanceEnabled() bool {
	cfg := h.CurrentConfig()
	return cfg != nil && cfg.Balance.Enabled
}

func addBalanceMetadata(meta map[string]any, d modelRouteDecision) {
	if d.BalanceSource != "" {
		meta["balance.task"] = d.BalanceTask
		meta["balance.source"] = d.BalanceSource
		meta["balance.model"] = d.Model
	}
}

// balanceResponseHeaders identifies the concrete route without exposing account details.
func balanceResponseHeaders(headers http.Header, d modelRouteDecision) http.Header {
	if d.BalanceSource == "" {
		return headers
	}
	out := cloneHeader(headers)
	if out == nil {
		out = make(http.Header)
	}
	out.Set("X-Balance-Model", d.Model)
	out.Set("X-Balance-Source", d.BalanceSource)
	return out
}
