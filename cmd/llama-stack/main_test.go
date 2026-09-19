package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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

// TestConfigureRootlessPodmanDisablesSystemdCgroups covers non-login service accounts.
func TestConfigureRootlessPodmanDisablesSystemdCgroups(t *testing.T) {
	var (
		cfg      config.Config
		contents []byte
		err      error
	)

	cfg.Paths.State = t.TempDir()
	if err = configureRootlessPodman(cfg, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	if contents, err = os.ReadFile(filepath.Join(cfg.Paths.State, ".config", "containers", "containers.conf")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `cgroup_manager = "cgroupfs"`) ||
		!strings.Contains(string(contents), `events_logger = "file"`) {
		t.Fatalf("unexpected rootless Podman configuration:\n%s", contents)
	}
}

// TestRunCommandAtDoesNotInheritWorkingDirectory protects no-login service commands.
func TestRunCommandAtDoesNotInheritWorkingDirectory(t *testing.T) {
	var err error

	if err = runCommandAt("/", "/usr/bin/test", "/", "-ef", "."); err != nil {
		t.Fatalf("run command from explicit directory: %v", err)
	}
}

// TestLlamaServerArgsIncludesTooling protects the configured agent runtime.
func TestLlamaServerArgsIncludesTooling(t *testing.T) {
	var (
		cfg  config.Config
		args []string
	)

	cfg.Llama.Binary = "/usr/local/bin/llama-server"
	cfg.Llama.Jinja = true
	cfg.Llama.Tools = "all"
	cfg.Llama.MCPServersFile = "/etc/llama-stack/generated/mcp.json"
	cfg.Llama.ModelsPresetFile = "/var/lib/llama-stack/generated/model-presets.ini"
	cfg.Toolbox.Enabled = true
	cfg.Toolbox.Runtime = "podman"
	cfg.Toolbox.Image = "localhost/llama-toolbox:latest"
	cfg.SearXNG.Enabled = true
	args = llamaServerArgs(cfg)

	for _, expected := range []string{"--models-preset", cfg.Llama.ModelsPresetFile, "--jinja", "--tools", "all", "--tools-runtime", "podman:localhost/llama-toolbox:latest", "--mcp-servers-config", cfg.Llama.MCPServersFile} {
		if !slices.Contains(args, expected) {
			t.Fatalf("missing %q in arguments: %v", expected, args)
		}
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
