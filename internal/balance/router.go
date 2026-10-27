package balance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
)

type Classification struct {
	Abstain    bool    `json:"abstain"`
	Category   string  `json:"category"`
	Confidence float64 `json:"category_confidence"`
	Difficulty string  `json:"difficulty"`
	Stakes     string  `json:"stakes"`
}

type Decision struct{ Model, Task, Difficulty, Source string }

type Router struct {
	cfg      config.BalanceConfig
	client   *http.Client
	endpoint string
	invalid  error
	busy     chan struct{}
}

func New(cfg config.BalanceConfig) *Router {
	r := &Router{cfg: cfg, busy: make(chan struct{}, 1)}
	if r.cfg.TimeoutMS <= 0 {
		r.cfg.TimeoutMS = 100
	}
	if r.cfg.TimeoutMS > 250 {
		r.cfg.TimeoutMS = 250
	}
	if r.cfg.MinConfidence == 0 {
		r.cfg.MinConfidence = .65
	}
	endpoint := cfg.ClassifierURL
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8318/classify"
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		r.invalid = errors.New("balance classifier-url must be literal loopback HTTP without credentials, query or fragment")
	} else {
		r.endpoint = u.String()
	}
	// This budget covers only local routing preflight, before credential acquisition.
	// It never applies a deadline to a generation provider or established generation stream.
	r.client = &http.Client{Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 1, MaxConnsPerHost: 1, IdleConnTimeout: 30 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, c := range cfg.Candidates {
		if c.Model == "" || thinking.ParseSuffix(c.Model).ModelName == "balance" || thinking.ParseSuffix(c.Model).HasSuffix || c.Model == "auto" {
			r.invalid = errors.New("balance candidates must be concrete unsuffixed models")
		}
		for _, score := range []float64{c.Quality, c.Cost, c.Latency} {
			if !finite(score) || score < 0 || score > 100 {
				r.invalid = errors.New("balance candidate scores must be in [0,100]")
			}
		}
	}
	if !finite(cfg.CostWeight) || !finite(cfg.LatencyWeight) || cfg.CostWeight < 0 || cfg.LatencyWeight < 0 || !finite(r.cfg.MinConfidence) || r.cfg.MinConfidence < 0 || r.cfg.MinConfidence > 1 {
		r.invalid = errors.New("invalid balance policy weights or confidence")
	}
	fallbackConfigured := false
	for _, candidate := range cfg.Candidates {
		if candidate.Model == cfg.Fallback {
			fallbackConfigured = true
		}
	}
	if cfg.Fallback == "" || !fallbackConfigured {
		r.invalid = errors.New("balance fallback must name a configured concrete candidate")
	}
	return r
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// Route selects only a concrete model registered by an existing credential.
// Registry presence is not a quota estimate: the normal auth manager owns cooldowns/failover.
func (r *Router) Route(ctx context.Context, format, requested string, raw []byte) (Decision, error) {
	if r.invalid != nil {
		return Decision{}, r.invalid
	}
	in, err := Extract(raw)
	if err != nil {
		return Decision{}, err
	}
	result := Decision{Task: "general", Difficulty: "moderate", Source: "fallback"}
	classification, err := r.classify(ctx, in)
	if ctx.Err() != nil {
		return Decision{}, ctx.Err()
	}
	if err == nil && !classification.Abstain && classification.Confidence >= r.cfg.MinConfidence && validClass(classification) {
		result.Task = classification.Category
		result.Difficulty = classification.Difficulty
		result.Source = "classifier"
		if classification.Stakes == "medium" || classification.Stakes == "high" {
			if result.Difficulty == "easy" || result.Difficulty == "trivial" || result.Difficulty == "moderate" {
				result.Difficulty = "hard"
			}
		}
	}
	{
		lower := strings.ToLower(in.Request)
		for _, rule := range r.cfg.Rules {
			matched := false
			for _, term := range rule.Any {
				if term != "" && strings.Contains(lower, strings.ToLower(term)) {
					result.Task = rule.Task
					result.Source = "policy-rule"
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
	}
	var chosen *config.BalanceCandidate
	best := math.Inf(-1)
	for i := range r.cfg.Candidates {
		c := &r.cfg.Candidates[i]
		if !eligible(*c, in) {
			continue
		}
		if result.Source == "fallback" {
			if c.Model == r.cfg.Fallback {
				chosen = c
				break
			}
			continue
		}
		if !slices.Contains(c.Tasks, result.Task) && !slices.Contains(c.Tasks, "*") {
			continue
		}
		if len(c.Difficulties) > 0 && !slices.Contains(c.Difficulties, result.Difficulty) {
			continue
		}
		score := c.Quality - r.cfg.CostWeight*c.Cost - r.cfg.LatencyWeight*c.Latency
		if score > best {
			best = score
			chosen = c
		}
	}
	if chosen == nil && result.Source != "fallback" {
		for i := range r.cfg.Candidates {
			c := &r.cfg.Candidates[i]
			if c.Model == r.cfg.Fallback && eligible(*c, in) {
				chosen = c
				result.Source = "fallback"
				break
			}
		}
	}
	if chosen == nil {
		return result, errors.New("balance has no registered compatible candidate; configure its fallback and candidates")
	}
	result.Model = chosen.Model
	suffix := thinking.ParseSuffix(requested)
	if suffix.HasSuffix {
		result.Model += "(" + suffix.RawSuffix + ")"
	} else if !in.ExplicitThinking && thinking.ExtractReasoningEffort(raw, format, requested) == "" {
		info := registry.GetGlobalRegistry().GetModelInfo(chosen.Model, "")
		if info != nil && info.Thinking != nil {
			level := "medium"
			switch result.Difficulty {
			case "trivial", "easy":
				level = "low"
			case "hard", "frontier":
				level = "high"
			}
			// Leave token-budget-only models to canonical thinking defaults. Never fabricate levels.
			if slices.Contains(info.Thinking.Levels, level) {
				result.Model += "(" + level + ")"
			}
		}
	}
	return result, nil
}

func eligible(c config.BalanceCandidate, in Input) bool {
	reg := registry.GetGlobalRegistry()
	if reg.GetModelCount(c.Model) == 0 || len(reg.GetModelProviders(c.Model)) == 0 {
		return false
	}
	info := reg.GetModelInfo(c.Model, "")
	if info == nil {
		return false
	}
	if in.Vision {
		vision := false
		for _, m := range info.SupportedInputModalities {
			if strings.EqualFold(m, "image") {
				vision = true
			}
		}
		if !vision {
			return false
		}
	}
	if in.Tools && !c.Tools {
		return false
	}
	for _, kind := range in.NativeTools {
		if !slices.Contains(c.NativeTools, kind) {
			return false
		}
	}
	return !in.LongContext || c.LongContext
}

func validClass(c Classification) bool {
	for _, ch := range c.Category {
		if !(ch >= 'a' && ch <= 'z') && !(ch >= '0' && ch <= '9') && ch != '_' && ch != '-' {
			return false
		}
	}
	return c.Category != "" && len(c.Category) <= 64 && finite(c.Confidence) && c.Confidence >= 0 && c.Confidence <= 1 && slices.Contains([]string{"trivial", "easy", "moderate", "hard", "frontier"}, c.Difficulty)
}

func (r *Router) classify(ctx context.Context, in Input) (Classification, error) {
	select {
	case r.busy <- struct{}{}:
		defer func() { <-r.busy }()
	default:
		return Classification{}, errors.New("classifier busy")
	}
	if in.Request == "" {
		return Classification{}, errors.New("no user text")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	body, err := json.Marshal(in)
	if err != nil {
		return Classification{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return Classification{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return Classification{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Classification{}, fmt.Errorf("classifier status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
	if err != nil || len(data) > 8192 {
		return Classification{}, errors.New("invalid classifier response size")
	}
	var out Classification
	if err = json.Unmarshal(data, &out); err != nil {
		return Classification{}, err
	}
	return out, nil
}
