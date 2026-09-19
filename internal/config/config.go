package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	DefaultPath              = "/etc/llama-stack/config.toml"
	DefaultAdminGroup        = "llama-stack-admins"
	DefaultModelProfilesFile = "/usr/share/llama-stack/model-profiles.toml"
)

type (
	Config struct {
		Stack        Stack        `toml:"stack"`
		Paths        Paths        `toml:"paths"`
		GPU          GPU          `toml:"gpu"`
		Llama        Llama        `toml:"llama"`
		OpenWebUI    OpenWebUI    `toml:"open_webui"`
		AgentTools   AgentTools   `toml:"agent_tools"`
		SearXNG      SearXNG      `toml:"searxng"`
		Downloads    Downloads    `toml:"downloads"`
		Toolbox      Toolbox      `toml:"toolbox"`
		Ports        Ports        `toml:"ports"`
		Jobs         Jobs         `toml:"jobs"`
		Orchestrator Orchestrator `toml:"orchestrator"`
	}

	Stack struct {
		User       string `toml:"user"`
		Group      string `toml:"group"`
		AdminGroup string `toml:"admin_group"`
	}

	Paths struct {
		State     string `toml:"state"`
		Cache     string `toml:"cache"`
		Models    string `toml:"models"`
		Users     string `toml:"users"`
		Artifacts string `toml:"artifacts"`
	}

	GPU struct {
		Enabled           bool     `toml:"enabled"`
		RepositoryURL     string   `toml:"repository_url"`
		DriverPackages    []string `toml:"driver_packages"`
		ToolkitPackages   []string `toml:"toolkit_packages"`
		CUDAArchitectures []string `toml:"cuda_architectures"`
	}

	Llama struct {
		Repository        string `toml:"repository"`
		Revision          string `toml:"revision"`
		SourceDirectory   string `toml:"source_directory"`
		Binary            string `toml:"binary"`
		Host              string `toml:"host"`
		Port              int    `toml:"port"`
		ContextSize       int    `toml:"context_size"`
		ModelsMax         int    `toml:"models_max"`
		GPULayers         string `toml:"gpu_layers"`
		SplitMode         string `toml:"split_mode"`
		TensorSplit       string `toml:"tensor_split"`
		IdleTimeout       int    `toml:"idle_timeout_seconds"`
		APIKeyFile        string `toml:"api_key_file"`
		Jinja             bool   `toml:"jinja"`
		Tools             string `toml:"tools"`
		MCPServersFile    string `toml:"mcp_servers_file"`
		ModelsPresetFile  string `toml:"models_preset_file"`
		ModelProfilesFile string `toml:"model_profiles_file"`
	}

	OpenWebUI struct {
		Service
		SecretFile                 string `toml:"secret_file"`
		BuiltinTools               bool   `toml:"builtin_tools"`
		AutoSelectTools            bool   `toml:"auto_select_tools"`
		FunctionCalling            string `toml:"function_calling"`
		ContextCompaction          bool   `toml:"context_compaction"`
		ContextCompactionThreshold int    `toml:"context_compaction_threshold"`
		ContextCompactionTokenCap  int    `toml:"context_compaction_token_cap"`
		ContextCompactionRetention int    `toml:"context_compaction_retention_percent"`
	}

	AgentTools struct {
		Enabled         bool   `toml:"enabled"`
		Host            string `toml:"host"`
		Port            int    `toml:"port"`
		APIKeyFile      string `toml:"api_key_file"`
		MaxOutput       int    `toml:"max_output_bytes"`
		RequireIdentity bool   `toml:"require_identity"`
	}

	Service struct {
		Enabled bool   `toml:"enabled"`
		Image   string `toml:"image"`
		Host    string `toml:"host"`
		Port    int    `toml:"port"`
	}

	SearXNG struct {
		Service
		SecretFile string `toml:"secret_file"`
	}

	Downloads struct {
		Service
		DirectoryListing bool `toml:"directory_listing"`
	}

	Toolbox struct {
		Enabled          bool    `toml:"enabled"`
		Runtime          string  `toml:"runtime"`
		Image            string  `toml:"image"`
		Memory           string  `toml:"memory"`
		CPUs             float64 `toml:"cpus"`
		PIDs             int     `toml:"pids"`
		ExecutionTimeout int     `toml:"execution_timeout_seconds"`
		IdleTimeout      int     `toml:"idle_timeout_seconds"`
	}

	Ports struct {
		Enabled        bool   `toml:"enabled"`
		ListenHost     string `toml:"listen_host"`
		Start          int    `toml:"start"`
		End            int    `toml:"end"`
		Database       string `toml:"database"`
		AdminTokenFile string `toml:"admin_token_file"`
		DefaultTTL     int    `toml:"default_ttl_seconds"`
		MaximumTTL     int    `toml:"maximum_ttl_seconds"`
		MaximumPerRun  int    `toml:"maximum_per_run"`
		MaximumPerUser int    `toml:"maximum_per_user"`
		MaximumWait    int    `toml:"maximum_wait_seconds"`
	}

	Jobs struct {
		Database            string `toml:"database"`
		PollInterval        int    `toml:"poll_interval_milliseconds"`
		RecoveryTimeout     int    `toml:"recovery_timeout_seconds"`
		DatabaseLockRetries int    `toml:"database_lock_retries"`
		DatabaseLockDelay   int    `toml:"database_lock_delay_milliseconds"`
	}

	Orchestrator struct {
		Host string `toml:"host"`
		Port int    `toml:"port"`
	}
)

// Load reads and validates the stack configuration at path.
func Load(path string) (cfg Config, err error) {
	var metadata toml.MetaData

	if metadata, err = toml.DecodeFile(path, &cfg); err != nil {
		err = fmt.Errorf("decode %s: %w", path, err)
		return
	}
	if cfg.Llama.ModelsPresetFile == "" {
		cfg.Llama.ModelsPresetFile = filepath.Join(cfg.Paths.State, "generated", "model-presets.ini")
	}
	if cfg.Llama.ModelProfilesFile == "" {
		cfg.Llama.ModelProfilesFile = DefaultModelProfilesFile
	}
	if cfg.OpenWebUI.FunctionCalling == "" {
		cfg.OpenWebUI.FunctionCalling = "native"
	}
	if !metadata.IsDefined("open_webui", "auto_select_tools") {
		cfg.OpenWebUI.AutoSelectTools = true
	}

	err = cfg.Validate()
	return
}

// Validate rejects unsafe or internally inconsistent configuration.
func (cfg Config) Validate() (err error) {
	var (
		serviceNames  []string = []string{"stack.user", "stack.group", "stack.admin_group"}
		serviceValues []string = []string{cfg.Stack.User, cfg.Stack.Group, cfg.Stack.AdminGroup}
		paths         []string = []string{cfg.Paths.State, cfg.Paths.Cache, cfg.Paths.Models, cfg.Paths.Users, cfg.Paths.Artifacts}
	)

	for index, value := range serviceValues {
		if strings.TrimSpace(value) == "" {
			err = fmt.Errorf("%s must not be empty", serviceNames[index])
			return
		}
	}

	if cfg.Stack.AdminGroup != DefaultAdminGroup {
		err = fmt.Errorf("stack.admin_group must be %q", DefaultAdminGroup)
		return
	}

	for _, path := range paths {
		if !filepath.IsAbs(path) {
			err = fmt.Errorf("managed path %q must be absolute", path)
			return
		}
	}

	if !filepath.IsAbs(cfg.Llama.Binary) || !filepath.IsAbs(cfg.Llama.APIKeyFile) ||
		!filepath.IsAbs(cfg.Llama.MCPServersFile) || !filepath.IsAbs(cfg.Llama.ModelsPresetFile) ||
		!filepath.IsAbs(cfg.Llama.ModelProfilesFile) ||
		!filepath.IsAbs(cfg.OpenWebUI.SecretFile) || !filepath.IsAbs(cfg.SearXNG.SecretFile) ||
		(cfg.AgentTools.Enabled && !filepath.IsAbs(cfg.AgentTools.APIKeyFile)) ||
		!filepath.IsAbs(cfg.Ports.Database) || !filepath.IsAbs(cfg.Ports.AdminTokenFile) ||
		!filepath.IsAbs(cfg.Jobs.Database) {
		err = errors.New("binary, generated, and secret file paths must be absolute")
		return
	}

	if net.ParseIP(cfg.Llama.Host) == nil || net.ParseIP(cfg.OpenWebUI.Host) == nil ||
		net.ParseIP(cfg.SearXNG.Host) == nil || net.ParseIP(cfg.Downloads.Host) == nil ||
		net.ParseIP(cfg.Ports.ListenHost) == nil || net.ParseIP(cfg.Orchestrator.Host) == nil {
		err = errors.New("service hosts must be IP addresses")
		return
	}

	for name, port := range map[string]int{
		"llama.port":        cfg.Llama.Port,
		"open_webui.port":   cfg.OpenWebUI.Port,
		"searxng.port":      cfg.SearXNG.Port,
		"downloads.port":    cfg.Downloads.Port,
		"orchestrator.port": cfg.Orchestrator.Port,
	} {
		if port < 1 || port > 65535 {
			err = fmt.Errorf("%s must be between 1 and 65535", name)
			return
		}
	}
	if cfg.AgentTools.Enabled {
		if net.ParseIP(cfg.AgentTools.Host) == nil {
			err = errors.New("agent_tools.host must be an IP address")
			return
		}
		if cfg.AgentTools.Port < 1 || cfg.AgentTools.Port > 65535 {
			err = errors.New("agent_tools.port must be between 1 and 65535")
			return
		}
		if cfg.AgentTools.MaxOutput < 1024 {
			err = errors.New("agent_tools.max_output_bytes must be at least 1024")
			return
		}
	}

	if cfg.OpenWebUI.ContextCompactionThreshold >= cfg.Llama.ContextSize {
		err = errors.New("context compaction threshold must be below llama context size")
		return
	}
	if cfg.OpenWebUI.FunctionCalling != "native" && cfg.OpenWebUI.FunctionCalling != "legacy" {
		err = errors.New("open_webui.function_calling must be native or legacy")
		return
	}

	if cfg.OpenWebUI.ContextCompactionTokenCap < cfg.OpenWebUI.ContextCompactionThreshold {
		err = errors.New("context compaction token cap must not be below its threshold")
		return
	}

	if cfg.Toolbox.Enabled {
		if cfg.Toolbox.Runtime != "podman" {
			err = errors.New("toolbox.runtime must be podman")
			return
		}
		if strings.TrimSpace(cfg.Toolbox.Image) == "" || strings.TrimSpace(cfg.Llama.Tools) == "" {
			err = errors.New("enabled toolbox requires toolbox.image and llama.tools")
			return
		}
	}

	if cfg.Ports.Start < 1024 || cfg.Ports.Start > cfg.Ports.End || cfg.Ports.End > 65535 {
		err = errors.New("ports range must be ordered, unprivileged, and within 1024-65535")
		return
	}
	if cfg.Ports.DefaultTTL < 1 || cfg.Ports.DefaultTTL > cfg.Ports.MaximumTTL ||
		cfg.Ports.MaximumPerRun < 1 || cfg.Ports.MaximumPerUser < cfg.Ports.MaximumPerRun ||
		cfg.Ports.MaximumWait < 0 {
		err = errors.New("port TTL, quota, or wait configuration is invalid")
		return
	}
	if cfg.Jobs.PollInterval < 1 || cfg.Jobs.RecoveryTimeout < 1 ||
		cfg.Jobs.DatabaseLockRetries < 1 || cfg.Jobs.DatabaseLockDelay < 1 {
		err = errors.New("job scheduler timing and retry configuration must be positive")
	}

	return
}

// Path returns the configured path, allowing LLAMA_STACK_CONFIG for tests and recovery.
func Path() (path string) {
	if path = os.Getenv("LLAMA_STACK_CONFIG"); path == "" {
		path = DefaultPath
	}

	return
}
