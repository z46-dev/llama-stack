package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/alexflint/go-arg"
	"github.com/z46-dev/llama-stack/internal/config"
	"github.com/z46-dev/llama-stack/internal/doctor"
	"github.com/z46-dev/llama-stack/internal/privilege"
	"github.com/z46-dev/llama-stack/internal/render"
	"github.com/z46-dev/llama-stack/internal/resourceapi"
)

var version string = "development"

const (
	toolboxBuildContext string = "/usr/share/llama-stack/toolbox"
	toolboxRuntimePath  string = "/run/llama-stack"
)

type (
	commandOptions struct {
		Doctor   *doctorOptions   `arg:"subcommand:doctor" help:"inspect the host and installed stack"`
		Install  *installOptions  `arg:"subcommand:install" help:"install and enable the stack"`
		Render   *renderOptions   `arg:"subcommand:render" help:"regenerate service configuration"`
		Model    *modelOptions    `arg:"subcommand:model" help:"manage local GGUF models"`
		Resource *resourceOptions `arg:"subcommand:resource" help:"manage agent resources"`
		Version  *versionOptions  `arg:"subcommand:version" help:"print the CLI version"`
		Internal *internalOptions `arg:"subcommand:internal" help:"run internal service entry points"`
	}
	doctorOptions struct {
		Config string
	}
	installOptions struct {
		Config  string
		NoStart bool `arg:"--no-start" help:"install without starting services"`
	}
	renderOptions struct {
		Config string
		Root   string `help:"render beneath an alternate root"`
	}
	modelOptions struct {
		Add  *modelAddOptions `arg:"subcommand:add"`
		List *emptyOptions    `arg:"subcommand:list"`
	}
	modelAddOptions struct {
		Source string `arg:"positional,required"`
	}
	resourceOptions struct {
		Capability *capabilityOptions `arg:"subcommand:capability"`
		Ports      *portOptions       `arg:"subcommand:ports"`
	}
	capabilityOptions struct {
		Issue  *capabilityIssueOptions  `arg:"subcommand:issue"`
		Revoke *capabilityRevokeOptions `arg:"subcommand:revoke"`
	}
	capabilityIssueOptions struct {
		User       string `arg:"required"`
		Run        string `arg:"required"`
		Workspace  string `arg:"required"`
		TargetHost string `arg:"--target-host,required"`
		TTL        int
	}
	capabilityRevokeOptions struct {
		ID string `arg:"positional,required"`
	}
	portOptions struct {
		List   *emptyOptions      `arg:"subcommand:list"`
		Revoke *portRevokeOptions `arg:"subcommand:revoke"`
	}
	portRevokeOptions struct {
		ID string `arg:"positional,required"`
	}
	internalOptions struct {
		RunLlamaServer *emptyOptions `arg:"subcommand:run-llama-server"`
	}
	emptyOptions   struct{}
	versionOptions struct{}
)

// main dispatches the administrative command-line interface.
func main() {
	var err error

	if err = run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "llama-stack: %v\n", err)
		os.Exit(1)
	}
}

// run parses one command and executes it.
func run(args []string) (err error) {
	var (
		options commandOptions
		parser  *arg.Parser
	)

	if parser, err = arg.NewParser(arg.Config{Program: "llama-stack"}, &options); err != nil {
		return
	}
	if err = parser.Parse(args); err != nil {
		if errors.Is(err, arg.ErrHelp) {
			parser.WriteHelp(os.Stdout)
			err = nil
		}
		return
	}

	switch {
	case options.Doctor != nil:
		resolveConfigPath(&options.Doctor.Config)
		err = runDoctor(*options.Doctor)
	case options.Install != nil:
		resolveConfigPath(&options.Install.Config)
		err = runInstall(*options.Install)
	case options.Render != nil:
		resolveConfigPath(&options.Render.Config)
		err = runRender(*options.Render)
	case options.Model != nil:
		err = runModel(*options.Model)
	case options.Resource != nil:
		err = runResource(*options.Resource)
	case options.Version != nil:
		fmt.Println(version)
	case options.Internal != nil && options.Internal.RunLlamaServer != nil:
		err = runLlamaServer()
	default:
		parser.WriteHelp(os.Stderr)
		err = errors.New("a command is required")
	}

	return
}

// resolveConfigPath preserves the environment-based recovery override when no flag is supplied.
func resolveConfigPath(path *string) {
	if *path == "" {
		*path = config.Path()
	}
}

// runDoctor validates the config before inspecting host and service health.
func runDoctor(options doctorOptions) (err error) {
	var (
		cfg    config.Config
		report doctor.Report
	)

	if cfg, err = config.Load(options.Config); err != nil {
		return
	}

	report = doctor.Run(cfg)
	for _, check := range report.Checks {
		fmt.Printf("%-5s %-34s %s\n", check.Status, check.Name, check.Message)
	}

	if !report.Healthy() {
		err = errors.New("one or more required checks failed")
	}

	return
}

// runInstall initializes persistent state and enables the rendered stack.
func runInstall(options installOptions) (err error) {
	var (
		cfg config.Config
	)

	if cfg, err = config.Load(options.Config); err != nil {
		return
	}
	if err = privilege.RequireAdmin(cfg.Stack.AdminGroup); err != nil {
		return
	}
	if err = initializeState(cfg); err != nil {
		return
	}
	if err = buildToolboxImage(cfg); err != nil {
		return
	}
	if err = pullServiceImages(cfg); err != nil {
		return
	}
	if err = render.Write(cfg, ""); err != nil {
		return
	}
	if err = setGeneratedOwnership(cfg); err != nil {
		return
	}
	if err = runCommand("systemctl", "daemon-reload"); err != nil {
		return
	}

	if options.NoStart {
		fmt.Println("Stack installed but not started (--no-start).")
	} else if err = runCommand("systemctl", "enable", "--now", "llama-stack.target"); err == nil {
		fmt.Println("Stack installed and started.")
	}

	return
}

// runRender regenerates managed systemd and Quadlet files.
func runRender(options renderOptions) (err error) {
	var (
		cfg config.Config
	)

	if cfg, err = config.Load(options.Config); err != nil {
		return
	}
	if err = privilege.RequireAdmin(cfg.Stack.AdminGroup); err != nil {
		return
	}

	err = render.Write(cfg, options.Root)
	if err == nil && options.Root == "" {
		err = setGeneratedOwnership(cfg)
	}
	if err == nil && options.Root == "" {
		err = runCommand("systemctl", "daemon-reload")
	}
	return
}

// runModel dispatches local model management.
func runModel(options modelOptions) (err error) {
	var cfg config.Config

	if cfg, err = config.Load(config.Path()); err != nil {
		return
	}
	switch {
	case options.Add != nil:
		if err = privilege.RequireAdmin(cfg.Stack.AdminGroup); err == nil {
			err = addLocalModel(cfg, options.Add.Source)
		}
	case options.List != nil:
		err = listModels(cfg)
	default:
		err = errors.New("model requires add or list")
	}

	return
}

// initializeState creates secrets and persistent directories with service ownership.
func initializeState(cfg config.Config) (err error) {
	var (
		uid int
		gid int
	)

	if uid, gid, err = serviceIdentity(cfg.Stack.User); err != nil {
		return
	}

	for _, path := range []string{cfg.Paths.State, cfg.Paths.Cache, cfg.Paths.Models, cfg.Paths.Users, cfg.Paths.Artifacts, filepath.Join(cfg.Paths.State, "open-webui"), filepath.Dir(cfg.Llama.APIKeyFile), filepath.Dir(cfg.OpenWebUI.SecretFile), filepath.Dir(cfg.SearXNG.SecretFile), filepath.Dir(cfg.Ports.AdminTokenFile), filepath.Dir(cfg.Ports.Database), filepath.Dir(cfg.Jobs.Database)} {
		if err = os.MkdirAll(path, 0o750); err != nil {
			return
		}
		if err = os.Chown(path, uid, gid); err != nil {
			return
		}
	}

	if err = ensureSecret(cfg.Llama.APIKeyFile, "sk-", uid, gid); err == nil {
		err = ensureSecret(cfg.OpenWebUI.SecretFile, "", uid, gid)
	}
	if err == nil {
		err = ensureSecret(cfg.SearXNG.SecretFile, "", uid, gid)
	}
	if err == nil {
		err = ensureSecret(cfg.Ports.AdminTokenFile, "lsadmin_", uid, gid)
	}

	return
}

// buildToolboxImage prepares the configured image in the service user's rootless Podman storage.
func buildToolboxImage(cfg config.Config) (err error) {
	var (
		uid  int
		gid  int
		args []string
	)

	if !cfg.Toolbox.Enabled {
		return
	}
	if uid, gid, err = serviceIdentity(cfg.Stack.User); err != nil {
		return
	}
	if err = os.MkdirAll(toolboxRuntimePath, 0o700); err != nil {
		return
	}
	if err = os.Chmod(toolboxRuntimePath, 0o700); err != nil {
		return
	}
	if err = os.Chown(toolboxRuntimePath, uid, gid); err != nil {
		return
	}

	args = []string{
		"--user", cfg.Stack.User,
		"--", "env",
		"HOME=" + cfg.Paths.State,
		"XDG_RUNTIME_DIR=" + toolboxRuntimePath,
		"podman", "build",
		"--tag", cfg.Toolbox.Image,
		toolboxBuildContext,
	}
	fmt.Printf("Building toolbox image %s as %s...\n", cfg.Toolbox.Image, cfg.Stack.User)
	err = runCommandAt("/", "runuser", args...)
	return
}

// pullServiceImages downloads enabled infrastructure images before systemd starts them.
func pullServiceImages(cfg config.Config) (err error) {
	for _, image := range enabledServiceImages(cfg) {
		fmt.Printf("Pulling service image %s...\n", image)
		if err = runCommand("podman", "pull", image); err != nil {
			return
		}
	}

	return
}

// enabledServiceImages returns each enabled infrastructure image once.
func enabledServiceImages(cfg config.Config) (images []string) {
	var seen map[string]bool = make(map[string]bool)

	for _, service := range []config.Service{cfg.OpenWebUI.Service, cfg.SearXNG.Service, cfg.Downloads.Service} {
		if service.Enabled && !seen[service.Image] {
			images = append(images, service.Image)
			seen[service.Image] = true
		}
	}

	return
}

// runResource dispatches administrator-only resource broker operations.
func runResource(options resourceOptions) (err error) {
	var cfg config.Config

	if cfg, err = config.Load(config.Path()); err != nil {
		return
	}
	if err = privilege.RequireAdmin(cfg.Stack.AdminGroup); err != nil {
		return
	}
	switch {
	case options.Capability != nil:
		err = runCapability(cfg, *options.Capability)
	case options.Ports != nil:
		err = runResourcePorts(cfg, *options.Ports)
	default:
		err = errors.New("resource requires capability or ports")
	}
	return
}

func runCapability(cfg config.Config, options capabilityOptions) (err error) {
	var (
		client   resourceapi.Client
		response resourceapi.IssueCapabilityResponse
	)

	if options.Revoke != nil {
		if client, err = administratorClient(cfg); err == nil {
			err = client.RevokeCapability(context.Background(), options.Revoke.ID)
		}
		return
	}
	if options.Issue == nil {
		err = errors.New("resource capability requires issue or revoke")
		return
	}
	if options.Issue.TTL == 0 {
		options.Issue.TTL = cfg.Ports.MaximumTTL
	}
	if client, err = administratorClient(cfg); err != nil {
		return
	}
	if response, err = client.IssueCapability(context.Background(), resourceapi.IssueCapabilityRequest{
		UserID:      options.Issue.User,
		RunID:       options.Issue.Run,
		WorkspaceID: options.Issue.Workspace,
		TargetHost:  options.Issue.TargetHost,
		TTLSeconds:  options.Issue.TTL,
	}); err == nil {
		err = json.NewEncoder(os.Stdout).Encode(response)
	}
	return
}

func runResourcePorts(cfg config.Config, options portOptions) (err error) {
	var (
		client resourceapi.Client
		leases any
	)

	if client, err = administratorClient(cfg); err != nil {
		return
	}
	switch {
	case options.List != nil:
		if leases, err = client.ListAll(context.Background()); err == nil {
			err = json.NewEncoder(os.Stdout).Encode(leases)
		}
	case options.Revoke != nil:
		err = client.Revoke(context.Background(), options.Revoke.ID)
	default:
		err = errors.New("resource ports requires list or revoke")
	}
	return
}

func administratorClient(cfg config.Config) (client resourceapi.Client, err error) {
	var token []byte

	if token, err = os.ReadFile(cfg.Ports.AdminTokenFile); err != nil {
		return
	}
	client = resourceapi.Client{
		BaseURL:    "http://" + cfg.Orchestrator.Host + ":" + strconv.Itoa(cfg.Orchestrator.Port),
		AdminToken: strings.TrimSpace(string(token)),
	}
	return
}

// setGeneratedOwnership makes secret-bearing and bind-mounted files available only to the service account.
func setGeneratedOwnership(cfg config.Config) (err error) {
	var (
		uid   int
		gid   int
		paths []string
	)

	if uid, gid, err = serviceIdentity(cfg.Stack.User); err != nil {
		return
	}

	paths = []string{
		"/etc/llama-stack/generated/open-webui.env",
		cfg.Llama.MCPServersFile,
		filepath.Join(cfg.Paths.State, "generated"),
		filepath.Join(cfg.Paths.State, "generated", "searxng-settings.yml"),
		filepath.Join(cfg.Paths.State, "generated", "downloads-nginx.conf"),
	}
	for _, path := range paths {
		if err = os.Chown(path, uid, gid); err != nil {
			return
		}
	}

	return
}

// serviceIdentity resolves the numeric ownership used by native and rootless services.
func serviceIdentity(username string) (uid, gid int, err error) {
	var account *user.User

	if account, err = user.Lookup(username); err != nil {
		err = fmt.Errorf("service account %q is missing: %w", username, err)
		return
	}
	if uid, err = strconv.Atoi(account.Uid); err != nil {
		return
	}
	if gid, err = strconv.Atoi(account.Gid); err != nil {
		return
	}

	return
}

// ensureSecret creates a random secret once and leaves existing values intact.
func ensureSecret(path, prefix string, uid, gid int) (err error) {
	var value []byte = make([]byte, 32)

	if _, err = os.Stat(path); err == nil {
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		return
	}

	if _, err = rand.Read(value); err != nil {
		return
	}
	if err = os.WriteFile(path, []byte(prefix+hex.EncodeToString(value)+"\n"), 0o640); err == nil {
		err = os.Chown(path, uid, gid)
	}

	return
}

// addLocalModel atomically imports a local GGUF into managed model storage.
func addLocalModel(cfg config.Config, sourceArgument string) (err error) {
	var (
		source      string
		destination string
		info        os.FileInfo
		input       *os.File
		output      *os.File
		uid         int
		gid         int
		written     int64
	)

	if source, err = filepath.Abs(sourceArgument); err != nil {
		return
	}
	if filepath.Ext(strings.ToLower(source)) != ".gguf" {
		err = errors.New("model must have a .gguf extension")
		return
	}
	if info, err = os.Stat(source); err != nil {
		return
	}
	if !info.Mode().IsRegular() {
		err = errors.New("model source is not a regular file")
		return
	}

	destination = filepath.Join(cfg.Paths.Models, filepath.Base(source))
	if source == destination {
		fmt.Printf("Model already resides in managed storage: %s\n", destination)
		return
	}
	if _, err = os.Lstat(destination); err == nil {
		err = fmt.Errorf("model %s is already registered", filepath.Base(destination))
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		return
	}
	if input, err = os.Open(source); err != nil {
		return
	}
	defer input.Close()
	if output, err = os.CreateTemp(cfg.Paths.Models, ".model-*.gguf"); err != nil {
		return
	}
	defer func() {
		if err != nil {
			_ = os.Remove(output.Name())
		}
	}()

	if written, err = io.Copy(output, input); err == nil && written != info.Size() {
		err = fmt.Errorf("short model copy: wrote %d of %d bytes", written, info.Size())
	}
	if err == nil {
		err = output.Sync()
	}
	if err == nil {
		err = output.Chmod(0o640)
	}
	if err == nil {
		err = output.Close()
	}
	if err != nil {
		return
	}

	if uid, gid, err = serviceIdentity(cfg.Stack.User); err != nil {
		return
	}
	if err = os.Chown(output.Name(), uid, gid); err != nil {
		return
	}
	if err = os.Rename(output.Name(), destination); err != nil {
		return
	}

	fmt.Printf("Imported %s (%d bytes)\n", destination, written)
	err = runCommand("systemctl", "try-reload-or-restart", "llama-server.service")
	return
}

// listModels lists only model files registered in the configured model directory.
func listModels(cfg config.Config) (err error) {
	var entries []os.DirEntry

	if entries, err = os.ReadDir(cfg.Paths.Models); err != nil {
		return
	}
	for _, entry := range entries {
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".gguf") {
			fmt.Println(entry.Name())
		}
	}

	return
}

// runLlamaServer replaces the helper process with llama-server using validated configuration.
func runLlamaServer() (err error) {
	var (
		cfg config.Config
		env []string
	)

	if cfg, err = config.Load(config.Path()); err != nil {
		return
	}

	env = append(os.Environ(), "LLAMA_CACHE="+cfg.Paths.Cache)
	err = syscall.Exec(cfg.Llama.Binary, llamaServerArgs(cfg), env)
	return
}

// llamaServerArgs converts validated configuration into the native server command line.
func llamaServerArgs(cfg config.Config) (args []string) {
	args = []string{
		cfg.Llama.Binary,
		"--host", cfg.Llama.Host,
		"--port", strconv.Itoa(cfg.Llama.Port),
		"--models-dir", cfg.Paths.Models,
		"--models-max", strconv.Itoa(cfg.Llama.ModelsMax),
		"--ctx-size", strconv.Itoa(cfg.Llama.ContextSize),
		"--n-gpu-layers", cfg.Llama.GPULayers,
		"--split-mode", cfg.Llama.SplitMode,
		"--tensor-split", cfg.Llama.TensorSplit,
		"--sleep-idle-seconds", strconv.Itoa(cfg.Llama.IdleTimeout),
		"--api-key-file", cfg.Llama.APIKeyFile,
	}
	if cfg.Llama.Jinja {
		args = append(args, "--jinja")
	}
	if cfg.Llama.Tools != "" {
		args = append(args, "--tools", cfg.Llama.Tools)
	}
	if cfg.Toolbox.Enabled {
		args = append(args, "--tools-runtime", cfg.Toolbox.Runtime+":"+cfg.Toolbox.Image)
	}
	if cfg.SearXNG.Enabled {
		args = append(args, "--mcp-servers-config", cfg.Llama.MCPServersFile)
	}

	return
}

// runCommand executes a system administration command with inherited output.
func runCommand(command string, args ...string) (err error) {
	err = runCommandAt("", command, args...)
	return
}

// runCommandAt executes a system administration command from a safe working directory.
func runCommandAt(directory, command string, args ...string) (err error) {
	var process *exec.Cmd = exec.Command(command, args...)
	process.Dir = directory
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	process.Stdin = os.Stdin
	err = process.Run()
	return
}
