// Package mcp is the VEDA MCP server: the tool table, strict argument
// validation and handlers that call the REST service through client.Client.
// Its tools, schemas, instructions and results match the former Python
// adapter (testdata/tools.json is its golden tool list).
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Mesteriis/veda-wiki/internal/mcp/client"
)

// MaxArticleBytes bounds the UTF-8 size of proposed article text.
const MaxArticleBytes = 1 << 20

// genericRefusal is returned for invalid input and any failure other than an
// HTTP refusal; it never carries server text.
const genericRefusal = "VEDA request refused: invalid input, unavailable source, authorization or revision conflict."

// handler performs one tool call with validated arguments and returns a value
// that marshals to the result object.
type handler func(ctx context.Context, c *client.Client, args arguments) (any, error)

// handlerTables holds every tool implementation, grouped by kind.
var handlerTables = []map[string]handler{readHandlers, prepareHandlers, writeHandlers, knowledgeHandlers}

func handlerFor(name string) (handler, bool) {
	for _, table := range handlerTables {
		if h, ok := table[name]; ok {
			return h, true
		}
	}
	return nil, false
}

// NewServer builds the MCP server for c. A read-only client gets only the
// tools marked ReadOnly, an editor client those plus the tools marked Editor;
// calls to any other name are refused.
func NewServer(c *client.Client) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "veda", Version: "0.1.0"},
		&sdk.ServerOptions{Instructions: Instructions})
	known := map[string]bool{}
	for _, spec := range Tools {
		if !spec.Allowed(c) {
			continue
		}
		schema, err := compileSchema(spec.Schema)
		if err != nil {
			panic(fmt.Sprintf("tool %s: %v", spec.Name, err))
		}
		h, ok := handlerFor(spec.Name)
		if !ok {
			panic("tool " + spec.Name + " has no handler")
		}
		server.AddTool(&sdk.Tool{Name: spec.Name, Description: spec.Description, InputSchema: spec.Schema},
			toolHandler(c, schema, h))
		known[spec.Name] = true
	}
	server.AddReceivingMiddleware(refuseUnknownTools(known))
	return server
}

// refuseUnknownTools answers a call to an unlisted tool with the generic
// refusal, as the Python adapter did, instead of a protocol error.
func refuseUnknownTools(known map[string]bool) sdk.Middleware {
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, request sdk.Request) (sdk.Result, error) {
			if call, ok := request.(*sdk.CallToolRequest); ok && method == "tools/call" &&
				(call.Params == nil || !known[call.Params.Name]) {
				return refused(genericRefusal), nil
			}
			return next(ctx, method, request)
		}
	}
}

func toolHandler(c *client.Client, schema *inputSchema, h handler) sdk.ToolHandler {
	return func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		args, err := schema.parse(request.Params.Arguments)
		if err != nil {
			var invalid *validationError
			if errors.As(err, &invalid) {
				return refused("Input validation error: " + invalid.reason), nil
			}
			return refused(genericRefusal), nil
		}
		result, err := h(ctx, c, args)
		if err != nil {
			var refusal client.Refused
			var invalid *validationError
			var own *toolRefusal
			switch {
			case errors.As(err, &refusal):
				return refused(refusal.Message()), nil
			case errors.As(err, &invalid):
				return refused("Input validation error: " + invalid.reason), nil
			case errors.As(err, &own):
				return refused(own.message), nil
			}
			return refused(genericRefusal), nil
		}
		return succeeded(result)
	}
}

// toolRefusal is a handler failure with a fixed message that carries no
// server text or argument value.
type toolRefusal struct{ message string }

func (e *toolRefusal) Error() string { return e.message }

func refused(message string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: message}}, IsError: true}
}

// succeeded returns the result as Python-formatted JSON text and as structured
// content.
func succeeded(result any) (*sdk.CallToolResult, error) {
	compact, err := marshal(result)
	if err != nil {
		return refused(genericRefusal), nil
	}
	text, err := pythonJSON(compact)
	if err != nil {
		return refused(genericRefusal), nil
	}
	if len(compact) == 0 || compact[0] != '{' {
		return refused(genericRefusal), nil // structured content must be an object
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: string(text)}},
		StructuredContent: json.RawMessage(compact),
	}, nil
}
