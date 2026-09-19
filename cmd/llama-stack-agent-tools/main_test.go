package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z46-dev/llama-stack/internal/config"
)

// TestToolRoutesRequireAuthenticationAndReturnRealRunnerOutput covers the public tool contract.
func TestToolRoutesRequireAuthenticationAndReturnRealRunnerOutput(t *testing.T) {
	var (
		service  *server = newServer(config.Config{}, "test-key")
		request  *http.Request
		response *httptest.ResponseRecorder
		decoded  execResponse
		err      error
	)
	service.run = func(_ context.Context, _ *http.Request, command string) (result execResponse) {
		result = execResponse{Output: "ran: " + command, ExitCode: 0}
		return
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(`{"command":"uname -m"}`))
	response = httptest.NewRecorder()
	service.routes().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request returned %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(`{"command":"uname -m"}`))
	request.Header.Set("Authorization", "Bearer test-key")
	response = httptest.NewRecorder()
	service.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated request returned %d: %s", response.Code, response.Body.String())
	}
	if err = json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Output != "ran: uname -m" || decoded.ExitCode != 0 {
		t.Fatalf("unexpected execution response: %+v", decoded)
	}
}

// TestOpenAPISpecPublishesBothTools ensures Open WebUI can discover the gateway.
func TestOpenAPISpecPublishesBothTools(t *testing.T) {
	var (
		document map[string]any
		err      error
	)
	if err = json.Unmarshal([]byte(openAPISpec), &document); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(openAPISpec, `"operationId":"exec_shell_command"`) ||
		!strings.Contains(openAPISpec, `"operationId":"web_search"`) ||
		!strings.Contains(openAPISpec, `persistent isolated Fedora toolbox container`) {
		t.Fatal("OpenAPI document does not publish both tools")
	}
}

// TestWorkspaceKeySanitizesForwardedIdentity keeps request headers out of paths.
func TestWorkspaceKeySanitizesForwardedIdentity(t *testing.T) {
	var request *http.Request

	request = httptest.NewRequest(http.MethodPost, "/v1/exec", strings.NewReader(`{}`))
	if workspaceKey(request) != "default" {
		t.Fatal("missing identity should use the default workspace")
	}
	if toolboxContainerName(workspaceKey(request)) != "llama-stack-toolbox-37a8eec1ce19687d132fe290" {
		t.Fatalf("unexpected default container name: %s", toolboxContainerName(workspaceKey(request)))
	}

	request.Header.Set("X-User-Id", "../user one")
	if workspaceKey(request) != ".._user_one" {
		t.Fatalf("unexpected sanitized user workspace: %s", workspaceKey(request))
	}

	request.Header.Set("X-Session-Id", "chat:abc/123")
	if workspaceKey(request) != "chat_abc_123" {
		t.Fatalf("unexpected sanitized session workspace: %s", workspaceKey(request))
	}
}
