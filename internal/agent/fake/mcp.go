package fake

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpClient is a lazily connected client of the app MCP server.
type mcpClient struct {
	url, token string
	cs         *mcp.ClientSession
}

func (c *mcpClient) call(ctx context.Context, call *MCPCall) (*mcp.CallToolResult, error) {
	if c.cs == nil {
		if c.url == "" {
			return nil, errors.New("fake: " + EnvMCPURL + " is not set")
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "remote-agent-fake", Version: "1"}, nil)
		transport := &mcp.StreamableClientTransport{
			Endpoint:             c.url,
			HTTPClient:           &http.Client{Transport: bearer{token: c.token, base: http.DefaultTransport}},
			MaxRetries:           -1,
			DisableStandaloneSSE: true,
		}
		cs, err := client.Connect(ctx, transport, nil)
		if err != nil {
			return nil, fmt.Errorf("fake: connect to app MCP server: %w", err)
		}
		c.cs = cs
	}
	params := &mcp.CallToolParams{Name: call.Tool}
	if len(call.Args) > 0 {
		params.Arguments = call.Args
	}
	res, err := c.cs.CallTool(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("fake: call %s: %w", call.Tool, err)
	}
	return res, nil
}

func (c *mcpClient) close() {
	if c.cs != nil {
		c.cs.Close()
	}
}

// resultText flattens a tool result's text content.
func resultText(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// bearer adds the run's token to every request.
type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.base.RoundTrip(r)
}
