package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHealthEndpoint verifies the daemon's service-readiness contract.
func TestHealthEndpoint(t *testing.T) {
	var (
		request  *http.Request              = httptest.NewRequest(http.MethodGet, "/healthz", nil)
		response *httptest.ResponseRecorder = httptest.NewRecorder()
		result   health
		err      error
	)

	routes(nil).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("unexpected health: %#v", result)
	}
}
