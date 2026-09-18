package main

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/z46-dev/llama-stack/internal/resourceapi"
)

// TestResourceToolsAreDiscoverable verifies the MCP contract exposed to agents.
func TestResourceToolsAreDiscoverable(t *testing.T) {
	var (
		ctx             context.Context
		cancel          context.CancelFunc
		serverTransport *mcp.InMemoryTransport
		clientTransport *mcp.InMemoryTransport
		client          *mcp.Client
		session         *mcp.ClientSession
		result          *mcp.ListToolsResult
		err             error
		names           map[string]bool = make(map[string]bool)
	)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport = mcp.NewInMemoryTransports()
	go func() {
		_ = newResourceServer(resourceapi.Client{}).Run(ctx, serverTransport)
	}()
	client = mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	if session, err = client.Connect(ctx, clientTransport, nil); err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if result, err = session.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	for _, expected := range []string{"port_allocate", "port_list", "port_renew", "port_release"} {
		if !names[expected] {
			t.Fatalf("MCP tool %q is missing", expected)
		}
	}
}
