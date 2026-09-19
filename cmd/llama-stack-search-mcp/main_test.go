package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestSearchReturnsBoundedResults verifies query encoding and result limiting.
func TestSearchReturnsBoundedResults(t *testing.T) {
	var (
		server *httptest.Server
		output searchOutput
		err    error
	)

	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("q") != "fedora tools" || request.URL.Query().Get("format") != "json" {
			t.Errorf("unexpected query: %s", request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"results":[{"title":"one","url":"https://example.com/1"},{"title":"two","url":"https://example.com/2"}]}`)
	}))
	defer server.Close()

	if output, err = search(context.Background(), server.Client(), server.URL, searchInput{Query: "fedora tools", MaxResults: 1}); err != nil {
		t.Fatal(err)
	}
	if len(output.Results) != 1 || output.Results[0].Title != "one" {
		t.Fatalf("unexpected search output: %+v", output)
	}
}

// TestSearchToolIsDiscoverable verifies the MCP contract exposed to models.
func TestSearchToolIsDiscoverable(t *testing.T) {
	var (
		ctx             context.Context
		cancel          context.CancelFunc
		serverTransport *mcp.InMemoryTransport
		clientTransport *mcp.InMemoryTransport
		client          *mcp.Client
		session         *mcp.ClientSession
		result          *mcp.ListToolsResult
		err             error
	)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport = mcp.NewInMemoryTransports()
	go func() {
		_ = newSearchServer(http.DefaultClient, "http://127.0.0.1:8082").Run(ctx, serverTransport)
	}()
	client = mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	if session, err = client.Connect(ctx, clientTransport, nil); err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if result, err = session.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "web_search" {
		t.Fatalf("unexpected tools: %+v", result.Tools)
	}
}
