package claudeagent

import (
	"encoding/json"
	"time"
)

// Message is the base interface for all messages exchanged with Claude Code CLI.
//
// Messages can be user prompts, assistant responses, control protocol requests,
// streaming events, or result notifications. The MessageType method returns a
// string identifier used for routing and serialization.
type Message interface {
	MessageType() string
}

// UserMessage represents a user prompt sent to Claude.
//
// This message type initiates or continues a conversation. The ParentToolUseID
// field links this message to a specific tool call when providing tool results.
type UserMessage struct {
	Type            string               `json:"type"`                       // Always "user"
	UUID            string               `json:"uuid,omitempty"`             // Unique message ID
	SessionID       string               `json:"session_id"`                 // Session identifier
	Message         APIUserMessage       `json:"message"`                    // Message content
	ParentToolUseID *string              `json:"parent_tool_use_id"`         // For tool results (null if not tool result)
	IsSynthetic     bool                 `json:"isSynthetic,omitempty"`      // True for system-generated messages
	ToolUseResult   interface{}          `json:"tool_use_result,omitempty"`  // Tool result JSON if applicable
	Priority        *UserMessagePriority `json:"priority,omitempty"`         // Scheduling priority
	SubagentType    string               `json:"subagent_type,omitempty"`    // Subagent type that produced this message
	TaskDescription string               `json:"task_description,omitempty"` // Description of the subagent task that produced this message
}

// APIUserMessage represents the message content in Anthropic API format.
type APIUserMessage struct {
	Role    string             `json:"role"`    // Always "user"
	Content []UserContentBlock `json:"content"` // Array of content blocks
}

// UserContentBlock represents a content block in a user message.
type UserContentBlock struct {
	Type string `json:"type"`           // "text" or other types
	Text string `json:"text,omitempty"` // Text content
}

// UserMessageReplay represents a replayed user message during session resume.
type UserMessageReplay struct {
	Type            string               `json:"type"`       // Always "user"
	UUID            string               `json:"uuid"`       // Unique message ID
	SessionID       string               `json:"session_id"` // Session identifier
	Message         APIUserMessage       `json:"message"`    // Message content
	ParentToolUseID *string              `json:"parent_tool_use_id"`
	IsReplay        bool                 `json:"isReplay"` // True for replayed messages
	Priority        *UserMessagePriority `json:"priority,omitempty"`
}

// UserMessagePriority indicates when a user message should be handled.
type UserMessagePriority string

const (
	UserMessagePriorityNow   UserMessagePriority = "now"
	UserMessagePriorityNext  UserMessagePriority = "next"
	UserMessagePriorityLater UserMessagePriority = "later"
)

// MessageType implements Message.
func (m UserMessage) MessageType() string { return "user" }

// MessageType implements Message.
func (m UserMessageReplay) MessageType() string { return "user" }

// AssistantMessage represents a response from Claude.
//
// Assistant messages contain one or more content blocks that can be text,
// tool use requests, or thinking blocks. Each message includes usage
// information for billing and rate limiting.
type AssistantMessage struct {
	Type      string `json:"type"`                 // Always "assistant"
	UUID      string `json:"uuid,omitempty"`       // Unique message ID
	SessionID string `json:"session_id,omitempty"` // Session identifier
	Message   struct {
		Role    string         `json:"role"`    // Always "assistant"
		Content []ContentBlock `json:"content"` // Response content blocks
	} `json:"message"`
	ParentToolUseID *string `json:"parent_tool_use_id,omitempty"` // Parent tool use if in subagent
	Usage           *Usage  `json:"usage,omitempty"`              // Token usage for this message

	// RequestID is the upstream API request id that produced this message,
	// when available. Optional.
	RequestID string `json:"request_id,omitempty"`

	// SubagentType is the subagent type that produced this message, when
	// the message originated from a subagent task. Empty for top-level
	// assistant turns.
	SubagentType string `json:"subagent_type,omitempty"`

	// TaskDescription is a short description of the subagent task that
	// produced this message. Set alongside SubagentType.
	TaskDescription string `json:"task_description,omitempty"`

	// Error is the upstream API error code, if the assistant turn failed.
	// Empty for a successful turn. See AssistantMessageError for known
	// variants.
	Error AssistantMessageError `json:"error,omitempty"`
}

// MessageType implements Message.
func (m AssistantMessage) MessageType() string { return "assistant" }

// ContentText returns the concatenated text from all text content blocks.
//
// This is a convenience method for extracting the main text response,
// ignoring tool use and thinking blocks.
func (m AssistantMessage) ContentText() string {
	var text string
	for _, block := range m.Message.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return text
}

// ContentBlock represents a single content element in an assistant message.
//
// Content blocks can be:
// - text: Plain text response
// - tool_use: Request to execute a tool
// - thinking: Claude's reasoning process (when extended thinking is enabled)
type ContentBlock struct {
	Type  string          `json:"type"`            // "text", "tool_use", or "thinking"
	Text  string          `json:"text,omitempty"`  // For text and thinking blocks
	ID    string          `json:"id,omitempty"`    // For tool_use blocks (unique ID)
	Name  string          `json:"name,omitempty"`  // For tool_use blocks (tool name)
	Input json.RawMessage `json:"input,omitempty"` // For tool_use blocks (arguments)
}

// BlockType returns the type of this content block.
func (c ContentBlock) BlockType() string { return c.Type }

// ResultMessage represents the final outcome of a conversation turn.
//
// This message signals completion (success or error) and includes cumulative
// usage statistics for the entire interaction.
type ResultMessage struct {
	Type   string `json:"type"`   // Always "result"
	Status string `json:"status"` // "success" or "error" (deprecated: use Subtype)

	// Subtype indicates the result type (TypeScript SDK compatible).
	// Values: "success", "error_max_turns", "error_during_execution",
	// "error_max_budget_usd", "error_max_structured_output_retries"
	Subtype string `json:"subtype,omitempty"`

	UUID      string `json:"uuid,omitempty"`       // Unique message ID
	SessionID string `json:"session_id,omitempty"` // Session identifier

	Result string   `json:"result,omitempty"` // Result text (for success)
	Errors []string `json:"errors,omitempty"` // Error messages (for errors)

	DurationMs    int64 `json:"duration_ms,omitempty"`     // Total duration in milliseconds
	DurationAPIMs int64 `json:"duration_api_ms,omitempty"` // API call duration in milliseconds
	TTFTMs        int64 `json:"ttft_ms,omitempty"`         // Time-to-first-token in milliseconds
	IsError       bool  `json:"is_error,omitempty"`        // Whether this is an error result
	NumTurns      int   `json:"num_turns,omitempty"`       // Number of conversation turns

	TotalCostUSD float64 `json:"total_cost_usd,omitempty"` // Total cost in USD

	Usage      *NonNullableUsage     `json:"usage,omitempty"`      // Token usage
	ModelUsage map[string]ModelUsage `json:"modelUsage,omitempty"` // Per-model usage

	PermissionDenials []PermissionDenial `json:"permission_denials,omitempty"` // Denied permissions
	StructuredOutput  interface{}        `json:"structured_output,omitempty"`  // Structured output (if OutputFormat set)
	StopReason        *string            `json:"stop_reason"`                  // Stop reason, explicitly null when absent
	TerminalReason    *TerminalReason    `json:"terminal_reason,omitempty"`    // Terminal completion reason
	Origin            *MessageOrigin     `json:"origin,omitempty"`             // Originating actor for this result
	FastModeState     *FastModeState     `json:"fast_mode_state,omitempty"`    // Fast mode state at completion
}

// MessageType implements Message.
func (m ResultMessage) MessageType() string { return "result" }

// MessageOriginKind discriminates a MessageOrigin.
type MessageOriginKind string

const (
	MessageOriginKindHuman            MessageOriginKind = "human"
	MessageOriginKindChannel          MessageOriginKind = "channel"
	MessageOriginKindPeer             MessageOriginKind = "peer"
	MessageOriginKindTaskNotification MessageOriginKind = "task-notification"
	MessageOriginKindCoordinator      MessageOriginKind = "coordinator"
)

// MessageOrigin describes the originating actor for a message.
type MessageOrigin struct {
	Kind   MessageOriginKind `json:"kind"`
	Server string            `json:"server,omitempty"`
	From   string            `json:"from,omitempty"`
	Name   string            `json:"name,omitempty"`
}

// StreamEvent represents a progressive delta update during streaming.
//
// Stream events allow real-time display of Claude's response as it's generated.
// The Event field indicates whether this is a delta (partial update) or done
// (streaming complete for this message).
type StreamEvent struct {
	Type      string    `json:"type"`  // Always "stream_event"
	Event     string    `json:"event"` // "delta" or "done"
	Delta     string    `json:"delta,omitempty"`
	Timestamp time.Time `json:"timestamp"` // Event timestamp
}

// MessageType implements Message.
func (m StreamEvent) MessageType() string { return "stream_event" }

// TodoUpdateMessage contains task tracking updates from Claude.
//
// Claude automatically creates and updates todos for complex multi-step tasks.
// This message type allows clients to display progress tracking UI.
type TodoUpdateMessage struct {
	Type  string     `json:"type"` // Always "todo_update"
	Items []TodoItem `json:"items"`
}

// MessageType implements Message.
func (m TodoUpdateMessage) MessageType() string { return "todo_update" }

// TodoItem represents a single task in Claude's task list.
//
// Each item has two forms: Content (imperative: "Run tests") and ActiveForm
// (continuous: "Running tests"). The Status field tracks lifecycle state.
type TodoItem struct {
	Content    string     `json:"content"`    // Task description (imperative form)
	ActiveForm string     `json:"activeForm"` // In-progress form (continuous)
	Status     TodoStatus `json:"status"`     // Lifecycle state
}

// TodoStatus represents the lifecycle state of a todo item.
type TodoStatus string

const (
	// TodoStatusPending indicates the task has not started.
	TodoStatusPending TodoStatus = "pending"

	// TodoStatusInProgress indicates the task is currently being worked on.
	TodoStatusInProgress TodoStatus = "in_progress"

	// TodoStatusCompleted indicates the task has finished.
	TodoStatusCompleted TodoStatus = "completed"
)

// SubagentResultMessage contains the result of a subagent invocation.
//
// When Claude delegates work to a specialized subagent, this message
// communicates the outcome back to the main agent.
type SubagentResultMessage struct {
	Type      string `json:"type"`       // Always "subagent_result"
	AgentName string `json:"agent_name"` // Subagent identifier
	Status    string `json:"status"`     // "success" or "error"
	Result    string `json:"result"`     // Subagent output
}

// MessageType implements Message.
func (m SubagentResultMessage) MessageType() string { return "subagent_result" }

// SDKControlRequest represents a control protocol request sent from SDK to CLI.
//
// Control requests are used for initialization, permission checks, hook
// invocations, and other SDK-level coordination. Each request has a unique
// ID for correlation with responses.
type SDKControlRequest struct {
	Type      string                `json:"type"`       // Always "control_request"
	RequestID string                `json:"request_id"` // Unique request ID (snake_case)
	Request   SDKControlRequestBody `json:"request"`    // Nested request payload
}

// SDKControlRequestBody contains the actual request data.
// Note: This is a union type - different fields are used for different subtypes.
type SDKControlRequestBody struct {
	Subtype                string                              `json:"subtype"`                          // Request subtype
	Hooks                  map[string][]SDKHookCallbackMatcher `json:"hooks,omitempty"`                  // For initialize
	SDKMCPServers          []string                            `json:"sdkMcpServers,omitempty"`          // For initialize
	MCPServers             map[string]MCPServerConfig          `json:"mcpServers,omitempty"`             // For initialize
	JSONSchema             map[string]interface{}              `json:"jsonSchema,omitempty"`             // For initialize
	SystemPrompt           string                              `json:"systemPrompt,omitempty"`           // For initialize
	AppendSystemPrompt     string                              `json:"appendSystemPrompt,omitempty"`     // For initialize
	PlanModeInstructions   string                              `json:"planModeInstructions,omitempty"`   // For initialize
	ExcludeDynamicSections *bool                               `json:"excludeDynamicSections,omitempty"` // For initialize
	Agents                 map[string]interface{}              `json:"agents,omitempty"`                 // For initialize
	Title                  string                              `json:"title,omitempty"`                  // For initialize
	Skills                 []string                            `json:"skills,omitempty"`                 // For initialize
	PromptSuggestions      *bool                               `json:"promptSuggestions,omitempty"`      // For initialize
	AgentProgressSummaries *bool                               `json:"agentProgressSummaries,omitempty"` // For initialize
	ForwardSubagentText    *bool                               `json:"forwardSubagentText,omitempty"`    // For initialize
	ToolAliases            map[string]string                   `json:"toolAliases,omitempty"`            // For initialize
	ToolName               string                              `json:"tool_name,omitempty"`              // For can_use_tool/hook_callback
	Input                  map[string]interface{}              `json:"input,omitempty"`                  // For can_use_tool/hook_callback
	ToolUseID              string                              `json:"tool_use_id,omitempty"`            // For can_use_tool/hooks/background_tasks
	AgentID                string                              `json:"agent_id,omitempty"`               // For can_use_tool
	CallbackID             string                              `json:"callback_id,omitempty"`            // For hook_callback
	Mode                   string                              `json:"mode,omitempty"`                   // For set_permission_mode
	Model                  string                              `json:"model,omitempty"`                  // For set_model
	MaxThinkingTokens      *int                                `json:"max_thinking_tokens,omitempty"`    // For set_max_thinking_tokens
	UserMessageID          string                              `json:"user_message_id,omitempty"`        // For rewind_files
	DryRun                 *bool                               `json:"dry_run,omitempty"`                // For rewind_files
	Path                   string                              `json:"path,omitempty"`                   // For read_file/seed_read_state
	MaxBytes               *int                                `json:"max_bytes,omitempty"`              // For read_file
	Encoding               string                              `json:"encoding,omitempty"`               // For read_file ("utf-8"|"base64")
	MTime                  *int64                              `json:"mtime,omitempty"`                  // For seed_read_state
	Settings               *map[string]interface{}             `json:"settings,omitempty"`               // For apply_flag_settings
	Description            string                              `json:"description,omitempty"`            // For submit_feedback
	Surface                string                              `json:"surface,omitempty"`                // For submit_feedback
	TaskID                 string                              `json:"task_id,omitempty"`                // For stop_task
	ServerName             string                              `json:"server_name,omitempty"`            // For mcp_message (snake_case)
	MCPServerName          string                              `json:"serverName,omitempty"`             // For mcp_reconnect/mcp_toggle/mcp_set_servers (camelCase)
	Enabled                *bool                               `json:"enabled,omitempty"`                // For mcp_toggle (pointer so explicit false serializes)
	Servers                *map[string]MCPServerConfig         `json:"servers,omitempty"`                // For mcp_set_servers (pointer so nil/empty round-trips as {})
	Message                map[string]interface{}              `json:"message,omitempty"`                // For mcp_message (JSONRPC)
}

// SDKHookCallbackMatcher defines hook callback matching configuration.
type SDKHookCallbackMatcher struct {
	Matcher         string   `json:"matcher,omitempty"`
	HookCallbackIDs []string `json:"hookCallbackIds"`
	Timeout         int      `json:"timeout,omitempty"` // Timeout in seconds
}

// MessageType implements Message.
func (m SDKControlRequest) MessageType() string { return "control_request" }

// SDKControlResponse represents a control protocol response from CLI to SDK.
//
// Control responses correlate to requests via RequestID and contain either
// a result payload or an error.
type SDKControlResponse struct {
	Type     string                 `json:"type"`     // Always "control_response"
	Response SDKControlResponseBody `json:"response"` // Nested response payload
}

// SDKControlResponseBody contains the actual response data.
type SDKControlResponseBody struct {
	Subtype                   string                 `json:"subtype"`                               // "success" or "error"
	RequestID                 string                 `json:"request_id"`                            // Correlates to request
	Response                  map[string]interface{} `json:"response,omitempty"`                    // Success response data
	Error                     string                 `json:"error,omitempty"`                       // Error message
	PendingPermissionRequests []SDKControlRequest    `json:"pending_permission_requests,omitempty"` // Pending requests
}

// MessageType implements Message.
func (m SDKControlResponse) MessageType() string { return "control_response" }

// SDKControlCancelRequest cancels a pending control request.
type SDKControlCancelRequest struct {
	Type      string `json:"type"`       // Always "control_cancel_request"
	RequestID string `json:"request_id"` // Request to cancel
}

// MessageType implements Message.
func (m SDKControlCancelRequest) MessageType() string { return "control_cancel_request" }

// KeepAliveMessage is a heartbeat message.
type KeepAliveMessage struct {
	Type string `json:"type"` // Always "keep_alive"
}

// MessageType implements Message.
func (m KeepAliveMessage) MessageType() string { return "keep_alive" }

// ToolProgressMessage reports tool execution progress.
type ToolProgressMessage struct {
	Type               string  `json:"type"`                 // Always "tool_progress"
	ToolUseID          string  `json:"tool_use_id"`          // Tool invocation ID
	ToolName           string  `json:"tool_name"`            // Tool name
	ParentToolUseID    *string `json:"parent_tool_use_id"`   // Parent tool if nested
	ElapsedTimeSeconds float64 `json:"elapsed_time_seconds"` // Time elapsed
	TaskID             *string `json:"task_id,omitempty"`    // Task ID if associated with a task
	UUID               string  `json:"uuid"`                 // Message UUID
	SessionID          string  `json:"session_id"`           // Session ID
}

// MessageType implements Message.
func (m ToolProgressMessage) MessageType() string { return "tool_progress" }

// ToolUseSummaryMessage reports a summary of preceding tool uses.
type ToolUseSummaryMessage struct {
	Type                string   `json:"type"`                   // Always "tool_use_summary"
	Summary             string   `json:"summary"`                // Summary text
	PrecedingToolUseIDs []string `json:"preceding_tool_use_ids"` // Tool use IDs summarized
	UUID                string   `json:"uuid"`                   // Message UUID
	SessionID           string   `json:"session_id"`             // Session ID
}

// MessageType implements Message.
func (m ToolUseSummaryMessage) MessageType() string { return "tool_use_summary" }

// PromptSuggestionMessage contains a predicted next user prompt.
type PromptSuggestionMessage struct {
	Type       string `json:"type"`       // Always "prompt_suggestion"
	Suggestion string `json:"suggestion"` // Suggested prompt text
	UUID       string `json:"uuid"`       // Message UUID
	SessionID  string `json:"session_id"` // Session ID
}

// MessageType implements Message.
func (m PromptSuggestionMessage) MessageType() string { return "prompt_suggestion" }

// RateLimitEventMessage reports rate limit information changes.
type RateLimitEventMessage struct {
	Type          string        `json:"type"`            // Always "rate_limit_event"
	RateLimitInfo RateLimitInfo `json:"rate_limit_info"` // Rate limit details
	UUID          string        `json:"uuid"`            // Message UUID
	SessionID     string        `json:"session_id"`      // Session ID
}

// MessageType implements Message.
func (m RateLimitEventMessage) MessageType() string { return "rate_limit_event" }

// RateLimitStatus is the status of a rate limit check.
type RateLimitStatus string

const (
	RateLimitStatusAllowed        RateLimitStatus = "allowed"
	RateLimitStatusAllowedWarning RateLimitStatus = "allowed_warning"
	RateLimitStatusRejected       RateLimitStatus = "rejected"
)

// TerminalReason explains why a result message reached a terminal state.
type TerminalReason string

const (
	TerminalReasonBlockingLimit      TerminalReason = "blocking_limit"
	TerminalReasonRapidRefillBreaker TerminalReason = "rapid_refill_breaker"
	TerminalReasonPromptTooLong      TerminalReason = "prompt_too_long"
	TerminalReasonImageError         TerminalReason = "image_error"
	TerminalReasonModelError         TerminalReason = "model_error"
	TerminalReasonAbortedStreaming   TerminalReason = "aborted_streaming"
	TerminalReasonAbortedTools       TerminalReason = "aborted_tools"
	TerminalReasonStopHookPrevented  TerminalReason = "stop_hook_prevented"
	TerminalReasonHookStopped        TerminalReason = "hook_stopped"
	TerminalReasonToolDeferred       TerminalReason = "tool_deferred"
	TerminalReasonMaxTurns           TerminalReason = "max_turns"
	TerminalReasonCompleted          TerminalReason = "completed"
)

// FastModeState is the current state of Claude Code fast mode.
type FastModeState string

const (
	FastModeStateOff      FastModeState = "off"
	FastModeStateCooldown FastModeState = "cooldown"
	FastModeStateOn       FastModeState = "on"
)

// RateLimitType identifies the quota window or overage bucket.
type RateLimitType string

const (
	RateLimitTypeFiveHour       RateLimitType = "five_hour"
	RateLimitTypeSevenDay       RateLimitType = "seven_day"
	RateLimitTypeSevenDayOpus   RateLimitType = "seven_day_opus"
	RateLimitTypeSevenDaySonnet RateLimitType = "seven_day_sonnet"
	RateLimitTypeOverage        RateLimitType = "overage"
)

// RateLimitOverageDisabledReason explains why overage is unavailable.
type RateLimitOverageDisabledReason string

const (
	RateLimitOverageDisabledReasonOverageNotProvisioned   RateLimitOverageDisabledReason = "overage_not_provisioned"
	RateLimitOverageDisabledReasonOrgLevelDisabled        RateLimitOverageDisabledReason = "org_level_disabled"
	RateLimitOverageDisabledReasonOrgLevelDisabledUntil   RateLimitOverageDisabledReason = "org_level_disabled_until"
	RateLimitOverageDisabledReasonOutOfCredits            RateLimitOverageDisabledReason = "out_of_credits"
	RateLimitOverageDisabledReasonSeatTierLevelDisabled   RateLimitOverageDisabledReason = "seat_tier_level_disabled"
	RateLimitOverageDisabledReasonMemberLevelDisabled     RateLimitOverageDisabledReason = "member_level_disabled"
	RateLimitOverageDisabledReasonSeatTierZeroCreditLimit RateLimitOverageDisabledReason = "seat_tier_zero_credit_limit" //nolint:gosec // G101: upstream enum literal, not a credential
	RateLimitOverageDisabledReasonGroupZeroCreditLimit    RateLimitOverageDisabledReason = "group_zero_credit_limit"     //nolint:gosec // G101: upstream enum literal, not a credential
	RateLimitOverageDisabledReasonMemberZeroCreditLimit   RateLimitOverageDisabledReason = "member_zero_credit_limit"    //nolint:gosec // G101: upstream enum literal, not a credential
	RateLimitOverageDisabledReasonOrgServiceLevelDisabled RateLimitOverageDisabledReason = "org_service_level_disabled"
	RateLimitOverageDisabledReasonNoLimitsConfigured      RateLimitOverageDisabledReason = "no_limits_configured"
	RateLimitOverageDisabledReasonFetchError              RateLimitOverageDisabledReason = "fetch_error"
	RateLimitOverageDisabledReasonUnknown                 RateLimitOverageDisabledReason = "unknown"
)

// RateLimitInfo contains rate limit details for claude.ai subscription users.
type RateLimitInfo struct {
	Status                RateLimitStatus                 `json:"status"`
	ResetsAt              *int64                          `json:"resetsAt,omitempty"`
	RateLimitType         *RateLimitType                  `json:"rateLimitType,omitempty"`
	Utilization           *float64                        `json:"utilization,omitempty"`
	OverageStatus         *RateLimitStatus                `json:"overageStatus,omitempty"`
	OverageResetsAt       *int64                          `json:"overageResetsAt,omitempty"`
	OverageDisabledReason *RateLimitOverageDisabledReason `json:"overageDisabledReason,omitempty"`
	IsUsingOverage        *bool                           `json:"isUsingOverage,omitempty"`
	SurpassedThreshold    *float64                        `json:"surpassedThreshold,omitempty"`
}

// AuthStatusMessage reports authentication status.
type AuthStatusMessage struct {
	Type             string   `json:"type"`             // Always "auth_status"
	IsAuthenticating bool     `json:"isAuthenticating"` // Whether auth is in progress
	Output           []string `json:"output"`           // Auth output messages
	Error            string   `json:"error,omitempty"`  // Error if any
	UUID             string   `json:"uuid"`             // Message UUID
	SessionID        string   `json:"session_id"`       // Session ID
}

// MessageType implements Message.
func (m AuthStatusMessage) MessageType() string { return "auth_status" }

// Legacy ControlRequest/ControlResponse kept for backward compatibility.
// These may be used internally but SDKControlRequest/SDKControlResponse
// should be used for CLI communication.

// ControlRequest represents a legacy control protocol request.
type ControlRequest struct {
	Type      string                 `json:"type"`              // "control"
	Subtype   string                 `json:"subtype"`           // Request subtype
	RequestID string                 `json:"requestId"`         // Unique request ID
	Payload   map[string]interface{} `json:"payload,omitempty"` // Request data
}

// MessageType implements Message.
func (m ControlRequest) MessageType() string { return "control" }

// ControlResponse represents a legacy control protocol response.
type ControlResponse struct {
	Type      string                 `json:"type"`             // "control"
	RequestID string                 `json:"requestId"`        // Correlates to request
	Result    map[string]interface{} `json:"result,omitempty"` // Response data
	Error     *ProtocolError         `json:"error,omitempty"`  // Error details
}

// MessageType implements Message.
func (m ControlResponse) MessageType() string { return "control" }

// ProtocolError represents an error in the control protocol.
type ProtocolError struct {
	Code    string `json:"code"`    // Error code
	Message string `json:"message"` // Human-readable message
}

// Usage tracks token consumption and cost for billing.
//
// Usage data appears in assistant messages (per-message) and result messages
// (cumulative). Token counts distinguish between input (prompt) and output
// (completion) tokens.
type Usage struct {
	InputTokens  int     `json:"input_tokens"`  // Prompt tokens
	OutputTokens int     `json:"output_tokens"` // Completion tokens
	TotalTokens  int     `json:"total_tokens"`  // Sum of input + output
	Cost         float64 `json:"cost"`          // Estimated cost in USD
}

// SystemMessage represents the initialization message from Claude Code.
//
// This message is sent at the start of a session and contains information
// about available tools, MCP servers, models, and permissions.
type SystemMessage struct {
	Type              string          `json:"type"`                      // Always "system"
	Subtype           string          `json:"subtype"`                   // "init" or "compact_boundary"
	UUID              string          `json:"uuid"`                      // Unique message ID
	SessionID         string          `json:"session_id"`                // Session identifier
	APIKeySource      string          `json:"apiKeySource"`              // Where the API key comes from
	Cwd               string          `json:"cwd"`                       // Current working directory
	Tools             []string        `json:"tools"`                     // Available tools
	MCPServers        []MCPServerInfo `json:"mcp_servers"`               // MCP server status
	Model             string          `json:"model"`                     // Active model
	PermissionMode    PermissionMode  `json:"permissionMode"`            // Current permission mode
	SlashCommands     []string        `json:"slash_commands"`            // Available slash commands
	OutputStyle       string          `json:"output_style"`              // Output formatting style
	ClaudeCodeVersion string          `json:"claude_code_version"`       // Claude Code version
	Skills            []string        `json:"skills"`                    // Available skills
	Plugins           []SystemPlugin  `json:"plugins"`                   // Available plugins
	Agents            []string        `json:"agents,omitempty"`          // Available agents
	Betas             []string        `json:"betas,omitempty"`           // Enabled beta flags
	FastModeState     *FastModeState  `json:"fast_mode_state,omitempty"` // Fast mode state
}

// MessageType implements Message.
func (m SystemMessage) MessageType() string { return "system" }

// MCPServerInfo contains status information about an MCP server.
type MCPServerInfo struct {
	Name   string `json:"name"`   // Server name
	Status string `json:"status"` // Connection status
}

// SystemPlugin contains metadata for an installed Claude Code plugin.
type SystemPlugin struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// PartialAssistantMessage represents a streaming partial message.
//
// These messages are only emitted when IncludePartialMessages is true in Options.
// They contain raw streaming events for real-time display.
type PartialAssistantMessage struct {
	Type  string          `json:"type"`  // Always "stream_event"
	Event json.RawMessage `json:"event"` // Raw streaming event
	// ParentToolUseID is the parent tool_use ID when the partial event is
	// nested inside a subagent invocation (Task tool); nil for top-level
	// streaming. The wire key is always present (string or null).
	ParentToolUseID *string `json:"parent_tool_use_id"`
	UUID            string  `json:"uuid"`       // Unique message ID
	SessionID       string  `json:"session_id"` // Session identifier
}

// MessageType implements Message.
func (m PartialAssistantMessage) MessageType() string { return "stream_event" }

// CompactBoundaryMessage marks a context compaction boundary.
//
// This message is emitted when context compaction occurs, either manually
// or automatically when approaching context limits.
type CompactBoundaryMessage struct {
	Type            string          `json:"type"`             // Always "system"
	Subtype         string          `json:"subtype"`          // "compact_boundary"
	UUID            string          `json:"uuid"`             // Unique message ID
	SessionID       string          `json:"session_id"`       // Session identifier
	CompactMetadata CompactMetadata `json:"compact_metadata"` // Compaction details
}

// MessageType implements Message.
func (m CompactBoundaryMessage) MessageType() string { return "system" }

// HookOutcome describes the terminal status of a hook execution.
type HookOutcome string

const (
	HookOutcomeSuccess  HookOutcome = "success"
	HookOutcomeError    HookOutcome = "error"
	HookOutcomeCanceled HookOutcome = "cancelled" //nolint:misspell // upstream wire format spelling
)

// HookStartedMessage reports that a hook has started executing.
type HookStartedMessage struct {
	Type      string `json:"type"`       // Always "system"
	Subtype   string `json:"subtype"`    // "hook_started"
	HookID    string `json:"hook_id"`    // Hook invocation ID
	HookName  string `json:"hook_name"`  // Hook name
	HookEvent string `json:"hook_event"` // Hook event name
	UUID      string `json:"uuid"`       // Unique message ID
	SessionID string `json:"session_id"` // Session identifier
}

// MessageType implements Message.
func (m HookStartedMessage) MessageType() string { return "system" }

// HookProgressMessage reports intermediate hook execution output.
type HookProgressMessage struct {
	Type      string `json:"type"`       // Always "system"
	Subtype   string `json:"subtype"`    // "hook_progress"
	HookID    string `json:"hook_id"`    // Hook invocation ID
	HookName  string `json:"hook_name"`  // Hook name
	HookEvent string `json:"hook_event"` // Hook event name
	Stdout    string `json:"stdout"`     // Standard output
	Stderr    string `json:"stderr"`     // Standard error
	Output    string `json:"output"`     // Combined output
	UUID      string `json:"uuid"`       // Unique message ID
	SessionID string `json:"session_id"` // Session identifier
}

// MessageType implements Message.
func (m HookProgressMessage) MessageType() string { return "system" }

// HookResponseMessage reports terminal hook execution output.
type HookResponseMessage struct {
	Type      string      `json:"type"`                // Always "system"
	Subtype   string      `json:"subtype"`             // "hook_response"
	HookID    string      `json:"hook_id"`             // Hook invocation ID
	HookName  string      `json:"hook_name"`           // Hook name
	HookEvent string      `json:"hook_event"`          // Hook event name
	Output    string      `json:"output"`              // Combined output
	Stdout    string      `json:"stdout"`              // Standard output
	Stderr    string      `json:"stderr"`              // Standard error
	ExitCode  *int        `json:"exit_code,omitempty"` // Process exit code
	Outcome   HookOutcome `json:"outcome"`             // Terminal status
	UUID      string      `json:"uuid"`                // Unique message ID
	SessionID string      `json:"session_id"`          // Session identifier
}

// MessageType implements Message.
func (m HookResponseMessage) MessageType() string { return "system" }

// TaskUsage summarizes resource consumption for a task lifecycle event.
type TaskUsage struct {
	TotalTokens int `json:"total_tokens"`
	ToolUses    int `json:"tool_uses"`
	DurationMS  int `json:"duration_ms"`
}

// TaskNotificationStatus is the terminal status reported on task_notification.
type TaskNotificationStatus string

const (
	TaskNotificationStatusCompleted TaskNotificationStatus = "completed"
	TaskNotificationStatusFailed    TaskNotificationStatus = "failed"
	TaskNotificationStatusStopped   TaskNotificationStatus = "stopped"
)

// TaskRunStatus is the running status carried in TaskUpdatePatch.Status.
//
// Distinct from TaskNotificationStatus: the notification set is a subset oriented
// around terminal outcomes; this enum covers the full run lifecycle.
type TaskRunStatus string

const (
	TaskRunStatusPending   TaskRunStatus = "pending"
	TaskRunStatusRunning   TaskRunStatus = "running"
	TaskRunStatusCompleted TaskRunStatus = "completed"
	TaskRunStatusFailed    TaskRunStatus = "failed"
	TaskRunStatusKilled    TaskRunStatus = "killed"
	TaskRunStatusPaused    TaskRunStatus = "paused"
)

// TaskStartedMessage reports that a task has started executing.
type TaskStartedMessage struct {
	Type           string `json:"type"`                      // Always "system"
	Subtype        string `json:"subtype"`                   // "task_started"
	TaskID         string `json:"task_id"`                   // Task identifier
	ToolUseID      string `json:"tool_use_id,omitempty"`     // Related tool use ID
	Description    string `json:"description"`               // Task description
	SubagentType   string `json:"subagent_type,omitempty"`   // Subagent type for Task tool subagents
	TaskType       string `json:"task_type,omitempty"`       // Task type
	WorkflowName   string `json:"workflow_name,omitempty"`   // Workflow script metadata name
	Prompt         string `json:"prompt,omitempty"`          // Task prompt
	SkipTranscript *bool  `json:"skip_transcript,omitempty"` // Ambient task marker
	UUID           string `json:"uuid"`                      // Unique message ID
	SessionID      string `json:"session_id"`                // Session identifier
}

// MessageType implements Message.
func (m TaskStartedMessage) MessageType() string { return "system" }

// TaskProgressMessage reports intermediate task progress and usage.
type TaskProgressMessage struct {
	Type         string    `json:"type"`                     // Always "system"
	Subtype      string    `json:"subtype"`                  // "task_progress"
	TaskID       string    `json:"task_id"`                  // Task identifier
	ToolUseID    string    `json:"tool_use_id,omitempty"`    // Related tool use ID
	Description  string    `json:"description"`              // Task description
	SubagentType string    `json:"subagent_type,omitempty"`  // Subagent type for Task tool subagents
	Usage        TaskUsage `json:"usage"`                    // Resource consumption
	LastToolName string    `json:"last_tool_name,omitempty"` // Last tool used by task
	Summary      string    `json:"summary,omitempty"`        // Task progress summary
	UUID         string    `json:"uuid"`                     // Unique message ID
	SessionID    string    `json:"session_id"`               // Session identifier
}

// MessageType implements Message.
func (m TaskProgressMessage) MessageType() string { return "system" }

// TaskUpdatePatch is the wire-safe subset of TaskState fields delivered with task_updated.
//
// All fields are optional: senders include only what changed.
type TaskUpdatePatch struct {
	Status         TaskRunStatus `json:"status,omitempty"`
	Description    string        `json:"description,omitempty"`
	EndTime        *int64        `json:"end_time,omitempty"`
	TotalPausedMS  *int64        `json:"total_paused_ms,omitempty"`
	Error          string        `json:"error,omitempty"`
	IsBackgrounded *bool         `json:"is_backgrounded,omitempty"`
}

// TaskUpdatedMessage reports changes to task state.
type TaskUpdatedMessage struct {
	Type      string          `json:"type"`       // Always "system"
	Subtype   string          `json:"subtype"`    // "task_updated"
	TaskID    string          `json:"task_id"`    // Task identifier
	Patch     TaskUpdatePatch `json:"patch"`      // Updated task fields
	UUID      string          `json:"uuid"`       // Unique message ID
	SessionID string          `json:"session_id"` // Session identifier
}

// MessageType implements Message.
func (m TaskUpdatedMessage) MessageType() string { return "system" }

// TaskNotificationMessage reports terminal task output.
type TaskNotificationMessage struct {
	Type           string                 `json:"type"`                      // Always "system"
	Subtype        string                 `json:"subtype"`                   // "task_notification"
	TaskID         string                 `json:"task_id"`                   // Task identifier
	ToolUseID      string                 `json:"tool_use_id,omitempty"`     // Related tool use ID
	Status         TaskNotificationStatus `json:"status"`                    // Terminal status
	OutputFile     string                 `json:"output_file"`               // Output file path
	Summary        string                 `json:"summary"`                   // Task summary
	Usage          *TaskUsage             `json:"usage,omitempty"`           // Resource consumption
	SkipTranscript *bool                  `json:"skip_transcript,omitempty"` // Ambient task marker
	UUID           string                 `json:"uuid"`                      // Unique message ID
	SessionID      string                 `json:"session_id"`                // Session identifier
}

// MessageType implements Message.
func (m TaskNotificationMessage) MessageType() string { return "system" }

// Misc system subtype messages.

// APIRetryError is the retryable assistant-message error category.
type APIRetryError string

const (
	APIRetryErrorAuthenticationFailed APIRetryError = "authentication_failed"
	APIRetryErrorBillingError         APIRetryError = "billing_error"
	APIRetryErrorRateLimit            APIRetryError = "rate_limit"
	APIRetryErrorInvalidRequest       APIRetryError = "invalid_request"
	APIRetryErrorServerError          APIRetryError = "server_error"
	APIRetryErrorUnknown              APIRetryError = "unknown"
	APIRetryErrorMaxOutputTokens      APIRetryError = "max_output_tokens"
)

// APIRetryMessage reports that a failed API request will be retried.
type APIRetryMessage struct {
	Type         string        `json:"type"`           // Always "system"
	Subtype      string        `json:"subtype"`        // "api_retry"
	Attempt      int           `json:"attempt"`        // Current retry attempt
	MaxRetries   int           `json:"max_retries"`    // Maximum retry attempts
	RetryDelayMS int           `json:"retry_delay_ms"` // Delay before retry
	ErrorStatus  *int          `json:"error_status"`   // HTTP status, nil represents JSON null (connection errors)
	Error        APIRetryError `json:"error"`          // Retryable error category
	UUID         string        `json:"uuid"`           // Unique message ID
	SessionID    string        `json:"session_id"`     // Session identifier
}

// MessageType implements Message.
func (m APIRetryMessage) MessageType() string { return "system" }

// ElicitationCompleteMessage reports completion of a URL-mode MCP elicitation.
type ElicitationCompleteMessage struct {
	Type          string `json:"type"`            // Always "system"
	Subtype       string `json:"subtype"`         // "elicitation_complete"
	MCPServerName string `json:"mcp_server_name"` // MCP server name
	ElicitationID string `json:"elicitation_id"`  // Elicitation identifier
	UUID          string `json:"uuid"`            // Unique message ID
	SessionID     string `json:"session_id"`      // Session identifier
}

// MessageType implements Message.
func (m ElicitationCompleteMessage) MessageType() string { return "system" }

// FilesPersistedSuccess describes a successfully persisted file.
type FilesPersistedSuccess struct {
	Filename string `json:"filename"`
	FileID   string `json:"file_id"`
}

// FilesPersistedFailure describes a file that failed to persist.
type FilesPersistedFailure struct {
	Filename string `json:"filename"`
	Error    string `json:"error"`
}

// FilesPersistedEvent reports persisted files and per-file failures.
type FilesPersistedEvent struct {
	Type        string                  `json:"type"`         // Always "system"
	Subtype     string                  `json:"subtype"`      // "files_persisted"
	Files       []FilesPersistedSuccess `json:"files"`        // Persisted files
	Failed      []FilesPersistedFailure `json:"failed"`       // Failed files
	ProcessedAt string                  `json:"processed_at"` // Processing timestamp
	UUID        string                  `json:"uuid"`         // Unique message ID
	SessionID   string                  `json:"session_id"`   // Session identifier
}

// MessageType implements Message.
func (m FilesPersistedEvent) MessageType() string { return "system" }

// LocalCommandOutputMessage reports output from a local slash command.
type LocalCommandOutputMessage struct {
	Type      string `json:"type"`       // Always "system"
	Subtype   string `json:"subtype"`    // "local_command_output"
	Content   string `json:"content"`    // Command output
	UUID      string `json:"uuid"`       // Unique message ID
	SessionID string `json:"session_id"` // Session identifier
}

// MessageType implements Message.
func (m LocalCommandOutputMessage) MessageType() string { return "system" }

// MemoryRecallMode describes how memories were surfaced.
type MemoryRecallMode string

const (
	MemoryRecallModeSelect     MemoryRecallMode = "select"
	MemoryRecallModeSynthesize MemoryRecallMode = "synthesize"
)

// MemoryScope identifies the source scope for a recalled memory.
type MemoryScope string

const (
	MemoryScopePersonal     MemoryScope = "personal"
	MemoryScopeTeam         MemoryScope = "team"
	MemoryScopeOrganization MemoryScope = "organization"
)

// MemoryRecallEntry describes one recalled memory or synthesis.
type MemoryRecallEntry struct {
	// Path is the absolute path to the memory file, a synthesis sentinel
	// of the form "<synthesis:DIR>" when mode is "synthesize", or an
	// https URL when scope is "organization".
	Path string `json:"path"`

	// Scope identifies the source of the memory.
	Scope MemoryScope `json:"scope"`

	// Content is the surfaced memory body. Always present for
	// "synthesize" mode and for "organization" scope (neither has an
	// on-disk path to lazy-load from); absent for file-backed "select"
	// entries.
	Content string `json:"content,omitempty"`
}

// MemoryRecallMessage reports memories surfaced into the current turn.
type MemoryRecallMessage struct {
	Type      string              `json:"type"`       // Always "system"
	Subtype   string              `json:"subtype"`    // "memory_recall"
	Mode      MemoryRecallMode    `json:"mode"`       // Recall mode
	Memories  []MemoryRecallEntry `json:"memories"`   // Recalled memories
	UUID      string              `json:"uuid"`       // Unique message ID
	SessionID string              `json:"session_id"` // Session identifier
}

// MessageType implements Message.
func (m MemoryRecallMessage) MessageType() string { return "system" }

// MirrorErrorKey identifies the transcript mirror batch that failed.
type MirrorErrorKey struct {
	ProjectKey string `json:"projectKey"`
	SessionID  string `json:"sessionId"`
	Subpath    string `json:"subpath,omitempty"`
}

// MirrorErrorMessage reports a transcript mirror append failure.
type MirrorErrorMessage struct {
	Type      string         `json:"type"`       // Always "system"
	Subtype   string         `json:"subtype"`    // "mirror_error"
	Error     string         `json:"error"`      // Error text
	Key       MirrorErrorKey `json:"key"`        // Mirror batch key
	UUID      string         `json:"uuid"`       // Unique message ID
	SessionID string         `json:"session_id"` // Session identifier
}

// MessageType implements Message.
func (m MirrorErrorMessage) MessageType() string { return "system" }

// NotificationPriority is the display urgency for a loop-side notification.
type NotificationPriority string

const (
	NotificationPriorityLow       NotificationPriority = "low"
	NotificationPriorityMedium    NotificationPriority = "medium"
	NotificationPriorityHigh      NotificationPriority = "high"
	NotificationPriorityImmediate NotificationPriority = "immediate"
)

// NotificationMessage reports a loop-side text notification.
type NotificationMessage struct {
	Type      string               `json:"type"`                 // Always "system"
	Subtype   string               `json:"subtype"`              // "notification"
	Key       string               `json:"key"`                  // Notification key
	Text      string               `json:"text"`                 // Notification text
	Priority  NotificationPriority `json:"priority"`             // Display urgency
	Color     string               `json:"color,omitempty"`      // Optional display color
	TimeoutMS *int                 `json:"timeout_ms,omitempty"` // Optional timeout
	UUID      string               `json:"uuid"`                 // Unique message ID
	SessionID string               `json:"session_id"`           // Session identifier
}

// MessageType implements Message.
func (m NotificationMessage) MessageType() string { return "system" }

// PermissionDeniedMessage reports a tool call denied by the permission
// machinery. The SDK emits it for every denial path (mode, rule,
// classifier, async-agent) so consumers can observe denials without
// parsing the rejection text out of the assistant turn.
type PermissionDeniedMessage struct {
	Type               string `json:"type"`                           // Always "system"
	Subtype            string `json:"subtype"`                        // "permission_denied"
	ToolName           string `json:"tool_name"`                      // Denied tool name
	ToolUseID          string `json:"tool_use_id"`                    // Denied tool_use_id
	AgentID            string `json:"agent_id,omitempty"`             // Subagent ID when denial originated in a subagent
	DecisionReasonType string `json:"decision_reason_type,omitempty"` // Discriminator from PermissionDecisionReason (e.g. "classifier", "asyncAgent", "mode", "rule")
	DecisionReason     string `json:"decision_reason,omitempty"`      // Human-readable reason from the deciding component
	Message            string `json:"message"`                        // Rejection message returned to the model in tool_result
	UUID               string `json:"uuid"`                           // Unique message ID
	SessionID          string `json:"session_id"`                     // Session identifier
}

// MessageType implements Message.
func (m PermissionDeniedMessage) MessageType() string { return "system" }

// PluginInstallStatus is the plugin installation progress state.
type PluginInstallStatus string

const (
	PluginInstallStatusStarted   PluginInstallStatus = "started"
	PluginInstallStatusInstalled PluginInstallStatus = "installed"
	PluginInstallStatusFailed    PluginInstallStatus = "failed"
	PluginInstallStatusCompleted PluginInstallStatus = "completed"
)

// PluginInstallMessage reports headless plugin installation progress.
type PluginInstallMessage struct {
	Type      string              `json:"type"`            // Always "system"
	Subtype   string              `json:"subtype"`         // "plugin_install"
	Status    PluginInstallStatus `json:"status"`          // Installation status
	Name      string              `json:"name,omitempty"`  // Marketplace or plugin name
	Error     string              `json:"error,omitempty"` // Failure text
	UUID      string              `json:"uuid"`            // Unique message ID
	SessionID string              `json:"session_id"`      // Session identifier
}

// MessageType implements Message.
func (m PluginInstallMessage) MessageType() string { return "system" }

// SessionState is the current session run state.
type SessionState string

const (
	SessionStateIdle           SessionState = "idle"
	SessionStateRunning        SessionState = "running"
	SessionStateRequiresAction SessionState = "requires_action"
)

// SessionStateChangedMessage reports authoritative turn-over state changes.
type SessionStateChangedMessage struct {
	Type      string       `json:"type"`       // Always "system"
	Subtype   string       `json:"subtype"`    // "session_state_changed"
	State     SessionState `json:"state"`      // Session state
	UUID      string       `json:"uuid"`       // Unique message ID
	SessionID string       `json:"session_id"` // Session identifier
}

// MessageType implements Message.
func (m SessionStateChangedMessage) MessageType() string { return "system" }

// SDKStatusValue is the non-null status payload for a status message.
type SDKStatusValue string

const (
	SDKStatusCompacting SDKStatusValue = "compacting"
	SDKStatusRequesting SDKStatusValue = "requesting"
)

// CompactResult is the terminal compaction result carried by status messages.
type CompactResult string

const (
	CompactResultSuccess CompactResult = "success"
	CompactResultFailed  CompactResult = "failed"
)

// StatusMessage reports current SDK status.
type StatusMessage struct {
	Type           string          `json:"type"`                     // Always "system"
	Subtype        string          `json:"subtype"`                  // "status"
	Status         *SDKStatusValue `json:"status"`                   // Nil represents JSON null
	PermissionMode PermissionMode  `json:"permissionMode,omitempty"` // Optional permission mode
	CompactResult  CompactResult   `json:"compact_result,omitempty"` // Optional compact result
	CompactError   string          `json:"compact_error,omitempty"`  // Optional compact error
	UUID           string          `json:"uuid"`                     // Unique message ID
	SessionID      string          `json:"session_id"`               // Session identifier
}

// MessageType implements Message.
func (m StatusMessage) MessageType() string { return "system" }

// PreservedMessages identifies the ordered list of messagesToKeep UUIDs
// that survived a compaction.
//
// Supersedes preserved_segment when present.
type PreservedMessages struct {
	AnchorUUID string   `json:"anchor_uuid"` // Anchor message UUID
	UUIDs      []string `json:"uuids"`       // Ordered messagesToKeep UUIDs
}

// CompactMetadata contains details about a compaction event.
type CompactMetadata struct {
	Trigger           string             `json:"trigger"`                      // "manual" or "auto"
	PreTokens         int                `json:"pre_tokens"`                   // Token count before compaction
	PreservedMessages *PreservedMessages `json:"preserved_messages,omitempty"` // Ordered messagesToKeep UUIDs
}

// PermissionDenial tracks a denied permission request.
type PermissionDenial struct {
	ToolName  string          `json:"tool_name"`  // Tool that was denied
	ToolInput json.RawMessage `json:"tool_input"` // Input that triggered denial
	Reason    string          `json:"reason"`     // Why permission was denied
}

// ModelUsage tracks usage statistics per model.
type ModelUsage struct {
	InputTokens              int     `json:"inputTokens"`              // Prompt tokens
	OutputTokens             int     `json:"outputTokens"`             // Completion tokens
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`     // Cache read tokens
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"` // Cache creation tokens
	WebSearchRequests        int     `json:"webSearchRequests"`        // Web search count
	CostUSD                  float64 `json:"costUSD"`                  // Cost in USD
	ContextWindow            int     `json:"contextWindow"`            // Context window size
	MaxOutputTokens          int     `json:"maxOutputTokens"`          // Max output tokens for this model
}

// NonNullableUsage is like Usage but all fields are guaranteed non-zero.
type NonNullableUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// ParseMessage parses a JSON message into the appropriate Message type.
//
// This function inspects the "type" field to determine the concrete type
// and unmarshals accordingly. Unknown types return an error.
func ParseMessage(data []byte) (Message, error) {
	// First, peek at the type field
	var typeOnly struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &typeOnly); err != nil {
		return nil, err
	}

	// Unmarshal into the appropriate concrete type
	switch typeOnly.Type {
	case "user":
		var msg UserMessage
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "assistant":
		var msg AssistantMessage
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "result":
		var msg ResultMessage
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "stream_event":
		// Could be StreamEvent or PartialAssistantMessage
		var partial PartialAssistantMessage
		if err := json.Unmarshal(data, &partial); err == nil && partial.UUID != "" {
			return partial, nil
		}
		var msg StreamEvent
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "system":
		// System messages have subtypes: "init", "compact_boundary", and lifecycle events.
		var base struct {
			Subtype string `json:"subtype"`
		}
		if err := json.Unmarshal(data, &base); err != nil {
			return nil, err
		}

		switch base.Subtype {
		case "compact_boundary":
			var msg CompactBoundaryMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "hook_started":
			var msg HookStartedMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "hook_progress":
			var msg HookProgressMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "hook_response":
			var msg HookResponseMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "task_started":
			var msg TaskStartedMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "task_progress":
			var msg TaskProgressMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "task_updated":
			var msg TaskUpdatedMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "task_notification":
			var msg TaskNotificationMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "api_retry":
			var msg APIRetryMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "elicitation_complete":
			var msg ElicitationCompleteMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "files_persisted":
			var msg FilesPersistedEvent
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "local_command_output":
			var msg LocalCommandOutputMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "memory_recall":
			var msg MemoryRecallMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "mirror_error":
			var msg MirrorErrorMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "notification":
			var msg NotificationMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "permission_denied":
			var msg PermissionDeniedMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "plugin_install":
			var msg PluginInstallMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "session_state_changed":
			var msg SessionStateChangedMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		case "status":
			var msg StatusMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		default:
			// Includes "init" and any forward-compatible unknown subtypes.
			var msg SystemMessage
			err := json.Unmarshal(data, &msg)
			return msg, err
		}

	case "todo_update":
		var msg TodoUpdateMessage
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "subagent_result":
		var msg SubagentResultMessage
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "control_request":
		var msg SDKControlRequest
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "control_response":
		var msg SDKControlResponse
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "control_cancel_request":
		var msg SDKControlCancelRequest
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "keep_alive":
		return KeepAliveMessage{Type: "keep_alive"}, nil

	case "tool_progress":
		var msg ToolProgressMessage
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "tool_use_summary":
		var msg ToolUseSummaryMessage
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "prompt_suggestion":
		var msg PromptSuggestionMessage
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "rate_limit_event":
		var msg RateLimitEventMessage
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "auth_status":
		var msg AuthStatusMessage
		err := json.Unmarshal(data, &msg)
		return msg, err

	case "control":
		// Legacy control messages - determine which by checking for requestId or result
		var base struct {
			Subtype   string          `json:"subtype,omitempty"`
			RequestID string          `json:"requestId"`
			Result    json.RawMessage `json:"result,omitempty"`
		}
		if err := json.Unmarshal(data, &base); err != nil {
			return nil, err
		}

		// If it has a subtype, it's a request
		if base.Subtype != "" {
			var msg ControlRequest
			err := json.Unmarshal(data, &msg)
			return msg, err
		}

		// Otherwise it's a response
		var msg ControlResponse
		err := json.Unmarshal(data, &msg)
		return msg, err

	default:
		return nil, &ErrUnknownMessageType{Type: typeOnly.Type}
	}
}
