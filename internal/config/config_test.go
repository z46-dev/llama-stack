package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	if cfg.OpenWebUI.BuiltinTools || !cfg.OpenWebUI.AutoSelectTools || cfg.OpenWebUI.FunctionCalling != "native" {
		t.Fatal("Open WebUI must default to focused native tool calling")
	}
	if !cfg.Toolbox.Enabled || cfg.Toolbox.Runtime != "podman" || !cfg.Llama.Jinja {
		t.Fatal("toolbox and Jinja support must be enabled in the example")
	}
	if !cfg.AgentTools.Enabled || cfg.AgentTools.Port != 8091 || cfg.AgentTools.MaxOutput != 1048576 {
		t.Fatal("agent tool gateway must be enabled in the example")
	}
	if cfg.Jobs.Database != "/var/lib/llama-stack/jobs.db" {
		t.Fatalf("unexpected scheduler database: %s", cfg.Jobs.Database)
	}
	if cfg.Llama.ModelsPresetFile != "/var/lib/llama-stack/generated/model-presets.ini" ||
		cfg.Llama.ModelProfilesFile != DefaultModelProfilesFile {
		t.Fatal("model compatibility paths must be configured in the example")
	}
}

// TestLegacyConfigurationReceivesModelProfileDefaults protects incremental upgrades.
func TestLegacyConfigurationReceivesModelProfileDefaults(t *testing.T) {
	var (
		_, filename, _, _ = runtime.Caller(0)
		contents          []byte
		cfg               Config
		filtered          []string
		legacyPath        string = filepath.Join(t.TempDir(), "config.toml")
		err               error
	)

	if contents, err = os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "..", "config", "config.example.toml")); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(contents), "\n") {
		if !strings.HasPrefix(line, "models_preset_file =") &&
			!strings.HasPrefix(line, "model_profiles_file =") &&
			!strings.HasPrefix(line, "auto_select_tools =") &&
			!strings.HasPrefix(line, "function_calling =") {
			filtered = append(filtered, line)
		}
	}
	if err = os.WriteFile(legacyPath, []byte(strings.Join(filtered, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(legacyPath); err != nil {
		t.Fatal(err)
	}
	if cfg.Llama.ModelsPresetFile != filepath.Join(cfg.Paths.State, "generated", "model-presets.ini") ||
		cfg.Llama.ModelProfilesFile != DefaultModelProfilesFile {
		t.Fatal("legacy configuration did not receive model compatibility defaults")
	}
	if cfg.OpenWebUI.FunctionCalling != "native" {
		t.Fatal("legacy configuration did not receive native function calling default")
	}
	if !cfg.OpenWebUI.AutoSelectTools {
		t.Fatal("legacy configuration did not receive automatic tool selection default")
	}
}

// TestToolboxRequiresSupportedRuntime rejects a runtime llama-server cannot launch.
func TestToolboxRequiresSupportedRuntime(t *testing.T) {
	var (
		_, filename, _, _ = runtime.Caller(0)
		cfg               Config
		err               error
	)

	if cfg, err = Load(filepath.Join(filepath.Dir(filename), "..", "..", "config", "config.example.toml")); err != nil {
		t.Fatal(err)
	}
	cfg.Toolbox.Runtime = "docker"
	if err = cfg.Validate(); err == nil {
		t.Fatal("expected unsupported toolbox runtime to be rejected")
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
