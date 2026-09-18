package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestExampleConfiguration ensures the shipped configuration remains loadable.
func TestExampleConfiguration(t *testing.T) {
	var (
		_, filename, _, _ = runtime.Caller(0)
		cfg               Config
		err               error
	)

	if cfg, err = Load(filepath.Join(filepath.Dir(filename), "..", "..", "config", "config.example.toml")); err != nil {
		t.Fatalf("load example configuration: %v", err)
	}
	if cfg.Llama.ContextSize != 65536 {
		t.Fatalf("unexpected context size: %d", cfg.Llama.ContextSize)
	}
	if !cfg.OpenWebUI.ContextCompaction {
		t.Fatal("context compaction must be enabled in the example")
	}
	if cfg.Jobs.Database != "/var/lib/llama-stack/jobs.db" {
		t.Fatalf("unexpected scheduler database: %s", cfg.Jobs.Database)
	}
}

// TestJobSchedulerRequiresPositiveTiming rejects a scheduler that would busy-loop.
func TestJobSchedulerRequiresPositiveTiming(t *testing.T) {
	var (
		_, filename, _, _ = runtime.Caller(0)
		cfg               Config
		err               error
	)

	if cfg, err = Load(filepath.Join(filepath.Dir(filename), "..", "..", "config", "config.example.toml")); err != nil {
		t.Fatal(err)
	}
	cfg.Jobs.PollInterval = 0
	if err = cfg.Validate(); err == nil {
		t.Fatal("expected zero scheduler poll interval to be rejected")
	}
}

// TestContextCompactionRequiresHeadroom protects the model output and tool budget.
func TestContextCompactionRequiresHeadroom(t *testing.T) {
	var (
		_, filename, _, _ = runtime.Caller(0)
		cfg               Config
		err               error
	)

	if cfg, err = Load(filepath.Join(filepath.Dir(filename), "..", "..", "config", "config.example.toml")); err != nil {
		t.Fatal(err)
	}

	cfg.OpenWebUI.ContextCompactionThreshold = cfg.Llama.ContextSize
	if err = cfg.Validate(); err == nil {
		t.Fatal("expected threshold at context size to be rejected")
	}
}

// TestAdministrativeGroupIsFixed protects the local authorization boundary.
func TestAdministrativeGroupIsFixed(t *testing.T) {
	var (
		_, filename, _, _ = runtime.Caller(0)
		cfg               Config
		err               error
	)

	if cfg, err = Load(filepath.Join(filepath.Dir(filename), "..", "..", "config", "config.example.toml")); err != nil {
		t.Fatal(err)
	}

	cfg.Stack.AdminGroup = "wheel"
	if err = cfg.Validate(); err == nil {
		t.Fatal("expected alternate administrative group to be rejected")
	}
}
