package main

import (
	"reflect"
	"testing"

	"github.com/alexflint/go-arg"
	"github.com/z46-dev/llama-stack/internal/config"
)

// TestCommandGrammar exercises help and required nested arguments without host access.
func TestCommandGrammar(t *testing.T) {
	var err error

	if err = run([]string{"--help"}); err != nil {
		t.Fatalf("root help failed: %v", err)
	}
	if err = run([]string{"resource", "capability", "issue", "--user", "user-a"}); err == nil {
		t.Fatalf("incomplete capability command returned %v", err)
	}
}

// TestEnabledServiceImages covers enabled filtering and duplicate suppression.
func TestEnabledServiceImages(t *testing.T) {
	var (
		cfg    config.Config
		images []string
	)

	cfg.OpenWebUI.Service = config.Service{Enabled: true, Image: "example/web:1"}
	cfg.SearXNG.Service = config.Service{Enabled: false, Image: "example/search:1"}
	cfg.Downloads.Service = config.Service{Enabled: true, Image: "example/web:1"}

	if images = enabledServiceImages(cfg); !reflect.DeepEqual(images, []string{"example/web:1"}) {
		t.Fatalf("unexpected images: %v", images)
	}
}

// TestResolveConfigPath honors the environment override used by tests and recovery.
func TestResolveConfigPath(t *testing.T) {
	var path string

	t.Setenv("LLAMA_STACK_CONFIG", "/tmp/llama-stack-test.toml")
	resolveConfigPath(&path)
	if path != "/tmp/llama-stack-test.toml" {
		t.Fatalf("unexpected config path: %s", path)
	}
}

// TestInstallNoStartFlag protects the staged installer command used by setup.
func TestInstallNoStartFlag(t *testing.T) {
	var (
		options commandOptions
		parser  *arg.Parser
		err     error
	)

	if parser, err = arg.NewParser(arg.Config{Program: "llama-stack"}, &options); err != nil {
		t.Fatal(err)
	}
	if err = parser.Parse([]string{"install", "--config", "/tmp/config.toml", "--no-start"}); err != nil {
		t.Fatal(err)
	}
	if options.Install == nil || !options.Install.NoStart {
		t.Fatal("--no-start was not parsed")
	}
}
