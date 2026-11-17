package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrNotRunning is Dial's error when nothing serves the endpoint: Dalibre
// isn't running, or its MCP server is off.
var ErrNotRunning = errors.New("Dalibre’s MCP server isn’t running")

// Dial connects to the app's MCP server at endpoint with token, as the
// client name, for tools such as dalibre-plugin that drive the running
// app.
func Dial(ctx context.Context, endpoint, token, name, version string) (*sdk.ClientSession, error) {
	if !answers(endpoint) {
		return nil, ErrNotRunning
	}
	client := sdk.NewClient(&sdk.Implementation{Name: name, Version: version}, nil)
	return client.Connect(ctx, &sdk.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: bearer{token, http.DefaultTransport}},
	}, nil)
}

// CallTool calls the tool name with args and decodes its structured result
// into out (when not nil). A tool that fails returns its text as the
// error.
func CallTool(ctx context.Context, cs *sdk.ClientSession, name string, args, out any) error {
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err
	}
	if res.IsError {
		for _, c := range res.Content {
			if t, ok := c.(*sdk.TextContent); ok {
				return errors.New(t.Text)
			}
		}
		return errors.New(name + " failed")
	}
	if out == nil || res.StructuredContent == nil {
		return nil
	}
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
