package resourceapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/z46-dev/llama-stack/internal/resource"
)

type (
	API struct {
		manager    *resource.Manager
		adminToken string
		maxWait    time.Duration
		schedule   ExpiryScheduler
	}

	ExpiryScheduler func(time.Time) error

	IssueCapabilityRequest struct {
		UserID      string `json:"user_id"`
		RunID       string `json:"run_id"`
		WorkspaceID string `json:"workspace_id"`
		TargetHost  string `json:"target_host"`
		TTLSeconds  int    `json:"ttl_seconds"`
	}

	IssueCapabilityResponse struct {
		Token      string              `json:"token"`
		Capability resource.Capability `json:"capability"`
	}

	AllocateRequest struct {
		TargetPorts []int `json:"target_ports"`
		TTLSeconds  int   `json:"ttl_seconds,omitempty"`
		WaitSeconds int   `json:"wait_seconds,omitempty"`
	}

	RenewRequest struct {
		TTLSeconds int `json:"ttl_seconds"`
	}

	LeaseResponse struct {
		Lease resource.Lease `json:"lease"`
	}

	LeasesResponse struct {
		Leases []resource.Lease `json:"leases"`
	}

	ErrorResponse struct {
		Error             string `json:"error"`
		Message           string `json:"message"`
		RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	}

	Client struct {
		BaseURL         string
		AdminToken      string
		CapabilityToken string
		HTTPClient      *http.Client
	}
)

// NewHandler builds the authenticated resource-allocation HTTP surface.
func NewHandler(manager *resource.Manager, adminToken string, maxWait time.Duration, schedule ExpiryScheduler) (handler http.Handler) {
	var api *API = &API{manager: manager, adminToken: strings.TrimSpace(adminToken), maxWait: maxWait, schedule: schedule}
	var mux *http.ServeMux = http.NewServeMux()

	mux.HandleFunc("POST /v1/admin/capabilities", api.issueCapability)
	mux.HandleFunc("DELETE /v1/admin/capabilities/{capability}", api.revokeCapability)
	mux.HandleFunc("GET /v1/admin/leases", api.listAll)
	mux.HandleFunc("DELETE /v1/admin/leases/{lease}", api.revoke)
	mux.HandleFunc("POST /v1/ports/leases", api.allocate)
	mux.HandleFunc("GET /v1/ports/leases", api.list)
	mux.HandleFunc("POST /v1/ports/leases/{lease}/renew", api.renew)
	mux.HandleFunc("DELETE /v1/ports/leases/{lease}", api.release)
	handler = mux
	return
}

func (api *API) issueCapability(writer http.ResponseWriter, request *http.Request) {
	var (
		input    IssueCapabilityRequest
		response IssueCapabilityResponse
		err      error
	)

	if !api.isAdmin(request) {
		writeError(writer, http.StatusForbidden, "forbidden", "valid administrator token required", 0)
		return
	}
	if err = decode(request.Body, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error(), 0)
		return
	}
	if response.Token, response.Capability, err = api.manager.IssueCapability(
		input.UserID,
		input.RunID,
		input.WorkspaceID,
		input.TargetHost,
		time.Duration(input.TTLSeconds)*time.Second,
	); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_capability", err.Error(), 0)
		return
	}
	writeJSON(writer, http.StatusCreated, response)
}

func (api *API) listAll(writer http.ResponseWriter, request *http.Request) {
	var (
		leases []resource.Lease
		err    error
	)

	if !api.isAdmin(request) {
		writeError(writer, http.StatusForbidden, "forbidden", "valid administrator token required", 0)
		return
	}
	if leases, err = api.manager.ListAll(); err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", err.Error(), 0)
		return
	}
	writeJSON(writer, http.StatusOK, LeasesResponse{Leases: leases})
}

func (api *API) revokeCapability(writer http.ResponseWriter, request *http.Request) {
	var err error

	if !api.isAdmin(request) {
		writeError(writer, http.StatusForbidden, "forbidden", "valid administrator token required", 0)
		return
	}
	if err = api.manager.RevokeCapability(request.PathValue("capability")); err != nil {
		writeResourceError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (api *API) revoke(writer http.ResponseWriter, request *http.Request) {
	var err error

	if !api.isAdmin(request) {
		writeError(writer, http.StatusForbidden, "forbidden", "valid administrator token required", 0)
		return
	}
	if err = api.manager.Revoke(request.PathValue("lease")); err != nil {
		writeResourceError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (api *API) allocate(writer http.ResponseWriter, request *http.Request) {
	var (
		capability resource.Capability
		input      AllocateRequest
		lease      resource.Lease
		err        error
		wait       time.Duration
	)

	if capability, err = api.authenticate(request); err != nil {
		writeResourceError(writer, err)
		return
	}
	if err = decode(request.Body, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error(), 0)
		return
	}
	wait = time.Duration(input.WaitSeconds) * time.Second
	if wait < 0 || wait > api.maxWait {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_wait", fmt.Sprintf("wait must be between zero and %s", api.maxWait), 0)
		return
	}
	if lease, err = api.manager.Allocate(request.Context(), capability, input.TargetPorts, time.Duration(input.TTLSeconds)*time.Second, wait); err != nil {
		writeResourceError(writer, err)
		return
	}
	if api.schedule != nil {
		if err = api.schedule(lease.ExpiresAt); err != nil {
			_ = api.manager.Release(capability, lease.ID)
			writeError(writer, http.StatusInternalServerError, "scheduler_error", err.Error(), 0)
			return
		}
	}
	writeJSON(writer, http.StatusCreated, LeaseResponse{Lease: lease})
}

func (api *API) list(writer http.ResponseWriter, request *http.Request) {
	var (
		capability resource.Capability
		leases     []resource.Lease
		err        error
	)

	if capability, err = api.authenticate(request); err != nil {
		writeResourceError(writer, err)
		return
	}
	if leases, err = api.manager.List(capability); err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, LeasesResponse{Leases: leases})
}

func (api *API) renew(writer http.ResponseWriter, request *http.Request) {
	var (
		capability resource.Capability
		input      RenewRequest
		lease      resource.Lease
		err        error
	)

	if capability, err = api.authenticate(request); err != nil {
		writeResourceError(writer, err)
		return
	}
	if err = decode(request.Body, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error(), 0)
		return
	}
	if lease, err = api.manager.Renew(capability, request.PathValue("lease"), time.Duration(input.TTLSeconds)*time.Second); err != nil {
		writeResourceError(writer, err)
		return
	}
	if api.schedule != nil {
		if err = api.schedule(lease.ExpiresAt); err != nil {
			writeError(writer, http.StatusInternalServerError, "scheduler_error", err.Error(), 0)
			return
		}
	}
	writeJSON(writer, http.StatusOK, LeaseResponse{Lease: lease})
}

func (api *API) release(writer http.ResponseWriter, request *http.Request) {
	var (
		capability resource.Capability
		err        error
	)

	if capability, err = api.authenticate(request); err != nil {
		writeResourceError(writer, err)
		return
	}
	if err = api.manager.Release(capability, request.PathValue("lease")); err != nil {
		writeResourceError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (api *API) authenticate(request *http.Request) (capability resource.Capability, err error) {
	var token string

	if token, err = bearerToken(request); err == nil {
		capability, err = api.manager.Authenticate(token)
	}
	return
}

func (api *API) isAdmin(request *http.Request) (authorized bool) {
	var (
		token string
		err   error
	)

	if token, err = bearerToken(request); err == nil && len(token) == len(api.adminToken) {
		authorized = subtle.ConstantTimeCompare([]byte(token), []byte(api.adminToken)) == 1
	}
	return
}

func bearerToken(request *http.Request) (token string, err error) {
	var authorization string = request.Header.Get("Authorization")

	if !strings.HasPrefix(authorization, "Bearer ") {
		err = resource.ErrForbidden
	} else if token = strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")); token == "" {
		err = resource.ErrForbidden
	}
	return
}

func decode(reader io.Reader, destination any) (err error) {
	var decoder *json.Decoder = json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(destination); err == nil {
		var (
			trailing    any
			trailingErr error
		)
		if trailingErr = decoder.Decode(&trailing); !errors.Is(trailingErr, io.EOF) {
			err = errors.New("request body must contain exactly one JSON value")
		}
	}
	return
}

func writeResourceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, resource.ErrExhausted):
		writer.Header().Set("Retry-After", "30")
		writeError(writer, http.StatusServiceUnavailable, "resource_exhausted", err.Error(), 30)
	case errors.Is(err, resource.ErrForbidden):
		writeError(writer, http.StatusForbidden, "forbidden", err.Error(), 0)
	case errors.Is(err, resource.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", err.Error(), 0)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeError(writer, http.StatusRequestTimeout, "request_timeout", err.Error(), 0)
	default:
		writeError(writer, http.StatusUnprocessableEntity, "allocation_failed", err.Error(), 0)
	}
}

func writeError(writer http.ResponseWriter, status int, code, message string, retryAfter int) {
	writeJSON(writer, status, ErrorResponse{Error: code, Message: message, RetryAfterSeconds: retryAfter})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

// IssueCapability asks the local daemon to mint one scoped run credential.
func (client Client) IssueCapability(ctx context.Context, input IssueCapabilityRequest) (response IssueCapabilityResponse, err error) {
	err = client.request(ctx, http.MethodPost, "/v1/admin/capabilities", client.AdminToken, input, &response)
	return
}

// RevokeCapability invalidates a run credential and releases its leases.
func (client Client) RevokeCapability(ctx context.Context, capabilityID string) (err error) {
	err = client.request(ctx, http.MethodDelete, "/v1/admin/capabilities/"+capabilityID, client.AdminToken, nil, nil)
	return
}

// Allocate requests exclusive public ports for container-side target ports.
func (client Client) Allocate(ctx context.Context, input AllocateRequest) (lease resource.Lease, err error) {
	var response LeaseResponse
	err = client.request(ctx, http.MethodPost, "/v1/ports/leases", client.CapabilityToken, input, &response)
	lease = response.Lease
	return
}

// List returns leases owned by the configured capability.
func (client Client) List(ctx context.Context) (leases []resource.Lease, err error) {
	var response LeasesResponse
	err = client.request(ctx, http.MethodGet, "/v1/ports/leases", client.CapabilityToken, nil, &response)
	leases = response.Leases
	return
}

// ListAll returns active leases using administrator authentication.
func (client Client) ListAll(ctx context.Context) (leases []resource.Lease, err error) {
	var response LeasesResponse
	err = client.request(ctx, http.MethodGet, "/v1/admin/leases", client.AdminToken, nil, &response)
	leases = response.Leases
	return
}

// Renew extends an owned lease.
func (client Client) Renew(ctx context.Context, leaseID string, ttlSeconds int) (lease resource.Lease, err error) {
	var response LeaseResponse
	err = client.request(ctx, http.MethodPost, "/v1/ports/leases/"+leaseID+"/renew", client.CapabilityToken, RenewRequest{TTLSeconds: ttlSeconds}, &response)
	lease = response.Lease
	return
}

// Release returns an owned lease to the pool.
func (client Client) Release(ctx context.Context, leaseID string) (err error) {
	err = client.request(ctx, http.MethodDelete, "/v1/ports/leases/"+leaseID, client.CapabilityToken, nil, nil)
	return
}

// Revoke administratively returns any lease to the pool.
func (client Client) Revoke(ctx context.Context, leaseID string) (err error) {
	err = client.request(ctx, http.MethodDelete, "/v1/admin/leases/"+leaseID, client.AdminToken, nil, nil)
	return
}

func (client Client) request(ctx context.Context, method, path, token string, input, output any) (err error) {
	var (
		body       io.Reader
		payload    []byte
		request    *http.Request
		response   *http.Response
		httpClient *http.Client = client.HTTPClient
	)

	if input != nil {
		if payload, err = json.Marshal(input); err != nil {
			return
		}
		body = bytes.NewReader(payload)
	}
	if request, err = http.NewRequestWithContext(ctx, method, strings.TrimRight(client.BaseURL, "/")+path, body); err != nil {
		return
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if response, err = httpClient.Do(request); err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var (
			responseError ErrorResponse
			decodeErr     error
		)
		if decodeErr = json.NewDecoder(response.Body).Decode(&responseError); decodeErr != nil {
			err = fmt.Errorf("resource API returned HTTP %d", response.StatusCode)
		} else {
			err = fmt.Errorf("%s: %s", responseError.Error, responseError.Message)
		}
		return
	}
	if output != nil && response.StatusCode != http.StatusNoContent {
		err = json.NewDecoder(response.Body).Decode(output)
	}
	return
}

// Seconds parses a bounded duration expressed in seconds for CLI callers.
func Seconds(value string) (seconds int, err error) {
	if seconds, err = strconv.Atoi(value); err == nil && seconds < 0 {
		err = errors.New("seconds must not be negative")
	}
	return
}
