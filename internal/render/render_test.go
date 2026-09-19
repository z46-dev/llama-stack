package render

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/z46-dev/llama-stack/internal/config"
)

// TestWriteProducesHardenedStackConfiguration covers the generated deployment surface.
func TestWriteProducesHardenedStackConfiguration(t *testing.T) {
	var (
		_, filename, _, _ = runtime.Caller(0)
		cfg               config.Config
		root              string = t.TempDir()
		contents          []byte
		err               error
	)

	if cfg, err = config.Load(filepath.Join(filepath.Dir(filename), "..", "..", "config", "config.example.toml")); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(rooted(root, filepath.Dir(cfg.OpenWebUI.SecretFile)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(rooted(root, cfg.OpenWebUI.SecretFile), []byte("test-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(rooted(root, cfg.Llama.APIKeyFile), []byte("sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(rooted(root, cfg.SearXNG.SecretFile), []byte("search-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = Write(cfg, root); err != nil {
		t.Fatal(err)
	}

	if contents, err = os.ReadFile(rooted(root, "/etc/llama-stack/generated/open-webui.env")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "ENABLE_CONTEXT_COMPACTION=true") ||
		!strings.Contains(string(contents), "CONTEXT_COMPACTION_TOKEN_THRESHOLD=48000") ||
		!strings.Contains(string(contents), "OPENAI_API_KEY=sk-test") ||
		!strings.Contains(string(contents), "WEB_SEARCH_ENGINE=searxng") {
		t.Fatalf("compaction configuration missing:\n%s", contents)
	}

	if contents, err = os.ReadFile(rooted(root, filepath.Join(cfg.Paths.State, "generated", "downloads-nginx.conf"))); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "autoindex off") || !strings.Contains(string(contents), "disable_symlinks on") {
		t.Fatalf("download hardening missing:\n%s", contents)
	}

	if contents, err = os.ReadFile(rooted(root, "/etc/systemd/system/llama-server.service")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "User=llama-stack") || !strings.Contains(string(contents), "ProtectSystem=strict") {
		t.Fatalf("service isolation missing:\n%s", contents)
	}
	if !strings.Contains(string(contents), "XDG_RUNTIME_DIR=/run/llama-stack") ||
		!strings.Contains(string(contents), "RuntimeDirectory=llama-stack") ||
		!strings.Contains(string(contents), "Delegate=yes") ||
		strings.Contains(string(contents), "NoNewPrivileges=true") {
		t.Fatalf("rootless Podman runtime configuration missing:\n%s", contents)
	}

	if contents, err = os.ReadFile(rooted(root, cfg.Llama.MCPServersFile)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "llama-stack-search-mcp") ||
		!strings.Contains(string(contents), "http://127.0.0.1:8082") {
		t.Fatalf("search MCP configuration missing:\n%s", contents)
	}

	if contents, err = os.ReadFile(rooted(root, "/etc/containers/systemd/llama-open-webui.container")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "TimeoutStartSec=15min") ||
		!strings.Contains(string(contents), cfg.Paths.State+"/open-webui:/app/backend/data:Z") {
		t.Fatalf("Open WebUI persistence or startup timeout missing:\n%s", contents)
	}

	if contents, err = os.ReadFile(rooted(root, "/etc/systemd/system/llama-stackd.service")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), filepath.Dir(cfg.Ports.Database)) {
		t.Fatalf("resource database is not writable by llama-stackd:\n%s", contents)
	}
}
