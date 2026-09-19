package doctor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/z46-dev/llama-stack/internal/config"
)

type (
	Status string

	Check struct {
		Name    string
		Status  Status
		Message string
	}

	Report struct {
		Checks []Check
	}
)

const (
	Pass Status = "PASS"
	Warn Status = "WARN"
	Fail Status = "FAIL"
)

// Run inspects the host without changing it and returns an actionable report.
func Run(cfg config.Config) (report Report) {
	report.Checks = append(report.Checks, checkOperatingSystem())
	report.Checks = append(report.Checks, checkCommand("Podman", "podman", "--version"))
	report.Checks = append(report.Checks, checkCommand("NVIDIA driver", "nvidia-smi", "--query-gpu=name,driver_version,memory.total", "--format=csv,noheader"))
	report.Checks = append(report.Checks, checkCommand("CUDA compiler", "nvcc", "--version"))
	report.Checks = append(report.Checks, checkFile("llama-server", cfg.Llama.Binary, 0o111))
	report.Checks = append(report.Checks, checkFile("llama API key", cfg.Llama.APIKeyFile, 0o400))
	report.Checks = append(report.Checks, checkFile("Open WebUI secret", cfg.OpenWebUI.SecretFile, 0o400))
	report.Checks = append(report.Checks, checkFile("SearXNG secret", cfg.SearXNG.SecretFile, 0o400))
	report.Checks = append(report.Checks, checkFile("Resource administrator token", cfg.Ports.AdminTokenFile, 0o400))
	if cfg.SearXNG.Enabled {
		report.Checks = append(report.Checks, checkFile("Search MCP server", "/usr/local/libexec/llama-stack/llama-stack-search-mcp", 0o111))
		report.Checks = append(report.Checks, checkFile("MCP server configuration", cfg.Llama.MCPServersFile, 0o400))
	}
	if cfg.AgentTools.Enabled {
		report.Checks = append(report.Checks, checkFile("Agent tools server", "/usr/local/libexec/llama-stack/llama-stack-agent-tools", 0o111))
		report.Checks = append(report.Checks, checkFile("Agent tools API key", cfg.AgentTools.APIKeyFile, 0o400))
	}
	report.Checks = append(report.Checks, checkManagedDirectories(cfg)...)
	report.Checks = append(report.Checks, checkLlamaDevices(cfg.Llama.Binary))
	report.Checks = append(report.Checks, checkService("llama-server.service"))
	report.Checks = append(report.Checks, checkService("llama-stackd.service"))
	if cfg.AgentTools.Enabled {
		report.Checks = append(report.Checks, checkService("llama-agent-tools.service"))
	}

	if cfg.OpenWebUI.Enabled {
		report.Checks = append(report.Checks, checkService("llama-open-webui.service"))
	}

	if cfg.SearXNG.Enabled {
		report.Checks = append(report.Checks, checkService("llama-searxng.service"))
	}

	if cfg.Downloads.Enabled {
		report.Checks = append(report.Checks, checkService("llama-downloads.service"))
	}

	return
}

// Healthy reports whether every required doctor check passed.
func (report Report) Healthy() (healthy bool) {
	healthy = true
	for _, check := range report.Checks {
		if check.Status == Fail {
			healthy = false
			break
		}
	}

	return
}

// checkOperatingSystem verifies the supported Fedora release.
func checkOperatingSystem() (check Check) {
	var (
		contents []byte
		fields   map[string]string = make(map[string]string)
		err      error
	)

	check.Name = "Operating system"
	if contents, err = os.ReadFile("/etc/os-release"); err != nil {
		check.Status = Fail
		check.Message = err.Error()
		return
	}

	for _, line := range strings.Split(string(contents), "\n") {
		var (
			key   string
			value string
			found bool
		)

		key, value, found = strings.Cut(line, "=")
		if found {
			fields[key] = strings.Trim(value, `"`)
		}
	}

	if fields["ID"] != "fedora" {
		check.Status = Fail
		check.Message = fmt.Sprintf("unsupported distribution %q; Fedora 44 is required", fields["ID"])
	} else if fields["VERSION_ID"] != "44" {
		check.Status = Warn
		check.Message = fmt.Sprintf("Fedora %s detected; this release targets Fedora 44", fields["VERSION_ID"])
	} else {
		check.Status = Pass
		check.Message = "Fedora 44"
	}

	return
}

// checkCommand verifies that a required executable runs successfully.
func checkCommand(name, command string, args ...string) (check Check) {
	var (
		output string
		err    error
	)

	check.Name = name
	if output, err = run(command, args...); err != nil {
		check.Status = Fail
		check.Message = err.Error()
	} else {
		check.Status = Pass
		check.Message = firstLine(output)
	}

	return
}

// checkFile validates a required file and its minimum permission bits.
func checkFile(name, path string, requiredMode os.FileMode) (check Check) {
	var (
		info os.FileInfo
		err  error
	)

	check.Name = name
	if info, err = os.Stat(path); err != nil {
		if errors.Is(err, os.ErrPermission) {
			check.Status = Warn
			check.Message = fmt.Sprintf("%s is protected; rerun doctor with sudo for a full check", path)
		} else {
			check.Status = Fail
			check.Message = fmt.Sprintf("%s: %v", path, err)
		}
	} else if info.IsDir() {
		check.Status = Fail
		check.Message = fmt.Sprintf("%s is a directory", path)
	} else if info.Mode().Perm()&requiredMode == 0 {
		check.Status = Fail
		check.Message = fmt.Sprintf("%s lacks required mode %04o", path, requiredMode)
	} else {
		check.Status = Pass
		check.Message = path
	}

	return
}

// checkManagedDirectories confirms that persistent stack directories exist.
func checkManagedDirectories(cfg config.Config) (checks []Check) {
	var paths []string = []string{cfg.Paths.State, cfg.Paths.Cache, cfg.Paths.Models, cfg.Paths.Users, cfg.Paths.Artifacts}
	if cfg.OpenWebUI.Enabled {
		paths = append(paths, filepath.Join(cfg.Paths.State, "open-webui"))
	}

	for _, path := range paths {
		var (
			check Check = Check{Name: "Directory " + path}
			info  os.FileInfo
			err   error
		)

		if info, err = os.Stat(path); err != nil {
			check.Status = Fail
			check.Message = err.Error()
		} else if !info.IsDir() {
			check.Status = Fail
			check.Message = "not a directory"
		} else {
			check.Status = Pass
			check.Message = "present"
		}

		checks = append(checks, check)
	}

	return
}

// checkLlamaDevices verifies that the installed server can see a CUDA device.
func checkLlamaDevices(binary string) (check Check) {
	var (
		output string
		err    error
	)

	check.Name = "llama.cpp CUDA devices"
	if _, err = os.Stat(binary); err != nil {
		check.Status = Fail
		check.Message = "llama-server is not installed"
	} else if output, err = run(binary, "--list-devices"); err != nil {
		check.Status = Fail
		check.Message = err.Error()
	} else if !strings.Contains(output, "CUDA") {
		check.Status = Fail
		check.Message = "no CUDA devices reported"
	} else {
		check.Status = Pass
		check.Message = firstLineContaining(output, "CUDA")
	}

	return
}

// checkService reports whether a systemd unit is active.
func checkService(unit string) (check Check) {
	var err error

	check.Name = unit
	if _, err = run("systemctl", "is-active", "--quiet", unit); err != nil {
		check.Status = Fail
		check.Message = "inactive or unavailable"
	} else {
		check.Status = Pass
		check.Message = "active"
	}

	return
}

// run executes a bounded diagnostic command and combines its output.
func run(command string, args ...string) (output string, err error) {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		data   []byte
	)

	ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if data, err = exec.CommandContext(ctx, command, args...).CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("%s timed out", command)
		} else {
			err = fmt.Errorf("%s: %w: %s", command, err, strings.TrimSpace(string(data)))
		}
		return
	}

	output = strings.TrimSpace(string(data))
	return
}

// firstLine returns a concise first line from command output.
func firstLine(value string) (line string) {
	var scanner *bufio.Scanner = bufio.NewScanner(bytes.NewBufferString(value))
	if scanner.Scan() {
		line = scanner.Text()
	}

	return
}

// firstLineContaining returns the first output line containing fragment.
func firstLineContaining(value, fragment string) (line string) {
	for _, candidate := range strings.Split(value, "\n") {
		if strings.Contains(candidate, fragment) {
			line = strings.TrimSpace(candidate)
			break
		}
	}

	return
}

// ConfigFileCheck exposes configuration validation as a doctor-style check.
func ConfigFileCheck(path string) (check Check) {
	var err error

	check.Name = "Configuration"
	if _, err = config.Load(filepath.Clean(path)); err != nil {
		check.Status = Fail
		check.Message = err.Error()
	} else {
		check.Status = Pass
		check.Message = filepath.Clean(path)
	}

	return
}
