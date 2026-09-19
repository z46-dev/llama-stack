package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/z46-dev/llama-stack/internal/config"
)

type (
	execRequest struct {
		Command string `json:"command"`
	}

	execResponse struct {
		Output   string `json:"output"`
		ExitCode int    `json:"exit_code"`
		TimedOut bool   `json:"timed_out"`
	}

	searchRequest struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}

	limitedBuffer struct {
		buffer    bytes.Buffer
		remaining int
		truncated bool
	}

	server struct {
		cfg        config.Config
		apiKey     string
		httpClient *http.Client
		run        func(context.Context, *http.Request, string) execResponse
	}
)

const openAPISpec string = `{"openapi":"3.1.0","info":{"title":"llama-stack tools","version":"1.0.0","description":"Authenticated tools backed by isolated llama-stack services."},"paths":{"/v1/exec":{"post":{"operationId":"exec_shell_command","summary":"Execute a shell command inside a persistent isolated Fedora toolbox container for this tool workspace. Returns actual stdout, stderr, exit status, and timeout state.","requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","properties":{"command":{"type":"string","description":"Shell command or script to execute inside the toolbox. Files, installed packages, background processes, and /workspace state persist for later calls in the same tool workspace."}},"required":["command"]}}}},"responses":{"200":{"description":"Command result","content":{"application/json":{"schema":{"type":"object","properties":{"output":{"type":"string"},"exit_code":{"type":"integer"},"timed_out":{"type":"boolean"}}}}}}}}},"/v1/search":{"post":{"operationId":"web_search","summary":"Search the public web through the local read-only SearXNG service.","requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","properties":{"query":{"type":"string"},"max_results":{"type":"integer","minimum":1,"maximum":10,"default":5}},"required":["query"]}}}},"responses":{"200":{"description":"SearXNG search results"}}}}}}`

// main starts the authenticated OpenAPI tool service.
func main() {
	var (
		cfg     config.Config
		apiKey  []byte
		handler http.Handler
		err     error
	)

	if cfg, err = config.Load(config.Path()); err == nil {
		apiKey, err = os.ReadFile(cfg.AgentTools.APIKeyFile)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "llama-stack-agent-tools: %v\n", err)
		os.Exit(1)
	}

	handler = newServer(cfg, strings.TrimSpace(string(apiKey))).routes()
	if err = http.ListenAndServe(cfg.AgentTools.Host+":"+strconv.Itoa(cfg.AgentTools.Port), handler); !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "llama-stack-agent-tools: %v\n", err)
		os.Exit(1)
	}
}

// newServer constructs the tool service with bounded network and process execution.
func newServer(cfg config.Config, apiKey string) (result *server) {
	result = &server{
		cfg:        cfg,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
	result.run = result.runToolbox
	return
}

// routes exposes the OpenAPI document and authenticated tool operations.
func (service *server) routes() (handler http.Handler) {
	var mux *http.ServeMux = http.NewServeMux()
	mux.HandleFunc("GET /healthz", service.health)
	mux.HandleFunc("GET /openapi.json", service.openapi)
	mux.Handle("POST /v1/exec", service.authorize(http.HandlerFunc(service.execute)))
	mux.Handle("POST /v1/search", service.authorize(http.HandlerFunc(service.search)))
	handler = mux
	return
}

func (service *server) authorize(next http.Handler) (handler http.Handler) {
	handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var supplied string = strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		if len(supplied) != len(service.apiKey) || subtle.ConstantTimeCompare([]byte(supplied), []byte(service.apiKey)) != 1 {
			writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(writer, request)
	})
	return
}

func (service *server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (service *server) openapi(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(writer, openAPISpec)
}

func (service *server) execute(writer http.ResponseWriter, request *http.Request) {
	var (
		input execRequest
		err   error
	)
	if err = decodeRequest(request, &input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(input.Command) == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "command must not be empty"})
		return
	}

	writeJSON(writer, http.StatusOK, service.run(request.Context(), request, input.Command))
}

// runToolbox executes one command inside the persistent container for this workspace.
func (service *server) runToolbox(parent context.Context, request *http.Request, command string) (result execResponse) {
	var (
		ctx           context.Context
		cancel        context.CancelFunc
		process       *exec.Cmd
		output        limitedBuffer
		exitError     *exec.ExitError
		key           string
		containerName string
		workspacePath string
		err           error
	)

	ctx, cancel = context.WithTimeout(parent, time.Duration(service.cfg.Toolbox.ExecutionTimeout)*time.Second)
	defer cancel()
	output.remaining = service.cfg.AgentTools.MaxOutput

	key = workspaceKey(request)
	if workspacePath, err = service.workspacePath(request); err != nil {
		result.ExitCode = -1
		result.Output = err.Error()
		return
	}
	containerName = toolboxContainerName(key)
	if err = service.ensureToolboxContainer(ctx, containerName, key, workspacePath); err != nil {
		result.ExitCode = -1
		result.Output = err.Error()
		return
	}

	process = exec.CommandContext(ctx, "podman", "--cgroup-manager=cgroupfs", "exec", "--workdir", "/workspace",
		containerName, "bash", "-lc", command)
	process.Stdout = &output
	process.Stderr = &output
	err = process.Run()
	result.Output = output.String()
	if output.truncated {
		result.Output += "\n[output truncated]\n"
	}
	if errors.As(err, &exitError) {
		result.ExitCode = exitError.ExitCode()
	} else if err != nil {
		result.ExitCode = -1
		if result.Output == "" {
			result.Output = err.Error()
		}
	}
	result.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	return
}

// ensureToolboxContainer creates or reuses the long-lived toolbox for a workspace.
func (service *server) ensureToolboxContainer(ctx context.Context, name, key, workspacePath string) (err error) {
	var (
		inspect *exec.Cmd
		run     *exec.Cmd
		state   []byte
	)

	inspect = exec.CommandContext(ctx, "podman", "--cgroup-manager=cgroupfs", "inspect", "--format", "{{.State.Running}}", name)
	if state, err = inspect.Output(); err == nil && strings.TrimSpace(string(state)) == "true" {
		return
	}

	run = exec.CommandContext(ctx, "podman", "--cgroup-manager=cgroupfs", "run", "-d", "--replace", "--pull=never",
		"--name", name,
		"--label", "llama-stack.toolbox=true",
		"--label", "llama-stack.workspace="+key,
		"--memory", service.cfg.Toolbox.Memory,
		"--cpus", strconv.FormatFloat(service.cfg.Toolbox.CPUs, 'f', -1, 64),
		"--pids-limit", strconv.Itoa(service.cfg.Toolbox.PIDs),
		"--volume", workspacePath+":/workspace:Z",
		"--workdir", "/workspace",
		service.cfg.Toolbox.Image, "sleep", "infinity")
	if output, runErr := run.CombinedOutput(); runErr != nil {
		err = fmt.Errorf("start toolbox container: %w: %s", runErr, strings.TrimSpace(string(output)))
	}
	return
}

// workspacePath returns a stable host directory for files created by tool calls.
func (service *server) workspacePath(request *http.Request) (path string, err error) {
	var (
		key  string
		root string
	)

	key = workspaceKey(request)
	root = filepath.Join(service.cfg.Paths.State, "agent-workspaces")
	path = filepath.Join(root, key)
	if err = os.MkdirAll(path, 0o700); err != nil {
		err = fmt.Errorf("create tool workspace: %w", err)
	}
	return
}

// workspaceKey scopes persistence when Open WebUI forwards user or session identity.
func workspaceKey(request *http.Request) (result string) {
	var candidate string

	candidate = request.Header.Get("X-Session-Id")
	if candidate == "" {
		candidate = request.Header.Get("X-User-Id")
	}
	if candidate == "" {
		candidate = "default"
	}
	for _, character := range candidate {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			result += string(character)
		} else {
			result += "_"
		}
	}
	if result == "" || result == "." || result == ".." {
		result = "default"
	}
	return
}

// toolboxContainerName gives Podman a short stable name for the workspace.
func toolboxContainerName(key string) (result string) {
	var (
		sum     [32]byte
		encoded string
	)

	sum = sha256.Sum256([]byte(key))
	encoded = hex.EncodeToString(sum[:])
	result = "llama-stack-toolbox-" + encoded[:24]
	return
}

func (service *server) search(writer http.ResponseWriter, request *http.Request) {
	var (
		input    searchRequest
		response *http.Response
		payload  map[string]any
		results  []any
		ok       bool
		err      error
	)
	if err = decodeRequest(request, &input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(input.Query) == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "query must not be empty"})
		return
	}
	if input.MaxResults == 0 {
		input.MaxResults = 5
	}
	if input.MaxResults < 1 || input.MaxResults > 10 {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "max_results must be between 1 and 10"})
		return
	}

	if response, err = service.httpClient.Get("http://127.0.0.1:"+strconv.Itoa(service.cfg.SearXNG.Port)+"/search?q="+url.QueryEscape(input.Query)+"&format=json"); err == nil {
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			err = fmt.Errorf("SearXNG returned %s", response.Status)
		} else {
			err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&payload)
		}
	}
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if results, ok = payload["results"].([]any); ok && len(results) > input.MaxResults {
		payload["results"] = results[:input.MaxResults]
	}
	writeJSON(writer, http.StatusOK, payload)
}

func decodeRequest(request *http.Request, target any) (err error) {
	var decoder *json.Decoder = json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		err = fmt.Errorf("invalid JSON request: %w", err)
	}
	return
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func (buffer *limitedBuffer) Write(data []byte) (written int, err error) {
	written = len(data)
	if buffer.remaining == 0 {
		buffer.truncated = true
		return
	}
	if len(data) > buffer.remaining {
		_, err = buffer.buffer.Write(data[:buffer.remaining])
		buffer.remaining = 0
		buffer.truncated = true
		return
	}
	_, err = buffer.buffer.Write(data)
	buffer.remaining -= len(data)
	return
}

func (buffer *limitedBuffer) String() (result string) {
	result = buffer.buffer.String()
	return
}
