package config

// BalanceConfig enables the local decision model and a versioned model-selection policy.
// Scores are operator-provided relative values in [0,100], not benchmark claims.
type BalanceConfig struct {
	Enabled       bool               `yaml:"enabled" json:"enabled"`
	ClassifierURL string             `yaml:"classifier-url,omitempty" json:"classifier-url,omitempty"`
	TimeoutMS     int                `yaml:"timeout-ms,omitempty" json:"timeout-ms,omitempty"`
	MinConfidence float64            `yaml:"min-confidence,omitempty" json:"min-confidence,omitempty"`
	Fallback      string             `yaml:"fallback,omitempty" json:"fallback,omitempty"`
	CostWeight    float64            `yaml:"cost-weight,omitempty" json:"cost-weight,omitempty"`
	LatencyWeight float64            `yaml:"latency-weight,omitempty" json:"latency-weight,omitempty"`
	Candidates    []BalanceCandidate `yaml:"candidates,omitempty" json:"candidates,omitempty"`
	Rules         []BalanceRule      `yaml:"rules,omitempty" json:"rules,omitempty"`
}

type BalanceCandidate struct {
	Model        string   `yaml:"model" json:"model"`
	Tasks        []string `yaml:"tasks" json:"tasks"`
	Difficulties []string `yaml:"difficulties,omitempty" json:"difficulties,omitempty"`
	Quality      float64  `yaml:"quality" json:"quality"`
	Cost         float64  `yaml:"cost,omitempty" json:"cost,omitempty"`
	Latency      float64  `yaml:"latency,omitempty" json:"latency,omitempty"`
	Tools        bool     `yaml:"tools,omitempty" json:"tools,omitempty"`
	NativeTools  []string `yaml:"native-tools,omitempty" json:"native-tools,omitempty"`
	LongContext  bool     `yaml:"long-context,omitempty" json:"long-context,omitempty"`
}

// BalanceRule is an explicit policy override, not a learned prediction. First match wins.
type BalanceRule struct {
	Task string   `yaml:"task" json:"task"`
	Any  []string `yaml:"any" json:"any"`
}
