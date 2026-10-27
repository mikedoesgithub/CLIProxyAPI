package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBalanceV8ConfigRoundTrip(t *testing.T) {
	raw := []byte(`routing:
  strategy: round-robin
  balance:
    enabled: true
    classifier-url: http://127.0.0.1:8318/classify
    fallback: test-model
    timeout-ms: 100
    cost-weight: 0.1
    candidates:
      - model: test-model
        tasks: ['*']
        quality: 95
        tools: true
    rules:
      - task: browser
        any: ['use the browser']
oauth:
  auth-dir: ./auths
`)
	cfg, err := ParseConfigBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Balance.Enabled || cfg.Balance.Fallback != "test-model" || len(cfg.Balance.Candidates) != 1 || len(cfg.Balance.Rules) != 1 {
		t.Fatalf("parse: %+v", cfg.Balance)
	}
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err = NormalizeConfigLayout(encoded, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "\nbalance:") {
		t.Fatal("serialized a legacy root field")
	}
	again, err := ParseConfigBytes(encoded)
	if err != nil || !reflect.DeepEqual(cfg.Balance, again.Balance) {
		t.Fatalf("roundtrip: %v %+v", err, again)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Balance.TimeoutMS = 150
	if err = SaveConfigPreserveComments(path, loaded, true); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadConfig(path)
	if err != nil || loaded.Balance.TimeoutMS != 150 || loaded.AuthDir != "./auths" {
		t.Fatalf("saved config: %v %+v", err, loaded)
	}
}
