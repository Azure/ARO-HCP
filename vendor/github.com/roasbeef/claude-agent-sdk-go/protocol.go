package claudeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// Protocol implements the control protocol for bidirectional communication
// with the Claude Code CLI.
//
// The protocol handles:
// - Initialization with hooks and permissions
// - Permission requests from the CLI
// - Hook callback invocation
// - Control request/response correlation
type Protocol struct {
	transport     Transport
	options       *Options
	requestID     atomic.Uint64
	pendingReqs   sync.Map                // requestID -> chan ControlResponse
	hookCallbacks map[string]HookCallback // hookID -> callback
	sdkMcpServers map[string]*McpServer   // serverName -> server (in-process MCP)
	initResponse  atomic.Pointer[SDKControlInitializeResponse]
	initialized   atomic.Bool
}

// NewProtocol creates a new protocol handler.
func NewProtocol(transport Transport, options *Options) *Protocol {
	// Copy SDK MCP servers from options.
	sdkMcpServers := make(map[string]*McpServer)
	for name, server := range options.SDKMcpServers {
		sdkMcpServers[name] = server
	}

	return &Protocol{
		transport:     transport,
		options:       options,
		hookCallbacks: make(map[string]HookCallback),
		sdkMcpServers: sdkMcpServers,
	}
}

// Initialize sends the initialization control message to the CLI.
//
// This registers hooks and configures the SDK integration. It must be called
// before any user messages are sent.
func (p *Protocol) Initialize(ctx context.Context) error {
	if p.initialized.Load() {
		return nil // Already initialized
	}

	// Build hook configuration in TypeScript SDK format.
	var hooks map[string][]SDKHookCallbackMatcher
	if len(p.options.Hooks) > 0 {
		hooks = make(map[string][]SDKHookCallbackMatcher)
		hookID := 0

		for hookType, configs := range p.options.Hooks {
			hookMatchers := []SDKHookCallbackMatcher{}
			for _, cfg := range configs {
				id := fmt.Sprintf("hook_%d", hookID)
				hookID++

				// Register callback.
				p.hookCallbacks[id] = cfg.Callback

				hookMatchers = append(hookMatchers, SDKHookCallbackMatcher{
					Matcher:         cfg.Matcher,
					HookCallbackIDs: []string{id},
					Timeout:         cfg.Timeout,
				})
			}
			hooks[string(hookType)] = hookMatchers
		}
	}

	// Build list of SDK MCP server names.
	var sdkMcpServers []string
	if len(p.sdkMcpServers) > 0 {
		sdkMcpServers = make([]string, 0, len(p.sdkMcpServers))
		for name := range p.sdkMcpServers {
			sdkMcpServers = append(sdkMcpServers, name)
		}
	}

	var excludeDynamicSections *bool
	if p.options.ExcludeDynamicSystemPromptSections {
		trueVal := true
		excludeDynamicSections = &trueVal
	}

	var agents map[string]interface{}
	if len(p.options.Agents) > 0 {
		agents = make(map[string]interface{}, len(p.options.Agents))
		for k, v := range p.options.Agents {
			agents[k] = v
		}
	}

	// Build initialization request in TypeScript SDK format.
	requestID := p.nextRequestID()
	req := SDKControlRequest{
		Type:      "control_request",
		RequestID: requestID,
		Request: SDKControlRequestBody{
			Subtype:                "initialize",
			Hooks:                  hooks,
			SDKMCPServers:          sdkMcpServers,
			MCPServers:             p.options.MCPServers,
			SystemPrompt:           p.options.SystemPrompt,
			PlanModeInstructions:   p.options.PlanModeInstructions,
			ExcludeDynamicSections: excludeDynamicSections,
			Agents:                 agents,
			Title:                  p.options.Title,
			Skills:                 p.options.Skills,
			PromptSuggestions:      p.options.PromptSuggestions,
			AgentProgressSummaries: p.options.AgentProgressSummaries,
			ForwardSubagentText:    p.options.ForwardSubagentText,
			ToolAliases:            p.options.ToolAliases,
		},
	}

	// Send request.
	if err := p.transport.Write(ctx, req); err != nil {
		return fmt.Errorf("failed to send initialize request: %w", err)
	}

	// Wait for response.
	resp, err := p.waitForSDKResponse(ctx, requestID)
	if err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}

	if resp.Response.Subtype == "error" {
		return fmt.Errorf("initialization error: %s", resp.Response.Error)
	}

	bytes, err := json.Marshal(resp.Response.Response)
	if err != nil {
		return fmt.Errorf("failed to parse initialization response: %w", err)
	}
	var initResp SDKControlInitializeResponse
	if err := json.Unmarshal(bytes, &initResp); err != nil {
		return fmt.Errorf("failed to parse initialization response: %w", err)
	}
	p.initResponse.Store(&initResp)
	p.initialized.Store(true)
	return nil
}

func (p *Protocol) initResult() *SDKControlInitializeResponse {
	return p.initResponse.Load()
}

// SendMessage sends a user message to the CLI.
// Note: Initialize() should be called before SendMessage().
func (p *Protocol) SendMessage(ctx context.Context, msg UserMessage) error {
	return p.transport.Write(ctx, msg)
}

// HandleControlMessage processes a control message from the CLI.
//
// This handles permission requests, hook callbacks, and other control
// protocol interactions. Returns a response to send back to the CLI.
func (p *Protocol) HandleControlMessage(ctx context.Context, msg Message) error {
	switch m := msg.(type) {
	case SDKControlRequest:
		return p.handleSDKControlRequest(ctx, m)
	case SDKControlResponse:
		return p.handleSDKControlResponse(m)
	case ControlRequest:
		return p.handleControlRequest(ctx, m)
	case ControlResponse:
		return p.handleControlResponse(m)
	default:
		return &ErrProtocolViolation{
			Message: fmt.Sprintf("unexpected control message type: %T", msg),
		}
	}
}

// handleControlRequest processes a control request from the CLI.
func (p *Protocol) handleControlRequest(ctx context.Context, req ControlRequest) error {
	var resp SDKControlResponse

	switch req.Subtype {
	// Permission request from CLI (can_use_tool).
	case "can_use_tool":
		resp = p.handlePermissionRequest(ctx, req)

	// Hook callback from CLI (hook_callback).
	case "hook_callback":
		resp = p.handleHookCallback(ctx, req)

	// Host auth token refresh from CLI.
	case "host_auth_token_refresh":
		resp = p.handleHostAuthTokenRefresh(ctx, req)

	// MCP message from CLI (mcp_message) - routes to in-process MCP server.
	case "mcp_message":
		resp = p.handleMCPMessage(ctx, req)

	// MCP elicitation request from CLI (elicitation).
	case "elicitation":
		resp = p.handleElicitationRequest(ctx, req)

	default:
		resp = SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     fmt.Sprintf("unknown control request subtype: %s", req.Subtype),
			},
		}
	}

	// Send response.
	return p.transport.Write(ctx, resp)
}

// handlePermissionRequest processes a permission check request.
func (p *Protocol) handlePermissionRequest(ctx context.Context, req ControlRequest) SDKControlResponse {
	// Extract request details (per TypeScript SDK: tool_name, input).
	toolName, _ := req.Payload["tool_name"].(string)
	input := req.Payload["input"]
	toolUseID, _ := req.Payload["tool_use_id"].(string)
	agentID, _ := req.Payload["agent_id"].(string)

	// Build permission request.
	permReq := ToolPermissionRequest{
		ToolName:  toolName,
		Arguments: marshalJSON(input),
		Context: PermissionContext{
			ToolUseID: toolUseID,
			AgentID:   agentID,
		},
	}

	// Check permission callback.
	var result PermissionResult = PermissionAllow{}
	if p.options.CanUseTool != nil {
		result = p.options.CanUseTool(ctx, permReq)
	}

	// Build response in SDK format.
	respData := map[string]interface{}{
		"allowed": result.IsAllow(),
	}
	var classification PermissionDecisionClassification
	switch r := result.(type) {
	case PermissionAllow:
		classification = r.Classification
	case PermissionDeny:
		classification = r.Classification
		if !result.IsAllow() {
			respData["reason"] = r.Reason
		}
	}
	if classification != "" {
		respData["decisionClassification"] = string(classification)
	}

	return SDKControlResponse{
		Type: "control_response",
		Response: SDKControlResponseBody{
			Subtype:   "success",
			RequestID: req.RequestID,
			Response:  respData,
		},
	}
}

// handleHostAuthTokenRefresh services a host_auth_token_refresh control
// request by invoking the configured callback.
func (p *Protocol) handleHostAuthTokenRefresh(
	ctx context.Context, req ControlRequest,
) SDKControlResponse {
	if p.options.GetHostAuthToken == nil {
		return SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     "GetHostAuthToken callback is not provided",
			},
		}
	}
	token, err := p.options.GetHostAuthToken(ctx)
	if err != nil {
		return SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     err.Error(),
			},
		}
	}
	return SDKControlResponse{
		Type: "control_response",
		Response: SDKControlResponseBody{
			Subtype:   "success",
			RequestID: req.RequestID,
			Response:  map[string]interface{}{"authToken": token},
		},
	}
}

// handleElicitationRequest processes an MCP elicitation request from the CLI.
func (p *Protocol) handleElicitationRequest(ctx context.Context, req ControlRequest) SDKControlResponse {
	elReq := ElicitationRequest{}
	elReq.ServerName, _ = req.Payload["mcp_server_name"].(string)
	elReq.Message, _ = req.Payload["message"].(string)
	elReq.Mode, _ = req.Payload["mode"].(string)
	elReq.URL, _ = req.Payload["url"].(string)
	elReq.ElicitationID, _ = req.Payload["elicitation_id"].(string)
	if rs, ok := req.Payload["requested_schema"].(map[string]interface{}); ok {
		elReq.RequestedSchema = rs
	}
	elReq.Title, _ = req.Payload["title"].(string)
	elReq.DisplayName, _ = req.Payload["display_name"].(string)
	elReq.Description, _ = req.Payload["description"].(string)

	result := ElicitationResult{Action: ElicitationActionDecline}
	if p.options.OnElicitation != nil {
		callbackResult, err := p.options.OnElicitation(ctx, elReq)
		if err != nil {
			result = ElicitationResult{Action: ElicitationActionCancel}
		} else {
			result = callbackResult
		}
	}

	respData := map[string]interface{}{
		"action": result.Action,
	}
	if len(result.Content) > 0 {
		respData["content"] = result.Content
	}

	return SDKControlResponse{
		Type: "control_response",
		Response: SDKControlResponseBody{
			Subtype:   "success",
			RequestID: req.RequestID,
			Response:  respData,
		},
	}
}

// handleHookCallback processes a hook callback request.
func (p *Protocol) handleHookCallback(ctx context.Context, req ControlRequest) SDKControlResponse {
	// Extract hook details (per TypeScript SDK: callback_id, input).
	hookID, _ := req.Payload["callback_id"].(string)
	inputData, _ := req.Payload["input"].(map[string]interface{})
	hookType, _ := inputData["hook_event"].(string)

	// Find callback.
	callback, ok := p.hookCallbacks[hookID]
	if !ok {
		return SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     fmt.Sprintf("unknown hook ID: %s", hookID),
			},
		}
	}

	// Extract base hook input fields.
	base := BaseHookInput{
		SessionID:      getString(inputData, "session_id"),
		TranscriptPath: getString(inputData, "transcript_path"),
		Cwd:            getString(inputData, "cwd"),
		PermissionMode: getString(inputData, "permission_mode"),
		AgentID:        getString(inputData, "agent_id"),
		AgentType:      getString(inputData, "agent_type"),
		Effort:         getHookEffort(inputData),
	}

	// Build hook input based on type.
	var input HookInput
	switch HookType(hookType) {
	case HookTypeConfigChange:
		input = ConfigChangeInput{
			BaseHookInput: base,
			Source:        getString(inputData, "source"),
			FilePath:      getString(inputData, "file_path"),
		}
	case HookTypeInstructionsLoaded:
		input = InstructionsLoadedInput{
			BaseHookInput:   base,
			FilePath:        getString(inputData, "file_path"),
			MemoryType:      getString(inputData, "memory_type"),
			LoadReason:      getString(inputData, "load_reason"),
			Globs:           getStringSlice(inputData, "globs"),
			TriggerFilePath: getString(inputData, "trigger_file_path"),
			ParentFilePath:  getString(inputData, "parent_file_path"),
		}
	case HookTypePreToolUse:
		input = PreToolUseInput{
			BaseHookInput: base,
			ToolName:      getString(inputData, "tool_name"),
			ToolInput:     marshalJSON(inputData["tool_input"]),
		}
	case HookTypePostToolUse:
		input = PostToolUseInput{
			BaseHookInput: base,
			ToolName:      getString(inputData, "tool_name"),
			ToolInput:     marshalJSON(inputData["tool_input"]),
			ToolResponse:  marshalJSON(inputData["tool_response"]),
		}
	case HookTypeUserPromptSubmit:
		input = UserPromptSubmitInput{
			BaseHookInput: base,
			Prompt:        getString(inputData, "prompt"),
		}
	case HookTypeStop:
		input = StopInput{
			BaseHookInput:        base,
			StopHookActive:       getBool(inputData, "stop_hook_active"),
			LastAssistantMessage: getString(inputData, "last_assistant_message"),
			BackgroundTasks:      getBackgroundTaskSummaries(inputData, "background_tasks"),
			SessionCrons:         getSessionCronSummaries(inputData, "session_crons"),
		}
	case HookTypeSubagentStop:
		input = SubagentStopInput{
			BaseHookInput:        base,
			AgentName:            getString(inputData, "agent_name"),
			Status:               getString(inputData, "status"),
			Result:               getString(inputData, "result"),
			StopHookActive:       getBool(inputData, "stop_hook_active"),
			AgentTranscriptPath:  getString(inputData, "agent_transcript_path"),
			LastAssistantMessage: getString(inputData, "last_assistant_message"),
			BackgroundTasks:      getBackgroundTaskSummaries(inputData, "background_tasks"),
			SessionCrons:         getSessionCronSummaries(inputData, "session_crons"),
		}
	case HookTypePreCompact:
		input = PreCompactInput{
			BaseHookInput: base,
			Trigger:       getString(inputData, "trigger"),
			MessageCount:  getInt(inputData, "message_count"),
		}
	case HookTypePostCompact:
		input = PostCompactInput{
			BaseHookInput:  base,
			Trigger:        getString(inputData, "trigger"),
			CompactSummary: getString(inputData, "compact_summary"),
		}
	case HookTypePostToolBatch:
		input = PostToolBatchInput{
			BaseHookInput: base,
			ToolCalls:     getPostToolBatchToolCalls(inputData),
		}
	case HookTypePostToolUseFailure:
		input = PostToolUseFailureInput{
			BaseHookInput: base,
			ToolName:      getString(inputData, "tool_name"),
			ToolInput:     marshalJSON(inputData["tool_input"]),
			Error:         getString(inputData, "error"),
			IsInterrupt:   getBool(inputData, "is_interrupt"),
		}
	case HookTypeNotification:
		input = NotificationInput{
			BaseHookInput: base,
			Message:       getString(inputData, "message"),
			Title:         getString(inputData, "title"),
		}
	case HookTypeSessionStart:
		input = SessionStartInput{
			BaseHookInput: base,
			Source:        getString(inputData, "source"),
		}
	case HookTypeSessionEnd:
		input = SessionEndInput{
			BaseHookInput: base,
			Reason:        getString(inputData, "reason"),
		}
	case HookTypeSubagentStart:
		input = SubagentStartInput{
			BaseHookInput: base,
			AgentID:       getString(inputData, "agent_id"),
			AgentType:     getString(inputData, "agent_type"),
		}
	case HookTypePermissionRequest:
		input = PermissionRequestInput{
			BaseHookInput: base,
			ToolName:      getString(inputData, "tool_name"),
			ToolInput:     marshalJSON(inputData["tool_input"]),
		}
	case HookTypePermissionDenied:
		input = PermissionDeniedInput{
			BaseHookInput: base,
			ToolName:      getString(inputData, "tool_name"),
			ToolInput:     marshalJSON(inputData["tool_input"]),
			ToolUseID:     getString(inputData, "tool_use_id"),
			Reason:        getString(inputData, "reason"),
		}
	case HookTypeCwdChanged:
		input = CwdChangedInput{
			BaseHookInput: base,
			OldCwd:        getString(inputData, "old_cwd"),
			NewCwd:        getString(inputData, "new_cwd"),
		}
	case HookTypeFileChanged:
		input = FileChangedInput{
			BaseHookInput: base,
			FilePath:      getString(inputData, "file_path"),
			Event:         getString(inputData, "event"),
		}
	case HookTypeElicitation:
		requestedSchema, _ := inputData["requested_schema"].(map[string]interface{})
		input = ElicitationInput{
			BaseHookInput:   base,
			MCPServerName:   getString(inputData, "mcp_server_name"),
			Message:         getString(inputData, "message"),
			Mode:            getString(inputData, "mode"),
			URL:             getString(inputData, "url"),
			ElicitationID:   getString(inputData, "elicitation_id"),
			RequestedSchema: requestedSchema,
		}
	case HookTypeElicitationResult:
		content, _ := inputData["content"].(map[string]interface{})
		input = ElicitationResultInput{
			BaseHookInput: base,
			MCPServerName: getString(inputData, "mcp_server_name"),
			ElicitationID: getString(inputData, "elicitation_id"),
			Mode:          getString(inputData, "mode"),
			Action:        getString(inputData, "action"),
			Content:       content,
		}
	case HookTypeSetup:
		input = SetupInput{
			BaseHookInput: base,
			Trigger:       getString(inputData, "trigger"),
		}
	case HookTypeStopFailure:
		input = StopFailureInput{
			BaseHookInput:        base,
			Error:                AssistantMessageError(getString(inputData, "error")),
			ErrorDetails:         getString(inputData, "error_details"),
			LastAssistantMessage: getString(inputData, "last_assistant_message"),
		}
	case HookTypeTaskCompleted:
		input = TaskCompletedInput{
			BaseHookInput:   base,
			TaskID:          getString(inputData, "task_id"),
			TaskSubject:     getString(inputData, "task_subject"),
			TaskDescription: getString(inputData, "task_description"),
			TeammateName:    getString(inputData, "teammate_name"),
			TeamName:        getString(inputData, "team_name"),
		}
	case HookTypeTaskCreated:
		input = TaskCreatedInput{
			BaseHookInput:   base,
			TaskID:          getString(inputData, "task_id"),
			TaskSubject:     getString(inputData, "task_subject"),
			TaskDescription: getString(inputData, "task_description"),
			TeammateName:    getString(inputData, "teammate_name"),
			TeamName:        getString(inputData, "team_name"),
		}
	case HookTypeTeammateIdle:
		input = TeammateIdleInput{
			BaseHookInput: base,
			TeammateName:  getString(inputData, "teammate_name"),
			TeamName:      getString(inputData, "team_name"),
		}
	case HookTypeUserPromptExpansion:
		input = UserPromptExpansionInput{
			BaseHookInput: base,
			ExpansionType: getString(inputData, "expansion_type"),
			CommandName:   getString(inputData, "command_name"),
			CommandArgs:   getString(inputData, "command_args"),
			CommandSource: getString(inputData, "command_source"),
			Prompt:        getString(inputData, "prompt"),
		}
	case HookTypeWorktreeCreate:
		input = WorktreeCreateInput{
			BaseHookInput: base,
			Name:          getString(inputData, "name"),
		}
	case HookTypeWorktreeRemove:
		input = WorktreeRemoveInput{
			BaseHookInput: base,
			WorktreePath:  getString(inputData, "worktree_path"),
		}
	default:
		return SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     fmt.Sprintf("unknown hook type: %s", hookType),
			},
		}
	}

	// Invoke callback.
	result, err := callback(ctx, input)
	if err != nil {
		return SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     err.Error(),
			},
		}
	}

	// Build response in SDK format.
	respData := buildHookResponse(hookType, result)

	return SDKControlResponse{
		Type: "control_response",
		Response: SDKControlResponseBody{
			Subtype:   "success",
			RequestID: req.RequestID,
			Response:  respData,
		},
	}
}

// handleMCPMessage processes an MCP message from the CLI.
//
// The CLI sends mcp_message control requests when Claude invokes a tool
// on an in-process MCP server. This handler routes the tool call to the
// appropriate server and returns the result.
func (p *Protocol) handleMCPMessage(ctx context.Context, req ControlRequest) SDKControlResponse {
	// Extract payload fields.
	serverName, _ := req.Payload["server_name"].(string)
	messageID, _ := req.Payload["message_id"].(string)
	message, _ := req.Payload["message"].(map[string]interface{})

	// Find the server.
	server, ok := p.sdkMcpServers[serverName]
	if !ok {
		return SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     fmt.Sprintf("unknown MCP server: %s", serverName),
			},
		}
	}

	// Extract method and params from message.
	method, _ := message["method"].(string)
	params, _ := message["params"].(map[string]interface{})

	var responseData map[string]interface{}

	switch method {
	case "tools/call":
		// Handle tool call.
		toolName, _ := params["name"].(string)
		arguments := params["arguments"]

		// Marshal arguments to JSON.
		argsJSON, err := json.Marshal(arguments)
		if err != nil {
			return SDKControlResponse{
				Type: "control_response",
				Response: SDKControlResponseBody{
					Subtype:   "error",
					RequestID: req.RequestID,
					Error:     fmt.Sprintf("failed to marshal arguments: %v", err),
				},
			}
		}

		// Call the tool.
		result, err := server.CallTool(ctx, toolName, argsJSON)
		if err != nil {
			return SDKControlResponse{
				Type: "control_response",
				Response: SDKControlResponseBody{
					Subtype:   "error",
					RequestID: req.RequestID,
					Error:     err.Error(),
				},
			}
		}

		// Build MCP response.
		responseData = map[string]interface{}{
			"message_id": messageID,
			"result": map[string]interface{}{
				"content": result.Content,
				"isError": result.IsError,
			},
		}

	case "tools/list":
		// Handle tools list request.
		tools := make([]map[string]interface{}, 0, len(server.ToolNames()))
		for _, def := range server.ToolDefs() {
			tool := map[string]interface{}{
				"name":        def.Name,
				"description": def.Description,
			}
			if def.InputSchema != nil {
				tool["inputSchema"] = def.InputSchema
			}
			tools = append(tools, tool)
		}

		responseData = map[string]interface{}{
			"message_id": messageID,
			"result": map[string]interface{}{
				"tools": tools,
			},
		}

	default:
		return SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     fmt.Sprintf("unknown MCP method: %s", method),
			},
		}
	}

	return SDKControlResponse{
		Type: "control_response",
		Response: SDKControlResponseBody{
			Subtype:   "success",
			RequestID: req.RequestID,
			Response:  responseData,
		},
	}
}

// handleControlResponse routes a control response to the waiting request.
func (p *Protocol) handleControlResponse(resp ControlResponse) error {
	// Find pending request.
	val, ok := p.pendingReqs.LoadAndDelete(resp.RequestID)
	if !ok {
		return &ErrProtocolViolation{
			Message: fmt.Sprintf("unexpected control response for request: %s", resp.RequestID),
		}
	}

	ch, ok := val.(chan ControlResponse)
	if !ok {
		return &ErrProtocolViolation{
			Message: fmt.Sprintf("wrong channel type for request: %s", resp.RequestID),
		}
	}
	select {
	case ch <- resp:
	default:
		// Channel closed or full (shouldn't happen).
	}

	return nil
}

// handleSDKControlRequest processes an SDK control request from the CLI (TypeScript SDK format).
func (p *Protocol) handleSDKControlRequest(ctx context.Context, req SDKControlRequest) error {
	var resp SDKControlResponse

	switch req.Request.Subtype {
	case "can_use_tool":
		resp = p.handleSDKPermissionRequest(ctx, req)

	case "hook_callback":
		resp = p.handleSDKHookCallback(ctx, req)

	case "mcp_message":
		resp = p.handleSDKMCPMessage(ctx, req)

	default:
		resp = SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     fmt.Sprintf("unknown control request subtype: %s", req.Request.Subtype),
			},
		}
	}

	// Send response.
	return p.transport.Write(ctx, resp)
}

// handleSDKPermissionRequest processes a permission check request (TypeScript SDK format).
func (p *Protocol) handleSDKPermissionRequest(ctx context.Context, req SDKControlRequest) SDKControlResponse {
	// Extract request details.
	toolName := req.Request.ToolName
	arguments := req.Request.Input

	// Build permission request.
	permReq := ToolPermissionRequest{
		ToolName:  toolName,
		Arguments: marshalJSON(arguments),
		Context:   PermissionContext{},
	}

	// Check permission callback.
	var result PermissionResult = PermissionAllow{}
	if p.options.CanUseTool != nil {
		result = p.options.CanUseTool(ctx, permReq)
	}

	// Build response. The CLI expects:
	//   allow: {"behavior": "allow", "updatedInput": <original input>}
	//   deny:  {"behavior": "deny", "message": "<reason>"}
	// The updatedInput field is required for allow responses — it
	// contains the (possibly modified) tool input. For a simple
	// allow, pass the original input through unchanged.
	responseData := map[string]interface{}{
		"behavior": "allow",
	}
	var classification PermissionDecisionClassification
	if result.IsAllow() {
		// Pass the original tool input through unchanged.
		responseData["updatedInput"] = arguments
		if allow, ok := result.(PermissionAllow); ok {
			classification = allow.Classification
		}
	} else {
		responseData["behavior"] = "deny"
		if deny, ok := result.(PermissionDeny); ok {
			responseData["message"] = deny.Reason
			classification = deny.Classification
		}
	}
	if classification != "" {
		responseData["decisionClassification"] = string(classification)
	}
	responseData["toolUseID"] = req.Request.ToolUseID

	return SDKControlResponse{
		Type: "control_response",
		Response: SDKControlResponseBody{
			Subtype:   "success",
			RequestID: req.RequestID,
			Response:  responseData,
		},
	}
}

// handleSDKHookCallback processes a hook callback request (TypeScript SDK format).
func (p *Protocol) handleSDKHookCallback(ctx context.Context, req SDKControlRequest) SDKControlResponse {
	// Extract hook details.
	callbackID := req.Request.CallbackID
	hookInput := req.Request.Input

	// Find callback.
	callback, ok := p.hookCallbacks[callbackID]
	if !ok {
		return SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     fmt.Sprintf("unknown hook callback ID: %s", callbackID),
			},
		}
	}

	// Extract base hook input fields.
	base := BaseHookInput{
		SessionID:      getString(hookInput, "session_id"),
		TranscriptPath: getString(hookInput, "transcript_path"),
		Cwd:            getString(hookInput, "cwd"),
		PermissionMode: getString(hookInput, "permission_mode"),
		AgentID:        getString(hookInput, "agent_id"),
		AgentType:      getString(hookInput, "agent_type"),
		Effort:         getHookEffort(hookInput),
	}

	// Build hook input based on hook_event_name.
	hookEventName := getString(hookInput, "hook_event_name")
	var input HookInput

	switch hookEventName {
	case "ConfigChange":
		input = ConfigChangeInput{
			BaseHookInput: base,
			Source:        getString(hookInput, "source"),
			FilePath:      getString(hookInput, "file_path"),
		}
	case "InstructionsLoaded":
		input = InstructionsLoadedInput{
			BaseHookInput:   base,
			FilePath:        getString(hookInput, "file_path"),
			MemoryType:      getString(hookInput, "memory_type"),
			LoadReason:      getString(hookInput, "load_reason"),
			Globs:           getStringSlice(hookInput, "globs"),
			TriggerFilePath: getString(hookInput, "trigger_file_path"),
			ParentFilePath:  getString(hookInput, "parent_file_path"),
		}
	case "PreToolUse":
		input = PreToolUseInput{
			BaseHookInput: base,
			ToolName:      getString(hookInput, "tool_name"),
			ToolInput:     marshalJSON(hookInput["tool_input"]),
		}
	case "PostToolUse":
		input = PostToolUseInput{
			BaseHookInput: base,
			ToolName:      getString(hookInput, "tool_name"),
			ToolInput:     marshalJSON(hookInput["tool_input"]),
			ToolResponse:  marshalJSON(hookInput["tool_response"]),
		}
	case "UserPromptSubmit":
		input = UserPromptSubmitInput{
			BaseHookInput: base,
			Prompt:        getString(hookInput, "prompt"),
		}
	case "Stop":
		input = StopInput{
			BaseHookInput:        base,
			StopHookActive:       getBool(hookInput, "stop_hook_active"),
			LastAssistantMessage: getString(hookInput, "last_assistant_message"),
			BackgroundTasks:      getBackgroundTaskSummaries(hookInput, "background_tasks"),
			SessionCrons:         getSessionCronSummaries(hookInput, "session_crons"),
		}
	case "SubagentStop":
		input = SubagentStopInput{
			BaseHookInput:        base,
			AgentName:            getString(hookInput, "agent_name"),
			Status:               getString(hookInput, "status"),
			Result:               getString(hookInput, "result"),
			StopHookActive:       getBool(hookInput, "stop_hook_active"),
			AgentTranscriptPath:  getString(hookInput, "agent_transcript_path"),
			LastAssistantMessage: getString(hookInput, "last_assistant_message"),
			BackgroundTasks:      getBackgroundTaskSummaries(hookInput, "background_tasks"),
			SessionCrons:         getSessionCronSummaries(hookInput, "session_crons"),
		}
	case "PreCompact":
		input = PreCompactInput{
			BaseHookInput: base,
			Trigger:       getString(hookInput, "trigger"),
			MessageCount:  getInt(hookInput, "message_count"),
		}
	case "PostCompact":
		input = PostCompactInput{
			BaseHookInput:  base,
			Trigger:        getString(hookInput, "trigger"),
			CompactSummary: getString(hookInput, "compact_summary"),
		}
	case "PostToolBatch":
		input = PostToolBatchInput{
			BaseHookInput: base,
			ToolCalls:     getPostToolBatchToolCalls(hookInput),
		}
	case "PostToolUseFailure":
		input = PostToolUseFailureInput{
			BaseHookInput: base,
			ToolName:      getString(hookInput, "tool_name"),
			ToolInput:     marshalJSON(hookInput["tool_input"]),
			Error:         getString(hookInput, "error"),
			IsInterrupt:   getBool(hookInput, "is_interrupt"),
		}
	case "Notification":
		input = NotificationInput{
			BaseHookInput: base,
			Message:       getString(hookInput, "message"),
			Title:         getString(hookInput, "title"),
		}
	case "SessionStart":
		input = SessionStartInput{
			BaseHookInput: base,
			Source:        getString(hookInput, "source"),
		}
	case "SessionEnd":
		input = SessionEndInput{
			BaseHookInput: base,
			Reason:        getString(hookInput, "reason"),
		}
	case "SubagentStart":
		input = SubagentStartInput{
			BaseHookInput: base,
			AgentID:       getString(hookInput, "agent_id"),
			AgentType:     getString(hookInput, "agent_type"),
		}
	case "PermissionRequest":
		input = PermissionRequestInput{
			BaseHookInput: base,
			ToolName:      getString(hookInput, "tool_name"),
			ToolInput:     marshalJSON(hookInput["tool_input"]),
		}
	case "PermissionDenied":
		input = PermissionDeniedInput{
			BaseHookInput: base,
			ToolName:      getString(hookInput, "tool_name"),
			ToolInput:     marshalJSON(hookInput["tool_input"]),
			ToolUseID:     getString(hookInput, "tool_use_id"),
			Reason:        getString(hookInput, "reason"),
		}
	case "CwdChanged":
		input = CwdChangedInput{
			BaseHookInput: base,
			OldCwd:        getString(hookInput, "old_cwd"),
			NewCwd:        getString(hookInput, "new_cwd"),
		}
	case "FileChanged":
		input = FileChangedInput{
			BaseHookInput: base,
			FilePath:      getString(hookInput, "file_path"),
			Event:         getString(hookInput, "event"),
		}
	case "Elicitation":
		requestedSchema, _ := hookInput["requested_schema"].(map[string]interface{})
		input = ElicitationInput{
			BaseHookInput:   base,
			MCPServerName:   getString(hookInput, "mcp_server_name"),
			Message:         getString(hookInput, "message"),
			Mode:            getString(hookInput, "mode"),
			URL:             getString(hookInput, "url"),
			ElicitationID:   getString(hookInput, "elicitation_id"),
			RequestedSchema: requestedSchema,
		}
	case "ElicitationResult":
		content, _ := hookInput["content"].(map[string]interface{})
		input = ElicitationResultInput{
			BaseHookInput: base,
			MCPServerName: getString(hookInput, "mcp_server_name"),
			ElicitationID: getString(hookInput, "elicitation_id"),
			Mode:          getString(hookInput, "mode"),
			Action:        getString(hookInput, "action"),
			Content:       content,
		}
	case "Setup":
		input = SetupInput{
			BaseHookInput: base,
			Trigger:       getString(hookInput, "trigger"),
		}
	case "StopFailure":
		input = StopFailureInput{
			BaseHookInput:        base,
			Error:                AssistantMessageError(getString(hookInput, "error")),
			ErrorDetails:         getString(hookInput, "error_details"),
			LastAssistantMessage: getString(hookInput, "last_assistant_message"),
		}
	case "TaskCompleted":
		input = TaskCompletedInput{
			BaseHookInput:   base,
			TaskID:          getString(hookInput, "task_id"),
			TaskSubject:     getString(hookInput, "task_subject"),
			TaskDescription: getString(hookInput, "task_description"),
			TeammateName:    getString(hookInput, "teammate_name"),
			TeamName:        getString(hookInput, "team_name"),
		}
	case "TaskCreated":
		input = TaskCreatedInput{
			BaseHookInput:   base,
			TaskID:          getString(hookInput, "task_id"),
			TaskSubject:     getString(hookInput, "task_subject"),
			TaskDescription: getString(hookInput, "task_description"),
			TeammateName:    getString(hookInput, "teammate_name"),
			TeamName:        getString(hookInput, "team_name"),
		}
	case "TeammateIdle":
		input = TeammateIdleInput{
			BaseHookInput: base,
			TeammateName:  getString(hookInput, "teammate_name"),
			TeamName:      getString(hookInput, "team_name"),
		}
	case "UserPromptExpansion":
		input = UserPromptExpansionInput{
			BaseHookInput: base,
			ExpansionType: getString(hookInput, "expansion_type"),
			CommandName:   getString(hookInput, "command_name"),
			CommandArgs:   getString(hookInput, "command_args"),
			CommandSource: getString(hookInput, "command_source"),
			Prompt:        getString(hookInput, "prompt"),
		}
	case "WorktreeCreate":
		input = WorktreeCreateInput{
			BaseHookInput: base,
			Name:          getString(hookInput, "name"),
		}
	case "WorktreeRemove":
		input = WorktreeRemoveInput{
			BaseHookInput: base,
			WorktreePath:  getString(hookInput, "worktree_path"),
		}
	default:
		return SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     fmt.Sprintf("unknown hook event name: %s", hookEventName),
			},
		}
	}

	// Invoke callback.
	result, err := callback(ctx, input)
	if err != nil {
		return SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     err.Error(),
			},
		}
	}

	// Build response.
	responseData := buildHookResponse(hookEventName, result)

	return SDKControlResponse{
		Type: "control_response",
		Response: SDKControlResponseBody{
			Subtype:   "success",
			RequestID: req.RequestID,
			Response:  responseData,
		},
	}
}

// handleSDKMCPMessage processes an MCP message from the CLI (TypeScript SDK format).
//
// The CLI sends mcp_message control requests when Claude invokes a tool
// on an in-process MCP server. This handler routes the tool call to the
// appropriate server and returns the result.
func (p *Protocol) handleSDKMCPMessage(ctx context.Context, req SDKControlRequest) SDKControlResponse {
	serverName := req.Request.ServerName
	message := req.Request.Message

	// Find the server.
	server, ok := p.sdkMcpServers[serverName]
	if !ok {
		return SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     fmt.Sprintf("unknown MCP server: %s", serverName),
			},
		}
	}

	// Extract method and params from message.
	method, _ := message["method"].(string)
	params, _ := message["params"].(map[string]interface{})

	// Extract message ID for response correlation.
	messageID := message["id"]

	var responseData map[string]interface{}

	switch method {
	case "initialize":
		// MCP protocol handshake - respond with server info and capabilities.
		// Return the full JSONRPC response envelope.
		result := map[string]interface{}{
			"protocolVersion": "2025-11-25",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{
					"listChanged": false,
				},
			},
			"serverInfo": map[string]interface{}{
				"name":    server.Name(),
				"version": server.Version(),
			},
		}
		if instr := server.Instructions(); instr != "" {
			result["instructions"] = instr
		}
		responseData = map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      messageID,
			"result":  result,
		}

	case "notifications/initialized", "notifications/cancelled": //nolint:misspell // MCP protocol uses British spelling
		// Notifications don't require responses, but we send empty success.
		responseData = map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      messageID,
			"result":  map[string]interface{}{},
		}

	case "tools/call":
		// Handle tool call.
		toolName, _ := params["name"].(string)
		arguments := params["arguments"]

		// Marshal arguments to JSON.
		argsJSON, err := json.Marshal(arguments)
		if err != nil {
			return SDKControlResponse{
				Type: "control_response",
				Response: SDKControlResponseBody{
					Subtype:   "error",
					RequestID: req.RequestID,
					Error:     fmt.Sprintf("failed to marshal arguments: %v", err),
				},
			}
		}

		// Call the tool.
		result, err := server.CallTool(ctx, toolName, argsJSON)
		if err != nil {
			return SDKControlResponse{
				Type: "control_response",
				Response: SDKControlResponseBody{
					Subtype:   "error",
					RequestID: req.RequestID,
					Error:     err.Error(),
				},
			}
		}

		// Build MCP response (JSONRPC format).
		responseData = map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      messageID,
			"result": map[string]interface{}{
				"content": result.Content,
				"isError": result.IsError,
			},
		}

	case "tools/list":
		// Handle tools list request.
		tools := make([]map[string]interface{}, 0, len(server.ToolNames()))
		for _, def := range server.ToolDefs() {
			tool := map[string]interface{}{
				"name":        def.Name,
				"description": def.Description,
			}
			if def.InputSchema != nil {
				tool["inputSchema"] = def.InputSchema
			}
			tools = append(tools, tool)
		}

		responseData = map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      messageID,
			"result": map[string]interface{}{
				"tools": tools,
			},
		}

	default:
		return SDKControlResponse{
			Type: "control_response",
			Response: SDKControlResponseBody{
				Subtype:   "error",
				RequestID: req.RequestID,
				Error:     fmt.Sprintf("unknown MCP method: %s", method),
			},
		}
	}

	// Wrap the JSONRPC response in mcp_response field.
	return SDKControlResponse{
		Type: "control_response",
		Response: SDKControlResponseBody{
			Subtype:   "success",
			RequestID: req.RequestID,
			Response: map[string]interface{}{
				"mcp_response": responseData,
			},
		},
	}
}

// handleSDKControlResponse routes an SDK control response to the waiting request.
func (p *Protocol) handleSDKControlResponse(resp SDKControlResponse) error {
	requestID := resp.Response.RequestID
	// Find pending request.
	val, ok := p.pendingReqs.LoadAndDelete(requestID)
	if !ok {
		return &ErrProtocolViolation{
			Message: fmt.Sprintf("unexpected SDK control response for request: %s", requestID),
		}
	}

	// Route based on channel type.
	switch ch := val.(type) {
	case chan SDKControlResponse:
		select {
		case ch <- resp:
		default:
		}
	case chan ControlResponse:
		// Convert to legacy format for backward compatibility.
		legacy := ControlResponse{
			Type:      "control",
			RequestID: requestID,
			Result:    resp.Response.Response,
		}
		if resp.Response.Subtype == "error" {
			legacy.Error = &ProtocolError{
				Code:    "error",
				Message: resp.Response.Error,
			}
		}
		select {
		case ch <- legacy:
		default:
		}
	}

	return nil
}

// waitForSDKResponse waits for an SDK control response with the given request ID.
func (p *Protocol) waitForSDKResponse(ctx context.Context, requestID string) (SDKControlResponse, error) {
	ch := make(chan SDKControlResponse, 1)
	p.pendingReqs.Store(requestID, ch)

	select {
	case <-ctx.Done():
		p.pendingReqs.Delete(requestID)
		return SDKControlResponse{}, ctx.Err()
	case resp := <-ch:
		return resp, nil
	}
}

// nextRequestID generates a unique request ID.
func (p *Protocol) nextRequestID() string {
	id := p.requestID.Add(1)
	return fmt.Sprintf("req_%d", id)
}

// sendRequest sends a legacy-shape control request and returns a channel for
// the response. Currently no callers remain after the v0.2.119 catchup moved
// stream introspection to the cached-init / SDKControlRequest paths; kept so
// a follow-up cleanup PR can remove this and the legacy ControlResponse type
// in one focused diff.
//
//nolint:unused // see comment above; scheduled removal in follow-up
func (p *Protocol) sendRequest(ctx context.Context, subtype string, payload map[string]interface{}) <-chan ControlResponse {
	respCh := make(chan ControlResponse, 1)

	req := ControlRequest{
		Type:      "control",
		Subtype:   subtype,
		RequestID: p.nextRequestID(),
		Payload:   payload,
	}

	// Register pending request before sending to avoid race
	p.pendingReqs.Store(req.RequestID, respCh)

	// Send request asynchronously
	go func() {
		if err := p.transport.Write(ctx, req); err != nil {
			// Clean up and send error response
			p.pendingReqs.Delete(req.RequestID)
			respCh <- ControlResponse{
				Type:      "control",
				RequestID: req.RequestID,
				Error: &ProtocolError{
					Code:    "send_error",
					Message: err.Error(),
				},
			}
		}
	}()

	return respCh
}

// Helper functions for extracting typed values from maps

func getString(m map[string]interface{}, key string) string {
	v, ok := m[key].(string)
	if !ok {
		return ""
	}
	return v
}

func getInt(m map[string]interface{}, key string) int {
	v, ok := m[key].(float64) // JSON numbers are float64
	if !ok {
		return 0
	}
	return int(v)
}

func getBool(m map[string]interface{}, key string) bool {
	v, ok := m[key].(bool)
	if !ok {
		return false
	}
	return v
}

func getHookEffort(m map[string]interface{}) *HookEffort {
	effort, ok := m["effort"].(map[string]interface{})
	if !ok {
		return nil
	}

	level := getString(effort, "level")
	if level == "" {
		return nil
	}

	return &HookEffort{Level: EffortLevel(level)}
}

func marshalJSON(v interface{}) []byte {
	// This is a simplified version - in production, handle errors
	if v == nil {
		return []byte("null")
	}
	data, _ := json.Marshal(v)
	return data
}

func getStringSlice(m map[string]interface{}, key string) []string {
	raw, ok := m[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func getPostToolBatchToolCalls(m map[string]interface{}) []PostToolBatchToolCall {
	raw, ok := m["tool_calls"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]PostToolBatchToolCall, 0, len(raw))
	for _, v := range raw {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		call := PostToolBatchToolCall{
			ToolName:  getString(entry, "tool_name"),
			ToolInput: marshalJSON(entry["tool_input"]),
			ToolUseID: getString(entry, "tool_use_id"),
		}
		// Preserve the absent-vs-null distinction: TS `tool_response?: unknown`
		// allows an explicit null payload, which must round-trip as "null" rather
		// than be conflated with the key being missing.
		if resp, ok := entry["tool_response"]; ok {
			call.ToolResponse = marshalJSON(resp)
		}
		out = append(out, call)
	}
	return out
}

func getBackgroundTaskSummaries(m map[string]interface{}, key string) []BackgroundTaskSummary {
	raw, ok := m[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]BackgroundTaskSummary, 0, len(raw))
	for _, v := range raw {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		out = append(out, BackgroundTaskSummary{
			ID:          getString(entry, "id"),
			Type:        getString(entry, "type"),
			Status:      getString(entry, "status"),
			Description: getString(entry, "description"),
			Command:     getString(entry, "command"),
			AgentType:   getString(entry, "agent_type"),
			Server:      getString(entry, "server"),
			Tool:        getString(entry, "tool"),
			Name:        getString(entry, "name"),
		})
	}
	return out
}

func getSessionCronSummaries(m map[string]interface{}, key string) []SessionCronSummary {
	raw, ok := m[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]SessionCronSummary, 0, len(raw))
	for _, v := range raw {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		out = append(out, SessionCronSummary{
			ID:        getString(entry, "id"),
			Schedule:  getString(entry, "schedule"),
			Recurring: getBool(entry, "recurring"),
			Prompt:    getString(entry, "prompt"),
		})
	}
	return out
}

// buildHookResponse constructs the response data map for hook callbacks.
//
// The hookType parameter identifies the hook event being responded to,
// which determines how tool input modifications are serialized. For
// PreToolUse hooks, the CLI expects modifications via
// hookSpecificOutput.updatedInput rather than a top-level modify field.
//
// For Stop hooks, the Decision/Reason/SystemMessage fields enable the
// Ralph Wiggum pattern where a hook can block session exit and reinject
// a new prompt.
//
// When the Decision field is set (Stop/SubagentStop hooks), the continue
// field is omitted to match the format that shell-based hooks produce.
// Shell hooks output {"decision":"block","reason":"..."} without a
// continue field. Including "continue":false alongside "decision":"block"
// causes the CLI to short-circuit and terminate the session before
// honoring the block decision.
func buildHookResponse(hookType string, result HookResult) map[string]interface{} {
	resp := make(map[string]interface{})

	// Stop hook path: decision/reason/systemMessage only, no continue.
	if result.Decision != "" {
		resp["decision"] = result.Decision

		if result.Reason != "" {
			resp["reason"] = result.Reason
		}
		if result.SystemMessage != "" {
			resp["systemMessage"] = result.SystemMessage
		}
	} else {
		// For non-Stop hooks (PreToolUse, PostToolUse, etc.),
		// emit the continue field as before.
		resp["continue"] = result.Continue
	}

	if result.TerminalSequence != "" {
		resp["terminalSequence"] = result.TerminalSequence
	}

	// If HookSpecificOutput is set explicitly, use it directly.
	// This gives callbacks full control over the hookSpecificOutput
	// envelope when auto-translation of Modify is insufficient.
	if result.HookSpecificOutput != nil {
		hookSpecificOutput := make(map[string]interface{}, len(result.HookSpecificOutput))
		for key, value := range result.HookSpecificOutput {
			hookSpecificOutput[key] = value
		}
		resp["hookSpecificOutput"] = hookSpecificOutput
	} else if result.UpdatedToolOutput != nil {
		resp["hookSpecificOutput"] = map[string]interface{}{
			"hookEventName":     "PostToolUse",
			"updatedToolOutput": result.UpdatedToolOutput,
		}
	} else if len(result.Modify) > 0 {
		// Auto-translate Modify into the hookSpecificOutput format
		// expected by the CLI. PreToolUse and PermissionRequest hooks
		// use hookSpecificOutput.updatedInput for tool input
		// modifications. Other hook types fall back to the legacy
		// modify field.
		switch hookType {
		case "PreToolUse":
			resp["hookSpecificOutput"] = map[string]interface{}{
				"hookEventName":      "PreToolUse",
				"permissionDecision": "allow",
				"updatedInput":       result.Modify,
			}

		case "PermissionRequest":
			resp["hookSpecificOutput"] = map[string]interface{}{
				"hookEventName": "PermissionRequest",
				"decision": map[string]interface{}{
					"behavior":     "allow",
					"updatedInput": result.Modify,
				},
			}

		default:
			resp["modify"] = result.Modify
		}
	}

	if len(result.WatchPaths) > 0 && isWatchPathsHook(hookType) {
		hookSpecificOutput, _ := resp["hookSpecificOutput"].(map[string]interface{})
		if hookSpecificOutput == nil {
			hookSpecificOutput = map[string]interface{}{
				"hookEventName": hookType,
			}
		}
		hookSpecificOutput["watchPaths"] = result.WatchPaths
		resp["hookSpecificOutput"] = hookSpecificOutput
	}

	if result.SuppressOriginalPrompt != nil && hookType == string(HookTypeUserPromptSubmit) {
		hookSpecificOutput, _ := resp["hookSpecificOutput"].(map[string]interface{})
		if hookSpecificOutput == nil {
			hookSpecificOutput = map[string]interface{}{
				"hookEventName": string(HookTypeUserPromptSubmit),
			}
		}
		hookSpecificOutput["suppressOriginalPrompt"] = *result.SuppressOriginalPrompt
		resp["hookSpecificOutput"] = hookSpecificOutput
	}

	return resp
}

// isWatchPathsHook returns true for hook events whose
// hookSpecificOutput accepts the optional watchPaths field per
// sdk.d.ts v0.2.119: CwdChanged (L435-L438), FileChanged (L555-L558),
// and SessionStart (L3515-L3520). WorktreeCreate's specific output is
// {hookEventName, worktreePath} (L5423-L5426) and does not accept
// watchPaths.
func isWatchPathsHook(hookType string) bool {
	switch hookType {
	case string(HookTypeSessionStart),
		string(HookTypeCwdChanged),
		string(HookTypeFileChanged):
		return true
	default:
		return false
	}
}
