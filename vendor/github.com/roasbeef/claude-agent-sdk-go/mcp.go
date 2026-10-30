package claudeagent

import (
	"context"
	"encoding/json"
	"fmt"
)

// McpServer represents an in-process MCP server.
//
// MCP servers provide tools that Claude can invoke. This implementation runs
// in-process, routing tool calls through the SDK control channel rather than
// spawning a separate subprocess.
//
// Use CreateMcpServer to create a new server and AddTool to register tools.
type McpServer struct {
	name         string
	version      string
	instructions string
	tools        map[string]*toolEntry
}

// toolEntry stores tool metadata and handler.
type toolEntry struct {
	def     ToolDef
	handler func(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

// ToolDef defines an MCP tool without the handler.
//
// The InputSchema field is optional - if nil, it will be auto-generated
// from the handler's Args type using reflection.
type ToolDef struct {
	Name        string      // Tool name (required).
	Description string      // Tool description (required).
	InputSchema interface{} // JSON Schema for input validation (optional).
}

// ToolResult is the result of a tool invocation.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent represents content in a tool result.
type ToolContent struct {
	Type     string `json:"type"`               // "text" or "resource".
	Text     string `json:"text,omitempty"`     // Text content.
	Resource string `json:"resource,omitempty"` // Resource content.
}

// ToolRegistrar is a function that registers a tool with a server.
//
// This allows passing tools to McpServerOptions. Use Tool() or ToolWithResponse()
// to create registrars.
type ToolRegistrar func(*McpServer)

// McpServerOptions configures an in-process MCP server.
type McpServerOptions struct {
	Name    string          // Server name (required).
	Version string          // Server version (default: "1.0.0").
	Tools   []ToolRegistrar // Tools to register (optional).

	// Instructions is an MCP "instructions" block exposed by this server.
	// When proxying a real MCP server through the SDK transport, pass the
	// underlying server's getInstructions() here so it isn't dropped on
	// the way through. Empty string omits the field entirely from the
	// MCP initialize response.
	Instructions string
}

// CreateMcpServer creates a new in-process MCP server.
//
// Example:
//
//	server := claudeagent.CreateMcpServer(claudeagent.McpServerOptions{
//	    Name:    "calculator",
//	    Version: "1.0.0",
//	    Tools: []claudeagent.ToolRegistrar{
//	        claudeagent.Tool("add", "Add two numbers", addHandler),
//	        claudeagent.Tool("multiply", "Multiply two numbers", multiplyHandler),
//	    },
//	})
func CreateMcpServer(opts McpServerOptions) *McpServer {
	version := opts.Version
	if version == "" {
		version = "1.0.0"
	}

	server := &McpServer{
		name:         opts.Name,
		version:      version,
		instructions: opts.Instructions,
		tools:        make(map[string]*toolEntry),
	}

	// Register any tools from options.
	for _, registrar := range opts.Tools {
		registrar(server)
	}

	return server
}

// Tool creates a ToolRegistrar for use with McpServerOptions.
//
// The generic Args type specifies the expected input type. Arguments are
// automatically unmarshaled from JSON to Args before the handler is invoked.
//
// Example:
//
//	type AddArgs struct {
//	    A int `json:"a"`
//	    B int `json:"b"`
//	}
//
//	server := claudeagent.CreateMcpServer(claudeagent.McpServerOptions{
//	    Name: "calculator",
//	    Tools: []claudeagent.ToolRegistrar{
//	        claudeagent.Tool("add", "Add two numbers",
//	            func(ctx context.Context, args AddArgs) (claudeagent.ToolResult, error) {
//	                return claudeagent.TextResult(fmt.Sprintf("%d", args.A+args.B)), nil
//	            },
//	        ),
//	    },
//	})
func Tool[Args any](
	name, description string,
	handler func(ctx context.Context, args Args) (ToolResult, error),
) ToolRegistrar {
	return func(s *McpServer) {
		s.addTool(ToolDef{Name: name, Description: description}, func(ctx context.Context, rawArgs json.RawMessage) (ToolResult, error) {
			var args Args
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			return handler(ctx, args)
		})
	}
}

// ToolWithResponse creates a ToolRegistrar with typed args and response.
//
// The generic Response type is automatically marshaled to JSON text content.
// This is useful when you want strongly-typed responses.
//
// Example:
//
//	type AddArgs struct {
//	    A int `json:"a"`
//	    B int `json:"b"`
//	}
//	type AddResult struct {
//	    Sum int `json:"sum"`
//	}
//
//	server := claudeagent.CreateMcpServer(claudeagent.McpServerOptions{
//	    Name: "calculator",
//	    Tools: []claudeagent.ToolRegistrar{
//	        claudeagent.ToolWithResponse("add", "Add two numbers",
//	            func(ctx context.Context, args AddArgs) (AddResult, error) {
//	                return AddResult{Sum: args.A + args.B}, nil
//	            },
//	        ),
//	    },
//	})
func ToolWithResponse[Args, Response any](
	name, description string,
	handler func(ctx context.Context, args Args) (Response, error),
) ToolRegistrar {
	return func(s *McpServer) {
		s.addTool(ToolDef{Name: name, Description: description}, func(ctx context.Context, rawArgs json.RawMessage) (ToolResult, error) {
			var args Args
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			resp, err := handler(ctx, args)
			if err != nil {
				return ErrorResult(err.Error()), nil
			}
			// Marshal response to JSON.
			data, err := json.Marshal(resp)
			if err != nil {
				return ErrorResult(fmt.Sprintf("failed to marshal response: %v", err)), nil
			}
			return TextResult(string(data)), nil
		})
	}
}

// ToolWithSchema creates a ToolRegistrar with explicit input schema.
//
// Use this when you need to specify a custom JSON schema for input validation.
func ToolWithSchema[Args any](
	name, description string,
	inputSchema interface{},
	handler func(ctx context.Context, args Args) (ToolResult, error),
) ToolRegistrar {
	return func(s *McpServer) {
		s.addTool(ToolDef{Name: name, Description: description, InputSchema: inputSchema}, func(ctx context.Context, rawArgs json.RawMessage) (ToolResult, error) {
			var args Args
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
			return handler(ctx, args)
		})
	}
}

// AddTool registers a type-safe tool handler with the server.
//
// This is a method version of the package-level AddTool function.
// Returns the server for method chaining.
//
// Example:
//
//	server := claudeagent.CreateMcpServer(claudeagent.McpServerOptions{Name: "calc"}).
//	    AddTool("add", "Add numbers", addHandler).
//	    AddTool("sub", "Subtract numbers", subHandler)
func (s *McpServer) AddTool(name, description string, handler interface{}) *McpServer {
	// We can't use generics on methods with different type parameters,
	// so we use reflection or type assertion here.
	// For now, this accepts a func that matches the signature.
	// Users should use the package-level Tool() for full type safety.
	switch h := handler.(type) {
	case func(context.Context, json.RawMessage) (ToolResult, error):
		s.addTool(ToolDef{Name: name, Description: description}, h)
	default:
		// For other handler types, they should use package-level AddTool.
		panic(fmt.Sprintf("unsupported handler type: %T - use package-level AddTool[Args] for typed handlers", handler))
	}
	return s
}

// addTool is the internal method for registering tools.
func (s *McpServer) addTool(def ToolDef, handler func(ctx context.Context, args json.RawMessage) (ToolResult, error)) {
	s.tools[def.Name] = &toolEntry{
		def:     def,
		handler: handler,
	}
}

// AddTool registers a type-safe tool handler with the server (package-level function).
//
// The generic Args parameter specifies the expected input type. Arguments
// are automatically unmarshaled from JSON to the Args type before the
// handler is invoked.
//
// Example:
//
//	type AddArgs struct {
//	    A int `json:"a" jsonschema:"First number"`
//	    B int `json:"b" jsonschema:"Second number"`
//	}
//
//	claudeagent.AddTool(server, claudeagent.ToolDef{
//	    Name:        "add",
//	    Description: "Add two numbers",
//	}, func(ctx context.Context, args AddArgs) (claudeagent.ToolResult, error) {
//	    return claudeagent.TextResult(fmt.Sprintf("%d", args.A+args.B)), nil
//	})
func AddTool[Args any](
	server *McpServer,
	def ToolDef,
	handler func(ctx context.Context, args Args) (ToolResult, error),
) {
	server.addTool(def, func(ctx context.Context, rawArgs json.RawMessage) (ToolResult, error) {
		var args Args
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		return handler(ctx, args)
	})
}

// AddToolWithResponse registers a tool with typed args and response.
//
// The generic Response type is automatically marshaled to JSON text content.
//
// Example:
//
//	type AddArgs struct {
//	    A int `json:"a"`
//	    B int `json:"b"`
//	}
//	type AddResult struct {
//	    Sum int `json:"sum"`
//	}
//
//	claudeagent.AddToolWithResponse(server, claudeagent.ToolDef{
//	    Name:        "add",
//	    Description: "Add two numbers",
//	}, func(ctx context.Context, args AddArgs) (AddResult, error) {
//	    return AddResult{Sum: args.A + args.B}, nil
//	})
func AddToolWithResponse[Args, Response any](
	server *McpServer,
	def ToolDef,
	handler func(ctx context.Context, args Args) (Response, error),
) {
	server.addTool(def, func(ctx context.Context, rawArgs json.RawMessage) (ToolResult, error) {
		var args Args
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		resp, err := handler(ctx, args)
		if err != nil {
			return ErrorResult(err.Error()), nil
		}
		// Marshal response to JSON.
		data, err := json.Marshal(resp)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to marshal response: %v", err)), nil
		}
		return TextResult(string(data)), nil
	})
}

// AddToolUntyped registers a tool handler that receives raw JSON arguments.
//
// Use this for tools that need dynamic argument handling or when you want
// to handle JSON parsing manually.
//
// Example:
//
//	claudeagent.AddToolUntyped(server, claudeagent.ToolDef{
//	    Name:        "dynamic",
//	    Description: "Handle dynamic args",
//	    InputSchema: customSchema,
//	}, func(ctx context.Context, args json.RawMessage) (claudeagent.ToolResult, error) {
//	    var data map[string]interface{}
//	    json.Unmarshal(args, &data)
//	    return claudeagent.TextResult("done"), nil
//	})
func AddToolUntyped(
	server *McpServer,
	def ToolDef,
	handler func(ctx context.Context, args json.RawMessage) (ToolResult, error),
) {
	server.addTool(def, handler)
}

// Name returns the server name.
func (s *McpServer) Name() string {
	return s.name
}

// Version returns the server version.
func (s *McpServer) Version() string {
	return s.version
}

// Instructions returns the MCP instructions block configured for this server.
// Empty string means no instructions block is exposed.
func (s *McpServer) Instructions() string {
	return s.instructions
}

// ToolNames returns the names of all registered tools.
func (s *McpServer) ToolNames() []string {
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	return names
}

// ToolDefs returns the definitions of all registered tools.
func (s *McpServer) ToolDefs() []ToolDef {
	defs := make([]ToolDef, 0, len(s.tools))
	for _, entry := range s.tools {
		defs = append(defs, entry.def)
	}
	return defs
}

// CallTool invokes a tool by name with the given arguments.
//
// Returns an error if the tool is not found. Tool execution errors are
// returned via ToolResult.IsError, not as Go errors.
func (s *McpServer) CallTool(
	ctx context.Context,
	name string,
	args json.RawMessage,
) (ToolResult, error) {
	entry, ok := s.tools[name]
	if !ok {
		return ToolResult{}, fmt.Errorf("tool not found: %s", name)
	}
	return entry.handler(ctx, args)
}

// TextResult creates a successful tool result with text content.
func TextResult(text string) ToolResult {
	return ToolResult{
		Content: []ToolContent{{Type: "text", Text: text}},
	}
}

// ErrorResult creates an error tool result with text content.
func ErrorResult(text string) ToolResult {
	return ToolResult{
		Content: []ToolContent{{Type: "text", Text: text}},
		IsError: true,
	}
}

// ResourceResult creates a successful tool result with resource content.
func ResourceResult(resource string) ToolResult {
	return ToolResult{
		Content: []ToolContent{{Type: "resource", Resource: resource}},
	}
}

// MultiContentResult creates a result with multiple content items.
func MultiContentResult(contents ...ToolContent) ToolResult {
	return ToolResult{
		Content: contents,
	}
}

// TextContent creates a text content item.
func TextContent(text string) ToolContent {
	return ToolContent{Type: "text", Text: text}
}

// ResourceContent creates a resource content item.
func ResourceContent(resource string) ToolContent {
	return ToolContent{Type: "resource", Resource: resource}
}
