package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/z46-dev/llama-stack/internal/resource"
	"github.com/z46-dev/llama-stack/internal/resourceapi"
)

var version string = "development"

type (
	allocateInput struct {
		TargetPorts []int `json:"target_ports" jsonschema:"Container-side TCP ports to expose."`
		TTLSeconds  int   `json:"ttl_seconds,omitempty" jsonschema:"Lease lifetime in seconds; zero selects the configured default."`
		WaitSeconds int   `json:"wait_seconds,omitempty" jsonschema:"Maximum seconds to wait for capacity; zero fails immediately."`
	}

	leaseIDInput struct {
		LeaseID string `json:"lease_id" jsonschema:"Lease identifier returned by port_allocate or port_list."`
	}

	renewInput struct {
		LeaseID    string `json:"lease_id" jsonschema:"Lease identifier to renew."`
		TTLSeconds int    `json:"ttl_seconds" jsonschema:"New lifetime from the current time, in seconds."`
	}

	leaseOutput struct {
		Lease resource.Lease `json:"lease"`
	}

	leasesOutput struct {
		Leases []resource.Lease `json:"leases"`
	}

	releaseOutput struct {
		Released bool `json:"released"`
	}
)

// main serves capability-scoped resource tools over MCP stdio.
func main() {
	var err error

	if err = run(); err != nil {
		fmt.Fprintf(os.Stderr, "llama-stack-resource-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run() (err error) {
	var (
		baseURL string = os.Getenv("LLAMA_STACK_URL")
		token   string = os.Getenv("LLAMA_STACK_CAPABILITY_TOKEN")
		client  resourceapi.Client
		server  *mcp.Server
	)

	if token == "" {
		err = errors.New("LLAMA_STACK_CAPABILITY_TOKEN is required")
		return
	}
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8090"
	}
	client = resourceapi.Client{BaseURL: baseURL, CapabilityToken: token}
	server = newResourceServer(client)

	err = server.Run(context.Background(), &mcp.StdioTransport{})
	return
}

func newResourceServer(client resourceapi.Client) (server *mcp.Server) {
	server = mcp.NewServer(&mcp.Implementation{
		Name:        "llama-stack-resource",
		Title:       "llama-stack Resource Broker",
		Description: "Leases externally reachable TCP ports for the current agent run.",
		Version:     version,
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "port_allocate",
		Description: "Lease external TCP ports and forward them to the requested ports in this run's workspace. Release leases when no longer needed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input allocateInput) (result *mcp.CallToolResult, output leaseOutput, handlerErr error) {
		output.Lease, handlerErr = client.Allocate(ctx, resourceapi.AllocateRequest{
			TargetPorts: input.TargetPorts,
			TTLSeconds:  input.TTLSeconds,
			WaitSeconds: input.WaitSeconds,
		})
		return
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "port_list",
		Description: "List active TCP port leases belonging to this agent run.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (result *mcp.CallToolResult, output leasesOutput, handlerErr error) {
		output.Leases, handlerErr = client.List(ctx)
		return
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "port_renew",
		Description: "Extend an active TCP port lease owned by this agent run.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input renewInput) (result *mcp.CallToolResult, output leaseOutput, handlerErr error) {
		output.Lease, handlerErr = client.Renew(ctx, input.LeaseID, input.TTLSeconds)
		return
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "port_release",
		Description: "Release an active TCP port lease immediately so another run can use it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input leaseIDInput) (result *mcp.CallToolResult, output releaseOutput, handlerErr error) {
		if handlerErr = client.Release(ctx, input.LeaseID); handlerErr == nil {
			output.Released = true
		}
		return
	})

	return
}
