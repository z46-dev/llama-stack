package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version string = "development"

type (
	searchInput struct {
		Query      string `json:"query" jsonschema:"Search query to submit to the configured read-only SearXNG service."`
		MaxResults int    `json:"max_results,omitempty" jsonschema:"Maximum results to return, from 1 through 10; zero selects 5."`
	}

	searchResult struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content,omitempty"`
		Engine  string `json:"engine,omitempty"`
	}

	searchOutput struct {
		Results []searchResult `json:"results"`
	}

	searxngResponse struct {
		Results []searchResult `json:"results"`
	}
)

// main serves the read-only SearXNG search tool over MCP stdio.
func main() {
	var err error

	if err = run(); err != nil {
		fmt.Fprintf(os.Stderr, "llama-stack-search-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run() (err error) {
	var (
		baseURL string = strings.TrimSpace(os.Getenv("SEARXNG_URL"))
		server  *mcp.Server
	)

	if baseURL == "" {
		err = errors.New("SEARXNG_URL is required")
		return
	}
	if _, err = url.ParseRequestURI(baseURL); err != nil {
		err = fmt.Errorf("invalid SEARXNG_URL: %w", err)
		return
	}

	server = newSearchServer(&http.Client{Timeout: 20 * time.Second}, baseURL)
	err = server.Run(context.Background(), &mcp.StdioTransport{})
	return
}

// newSearchServer constructs the MCP contract around a fixed SearXNG endpoint.
func newSearchServer(client *http.Client, baseURL string) (server *mcp.Server) {
	server = mcp.NewServer(&mcp.Implementation{
		Name:        "llama-stack-search",
		Title:       "llama-stack Read-only Web Search",
		Description: "Searches the web through the administrator-configured SearXNG service without fetching arbitrary URLs.",
		Version:     version,
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "web_search",
		Description: "Search the public web through SearXNG and return titles, URLs, and result snippets. This tool does not modify remote or local state.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input searchInput) (result *mcp.CallToolResult, output searchOutput, handlerErr error) {
		output, handlerErr = search(ctx, client, baseURL, input)
		return
	})

	return
}

// search performs one bounded JSON search against the configured service.
func search(ctx context.Context, client *http.Client, baseURL string, input searchInput) (output searchOutput, err error) {
	var (
		endpoint *url.URL
		request  *http.Request
		response *http.Response
		payload  searxngResponse
		limit    int = input.MaxResults
	)

	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" {
		err = errors.New("query must not be empty")
		return
	}
	if limit == 0 {
		limit = 5
	}
	if limit < 1 || limit > 10 {
		err = errors.New("max_results must be between 1 and 10")
		return
	}
	if endpoint, err = url.Parse(baseURL); err != nil {
		return
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/search"
	endpoint.RawQuery = url.Values{
		"format": {"json"},
		"q":      {input.Query},
	}.Encode()

	if request, err = http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil); err != nil {
		return
	}
	request.Header.Set("Accept", "application/json")
	if response, err = client.Do(request); err != nil {
		return
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var body []byte
		body, _ = io.ReadAll(io.LimitReader(response.Body, 4096))
		err = fmt.Errorf("SearXNG returned %s: %s", response.Status, strings.TrimSpace(string(body)))
		return
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		err = fmt.Errorf("decode SearXNG response: %w", err)
		return
	}
	if len(payload.Results) > limit {
		payload.Results = payload.Results[:limit]
	}
	output.Results = payload.Results
	return
}
