package render

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/z46-dev/llama-stack/internal/config"
)

type (
	output struct {
		path string
		mode os.FileMode
		data string
	}

	mcpServer struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}

	mcpConfiguration struct {
		Servers map[string]mcpServer `json:"mcpServers"`
	}
)

// Write generates systemd and container configuration beneath root.
func Write(cfg config.Config, root string) (err error) {
	var (
		mcpJSON []byte
		outputs []output
	)

	if mcpJSON, err = json.MarshalIndent(buildMCPConfiguration(cfg), "", "  "); err != nil {
		return
	}
	outputs = []output{
		{path: "/etc/systemd/system/llama-stack.target", mode: 0o644, data: targetUnit(cfg)},
		{path: "/etc/systemd/system/llama-server.service", mode: 0o644, data: llamaUnit(cfg)},
		{path: "/etc/systemd/system/llama-stackd.service", mode: 0o644, data: stackdUnit(cfg)},
		{path: "/etc/systemd/system/llama-agent-tools.service", mode: 0o644, data: agentToolsUnit(cfg)},
		{path: "/etc/containers/systemd/llama-stack.network", mode: 0o644, data: networkQuadlet()},
		{path: "/etc/containers/systemd/llama-open-webui.container", mode: 0o644, data: openWebUIQuadlet(cfg)},
		{path: "/etc/containers/systemd/llama-searxng.container", mode: 0o644, data: searxngQuadlet(cfg)},
		{path: "/etc/containers/systemd/llama-downloads.container", mode: 0o644, data: downloadsQuadlet(cfg)},
		{path: cfg.Llama.MCPServersFile, mode: 0o640, data: string(mcpJSON) + "\n"},
		{path: filepath.Join(cfg.Paths.State, "generated", "downloads-nginx.conf"), mode: 0o640, data: downloadsNGINX(cfg)},
	}

	for _, item := range outputs {
		if err = writeAtomic(rooted(root, item.path), []byte(item.data), item.mode); err != nil {
			return
		}
	}

	if err = writeOpenWebUIEnvironment(cfg, root); err == nil {
		err = writeSearxngSettings(cfg, root)
	}
	return
}

// buildMCPConfiguration exposes only administrator-selected local MCP servers.
func buildMCPConfiguration(cfg config.Config) (result mcpConfiguration) {
	result.Servers = make(map[string]mcpServer)
	if cfg.SearXNG.Enabled {
		result.Servers["search"] = mcpServer{
			Command: "/usr/local/libexec/llama-stack/llama-stack-search-mcp",
			Args:    []string{},
			Env: map[string]string{
				"SEARXNG_URL": fmt.Sprintf("http://127.0.0.1:%d", cfg.SearXNG.Port),
			},
		}
	}

	return
}

// writeOpenWebUIEnvironment renders secret-bearing environment separately with restrictive permissions.
func writeOpenWebUIEnvironment(cfg config.Config, root string) (err error) {
	var (
		secret     []byte
		apiKey     []byte
		toolKey    []byte
		connections []byte
		lines      []string
		marshalErr error
	)

	if secret, err = os.ReadFile(rooted(root, cfg.OpenWebUI.SecretFile)); err != nil {
		err = fmt.Errorf("read Open WebUI secret: %w", err)
		return
	}
	if apiKey, err = os.ReadFile(rooted(root, cfg.Llama.APIKeyFile)); err != nil {
		err = fmt.Errorf("read llama API key: %w", err)
		return
	}
	if cfg.AgentTools.Enabled {
		if toolKey, err = os.ReadFile(rooted(root, cfg.AgentTools.APIKeyFile)); err != nil {
			err = fmt.Errorf("read agent tools API key: %w", err)
			return
		}
	}

	lines = []string{
		"WEBUI_SECRET_KEY=" + strings.TrimSpace(string(secret)),
		"ENABLE_OLLAMA_API=false",
		"ENABLE_OPENAI_API=true",
		fmt.Sprintf("OPENAI_API_BASE_URL=http://host.containers.internal:%d/v1", cfg.Llama.Port),
		"OPENAI_API_KEY=" + firstLine(string(apiKey)),
		"ENABLE_CONTEXT_COMPACTION=" + strconv.FormatBool(cfg.OpenWebUI.ContextCompaction),
		"CONTEXT_COMPACTION_TOKEN_THRESHOLD=" + strconv.Itoa(cfg.OpenWebUI.ContextCompactionThreshold),
		"CONTEXT_COMPACTION_TOKEN_CAP=" + strconv.Itoa(cfg.OpenWebUI.ContextCompactionTokenCap),
		"CONTEXT_COMPACTION_RETENTION_PERCENTAGE=" + strconv.Itoa(cfg.OpenWebUI.ContextCompactionRetention),
	}
	if cfg.AgentTools.Enabled {
		connections, marshalErr = json.Marshal([]map[string]any{{
			"url":       fmt.Sprintf("http://host.containers.internal:%d", cfg.AgentTools.Port),
			"path":      "/openapi.json",
			"type":      "openapi",
			"auth_type": "bearer",
			"key":       strings.TrimSpace(string(toolKey)),
			"config":    map[string]bool{"enable": true},
			"info": map[string]string{
				"id":          "llama-stack-tools",
				"name":        "llama-stack tools",
				"description": "Isolated command execution and read-only web search",
			},
		}})
		if marshalErr != nil {
			err = marshalErr
			return
		}
		lines = append(lines, "TOOL_SERVER_CONNECTIONS="+string(connections))
	}
	if cfg.SearXNG.Enabled {
		lines = append(lines,
			"ENABLE_WEB_SEARCH=true",
			"WEB_SEARCH_ENGINE=searxng",
			"SEARXNG_QUERY_URL=http://llama-searxng:8080/search?q=<query>&format=json",
		)
	}

	err = writeAtomic(rooted(root, "/etc/llama-stack/generated/open-webui.env"), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	return
}

// writeSearxngSettings creates a JSON-enabled search service configuration.
func writeSearxngSettings(cfg config.Config, root string) (err error) {
	var secret []byte

	if secret, err = os.ReadFile(rooted(root, cfg.SearXNG.SecretFile)); err != nil {
		err = fmt.Errorf("read SearXNG secret: %w", err)
		return
	}

	err = writeAtomic(
		rooted(root, filepath.Join(cfg.Paths.State, "generated", "searxng-settings.yml")),
		[]byte(fmt.Sprintf(`use_default_settings: true
server:
  bind_address: "0.0.0.0"
  port: 8080
  secret_key: %q
search:
  safe_search: 0
  formats:
    - html
    - json
`, strings.TrimSpace(string(secret)))),
		0o640,
	)
	return
}

// writeAtomic replaces a generated file without exposing partially written content.
func writeAtomic(path string, data []byte, mode os.FileMode) (err error) {
	var temporary *os.File

	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		err = fmt.Errorf("create %s: %w", filepath.Dir(path), err)
		return
	}

	if temporary, err = os.CreateTemp(filepath.Dir(path), ".llama-stack-*"); err != nil {
		err = fmt.Errorf("create temporary file for %s: %w", path, err)
		return
	}
	defer temporary.Close()

	if err = temporary.Chmod(mode); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if err == nil {
		err = temporary.Close()
	}
	if err == nil {
		err = os.Rename(temporary.Name(), path)
	}
	if err != nil {
		err = fmt.Errorf("write %s: %w", path, err)
	}

	return
}

func rooted(root, path string) (result string) {
	if root == "" {
		result = path
	} else {
		result = filepath.Join(root, strings.TrimPrefix(path, "/"))
	}

	return
}

func targetUnit(cfg config.Config) string {
	var units []string = []string{"llama-server.service", "llama-stackd.service"}
	if cfg.AgentTools.Enabled {
		units = append(units, "llama-agent-tools.service")
	}
	if cfg.OpenWebUI.Enabled {
		units = append(units, "llama-open-webui.service")
	}
	if cfg.SearXNG.Enabled {
		units = append(units, "llama-searxng.service")
	}
	if cfg.Downloads.Enabled {
		units = append(units, "llama-downloads.service")
	}

	return fmt.Sprintf(`[Unit]
Description=Local AI service stack
Wants=%s
After=network-online.target

[Install]
WantedBy=multi-user.target
`, strings.Join(units, " "))
}

func agentToolsUnit(cfg config.Config) string {
	var dependencies string
	if cfg.SearXNG.Enabled {
		dependencies = "After=llama-searxng.service\nWants=llama-searxng.service\n"
	}

	return fmt.Sprintf(`[Unit]
Description=Authenticated OpenAPI tools for llama-stack
After=network-online.target
Wants=network-online.target
%s
[Service]
Type=simple
User=%s
Group=%s
Environment=LLAMA_STACK_CONFIG=/etc/llama-stack/config.toml
Environment=HOME=%s
Environment=XDG_RUNTIME_DIR=/run/llama-stack-tools
RuntimeDirectory=llama-stack-tools
RuntimeDirectoryMode=0700
Delegate=yes
ExecStart=/usr/local/libexec/llama-stack/llama-stack-agent-tools
Restart=on-failure
RestartSec=5
TimeoutStopSec=30
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=%s %s

[Install]
WantedBy=llama-stack.target
`, dependencies, cfg.Stack.User, cfg.Stack.Group, cfg.Paths.State, cfg.Paths.State, cfg.Paths.Cache)
}

func llamaUnit(cfg config.Config) string {
	var dependencies string
	if cfg.SearXNG.Enabled {
		dependencies = "After=llama-searxng.service\nWants=llama-searxng.service\n"
	}

	return fmt.Sprintf(`[Unit]
Description=llama.cpp inference server
After=network-online.target
Wants=network-online.target
%s

[Service]
Type=simple
User=%s
Group=%s
Environment=LLAMA_STACK_CONFIG=/etc/llama-stack/config.toml
Environment=HOME=%s
Environment=XDG_RUNTIME_DIR=/run/llama-stack
RuntimeDirectory=llama-stack
RuntimeDirectoryMode=0700
Delegate=yes
ExecStart=/usr/local/bin/llama-stack internal run-llama-server
Restart=on-failure
RestartSec=5
TimeoutStopSec=45
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=%s %s

[Install]
WantedBy=llama-stack.target
`, dependencies, cfg.Stack.User, cfg.Stack.Group, cfg.Paths.State, cfg.Paths.State, cfg.Paths.Cache)
}

func stackdUnit(cfg config.Config) string {
	return fmt.Sprintf(`[Unit]
Description=llama-stack orchestration API
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
Environment=LLAMA_STACK_CONFIG=/etc/llama-stack/config.toml
ExecStart=/usr/local/libexec/llama-stack/llama-stackd
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=%s %s %s %s

[Install]
WantedBy=llama-stack.target
`, cfg.Stack.User, cfg.Stack.Group, cfg.Paths.State, cfg.Paths.Cache, filepath.Dir(cfg.Ports.Database), filepath.Dir(cfg.Jobs.Database))
}

func networkQuadlet() string {
	return `[Unit]
Description=llama-stack internal container network

[Network]
NetworkName=llama-stack
`
}

func openWebUIQuadlet(cfg config.Config) string {
	var dependencies string
	if cfg.AgentTools.Enabled {
		dependencies = "After=llama-agent-tools.service\nRequires=llama-agent-tools.service\n"
	}

	return fmt.Sprintf(`[Unit]
Description=Open WebUI for llama-stack
After=llama-server.service
Requires=llama-server.service
%s

[Container]
Image=%s
ContainerName=llama-open-webui
Network=llama-stack.network
PublishPort=%s:%d:8080
AddHost=host.containers.internal:host-gateway
Volume=%s/open-webui:/app/backend/data:Z
EnvironmentFile=/etc/llama-stack/generated/open-webui.env

[Service]
Restart=on-failure
TimeoutStartSec=15min

[Install]
WantedBy=llama-stack.target
`, dependencies, cfg.OpenWebUI.Image, cfg.OpenWebUI.Host, cfg.OpenWebUI.Port, cfg.Paths.State)
}

func searxngQuadlet(cfg config.Config) string {
	return fmt.Sprintf(`[Unit]
Description=SearXNG for llama-stack

[Container]
Image=%s
ContainerName=llama-searxng
Network=llama-stack.network
PublishPort=%s:%d:8080
Volume=%s/generated/searxng-settings.yml:/etc/searxng/settings.yml:ro,Z

[Service]
Restart=on-failure

[Install]
WantedBy=llama-stack.target
`, cfg.SearXNG.Image, cfg.SearXNG.Host, cfg.SearXNG.Port, cfg.Paths.State)
}

func downloadsQuadlet(cfg config.Config) string {
	return fmt.Sprintf(`[Unit]
Description=Read-only artifact downloads for llama-stack

[Container]
Image=%s
ContainerName=llama-downloads
Network=llama-stack.network
PublishPort=%s:%d:8080
Volume=%s:/srv:ro,z
Volume=%s/generated/downloads-nginx.conf:/etc/nginx/conf.d/default.conf:ro,Z

[Service]
Restart=on-failure

[Install]
WantedBy=llama-stack.target
`, cfg.Downloads.Image, cfg.Downloads.Host, cfg.Downloads.Port, cfg.Paths.Artifacts, cfg.Paths.State)
}

func downloadsNGINX(cfg config.Config) string {
	var autoindex string = "off"
	if cfg.Downloads.DirectoryListing {
		autoindex = "on"
	}

	return fmt.Sprintf(`server {
    listen 8080;
    server_name _;

    root /srv;
    autoindex %s;
    disable_symlinks on;

    location ~ (^|/)\. {
        deny all;
    }

    location / {
        try_files $uri =404;
        limit_except GET HEAD {
            deny all;
        }
    }
}
`, autoindex)
}

func firstLine(value string) (line string) {
	line, _, _ = strings.Cut(strings.TrimSpace(value), "\n")
	return
}
