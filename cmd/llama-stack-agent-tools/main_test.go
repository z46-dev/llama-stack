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
	service.run = func(_ context.Context, command string) (result execResponse) {
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
		!strings.Contains(openAPISpec, `"operationId":"web_search"`) {
		t.Fatal("OpenAPI document does not publish both tools")
	}
}
