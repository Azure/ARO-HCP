package claudeagent

// SlashCommand represents an available slash command.
type SlashCommand struct {
	Name         string `json:"name"`         // Command name (without slash)
	Description  string `json:"description"`  // Command description
	ArgumentHint string `json:"argumentHint"` // Hint for command arguments
}

// ModelInfo contains information about an available model.
type ModelInfo struct {
	Value       string `json:"value"`       // Model ID to use in API calls
	DisplayName string `json:"displayName"` // Human-readable model name
	Description string `json:"description"` // Model capabilities description
}

// AgentInfo describes a subagent available to the Task tool.
type AgentInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Model       string `json:"model,omitempty"`
}

// SDKControlInitializeResponse is the parsed initialize control response.
type SDKControlInitializeResponse struct {
	Commands              []SlashCommand `json:"commands"`
	Agents                []AgentInfo    `json:"agents"`
	OutputStyle           string         `json:"output_style"`
	AvailableOutputStyles []string       `json:"available_output_styles"`
	Models                []ModelInfo    `json:"models"`
	Account               AccountInfo    `json:"account"`
	FastModeState         string         `json:"fast_mode_state,omitempty"`
}

// McpServerStatus reports the connection status of an MCP server.
type McpServerStatus struct {
	Name       string         `json:"name"`       // Server name
	Status     McpServerState `json:"status"`     // Connection state
	ServerInfo *McpServerInfo `json:"serverInfo"` // Server metadata (if connected)
}

// McpServerState represents MCP server connection states.
type McpServerState string

const (
	// McpServerStateConnected indicates successful connection.
	McpServerStateConnected McpServerState = "connected"
	// McpServerStateFailed indicates connection failure.
	McpServerStateFailed McpServerState = "failed"
	// McpServerStateNeedsAuth indicates authentication required.
	McpServerStateNeedsAuth McpServerState = "needs-auth"
	// McpServerStatePending indicates connection in progress.
	McpServerStatePending McpServerState = "pending"
)

// McpServerInfo contains metadata about a connected MCP server.
type McpServerInfo struct {
	Name    string `json:"name"`    // Server name
	Version string `json:"version"` // Server version
}

// McpSetServersResult is the response from Stream.SetMcpServers.
type McpSetServersResult struct {
	Added   []string          `json:"added"`
	Removed []string          `json:"removed"`
	Errors  map[string]string `json:"errors"`
}

// RewindFilesOptions controls a file checkpoint rewind.
type RewindFilesOptions struct {
	DryRun bool `json:"dryRun,omitempty"`
}

// RewindFilesResult is the response from Stream.RewindFiles.
type RewindFilesResult struct {
	CanRewind    bool     `json:"canRewind"`
	Error        string   `json:"error,omitempty"`
	FilesChanged []string `json:"filesChanged,omitempty"`
	Insertions   int      `json:"insertions,omitempty"`
	Deletions    int      `json:"deletions,omitempty"`
}

// ReadFileOptions controls Stream.ReadFile.
type ReadFileOptions struct {
	MaxBytes int `json:"maxBytes,omitempty"`
	// Encoding selects how the CLI encodes file bytes in the response.
	// Valid values are "utf-8" (default; lossy for binary) and "base64"
	// (required for arbitrary binary). Empty string lets the CLI pick its
	// default.
	Encoding string `json:"encoding,omitempty"`
}

// SDKControlReadFileResponse contains file contents from Stream.ReadFile.
type SDKControlReadFileResponse struct {
	Contents  string `json:"contents"`
	AbsPath   string `json:"absPath"`
	Truncated bool   `json:"truncated,omitempty"`
	// Encoding is set to "base64" when the CLI honored a base64 request.
	// Empty string means utf-8, including when older CLIs ignore the request.
	Encoding string `json:"encoding,omitempty"`
}

// SDKControlReloadPluginsResponse reports refreshed session components.
type SDKControlReloadPluginsResponse struct {
	Commands   []SlashCommand    `json:"commands"`
	Agents     []AgentInfo       `json:"agents"`
	Plugins    []PluginInfo      `json:"plugins"`
	McpServers []McpServerStatus `json:"mcpServers"`
	ErrorCount int               `json:"error_count"`
}

// PluginInfo describes a plugin loaded by the CLI.
type PluginInfo struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Source string `json:"source,omitempty"`
}

// APIProvider names the active CLI API backend. Anthropic OAuth login only
// applies for APIProviderFirstParty; for 3P providers the other AccountInfo
// fields are absent and auth is external (AWS creds, gcloud ADC, etc.).
// APIProviderGateway means the CLI is authenticated against an enterprise
// gateway.
type APIProvider string

const (
	APIProviderFirstParty   APIProvider = "firstParty"
	APIProviderBedrock      APIProvider = "bedrock"
	APIProviderVertex       APIProvider = "vertex"
	APIProviderFoundry      APIProvider = "foundry"
	APIProviderAnthropicAWS APIProvider = "anthropicAws"
	APIProviderMantle       APIProvider = "mantle"
	APIProviderGateway      APIProvider = "gateway"
)

// AccountInfo contains user account information.
type AccountInfo struct {
	Email            string      `json:"email,omitempty"`            // User email
	Organization     string      `json:"organization,omitempty"`     // Organization name
	SubscriptionType string      `json:"subscriptionType,omitempty"` // Subscription tier
	TokenSource      string      `json:"tokenSource,omitempty"`      // How token was obtained
	APIKeySource     string      `json:"apiKeySource,omitempty"`     // API key source
	APIProvider      APIProvider `json:"apiProvider,omitempty"`      // Active API backend
}

// SDKControlGetContextUsageResponse mirrors the context usage control response.
type SDKControlGetContextUsageResponse struct {
	Categories           []ContextUsageCategory        `json:"categories"`
	TotalTokens          int                           `json:"totalTokens"`
	MaxTokens            int                           `json:"maxTokens"`
	RawMaxTokens         int                           `json:"rawMaxTokens"`
	Percentage           float64                       `json:"percentage"`
	GridRows             [][]ContextUsageGridCell      `json:"gridRows"`
	Model                string                        `json:"model"`
	MemoryFiles          []ContextUsageMemoryFile      `json:"memoryFiles"`
	McpTools             []ContextUsageMcpTool         `json:"mcpTools"`
	DeferredBuiltinTools []ContextUsageBuiltinTool     `json:"deferredBuiltinTools,omitempty"`
	SystemTools          []ContextUsageSystemTool      `json:"systemTools,omitempty"`
	SystemPromptSections []ContextUsageSection         `json:"systemPromptSections,omitempty"`
	Agents               []ContextUsageAgent           `json:"agents"`
	SlashCommands        *ContextUsageSlashCommands    `json:"slashCommands,omitempty"`
	Skills               *ContextUsageSkills           `json:"skills,omitempty"`
	AutoCompactThreshold *float64                      `json:"autoCompactThreshold,omitempty"`
	IsAutoCompactEnabled bool                          `json:"isAutoCompactEnabled"`
	MessageBreakdown     *ContextUsageMessageBreakdown `json:"messageBreakdown,omitempty"`
	APIUsage             *ContextUsageAPIUsage         `json:"apiUsage"`
}

type ContextUsageCategory struct {
	Name       string `json:"name"`
	Tokens     int    `json:"tokens"`
	Color      string `json:"color"`
	IsDeferred bool   `json:"isDeferred,omitempty"`
}

type ContextUsageGridCell struct {
	Color          string  `json:"color"`
	IsFilled       bool    `json:"isFilled"`
	CategoryName   string  `json:"categoryName"`
	Tokens         int     `json:"tokens"`
	Percentage     float64 `json:"percentage"`
	SquareFullness float64 `json:"squareFullness"`
}

type ContextUsageMemoryFile struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Tokens int    `json:"tokens"`
}

type ContextUsageMcpTool struct {
	Name       string `json:"name"`
	ServerName string `json:"serverName"`
	Tokens     int    `json:"tokens"`
	IsLoaded   bool   `json:"isLoaded,omitempty"`
}

type ContextUsageBuiltinTool struct {
	Name     string `json:"name"`
	Tokens   int    `json:"tokens"`
	IsLoaded bool   `json:"isLoaded"`
}

type ContextUsageSystemTool struct {
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
}

type ContextUsageSection struct {
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
}

type ContextUsageAgent struct {
	AgentType string `json:"agentType"`
	Source    string `json:"source"`
	Tokens    int    `json:"tokens"`
}

type ContextUsageSlashCommands struct {
	TotalCommands    int `json:"totalCommands"`
	IncludedCommands int `json:"includedCommands"`
	Tokens           int `json:"tokens"`
}

type ContextUsageSkills struct {
	TotalSkills      int                       `json:"totalSkills"`
	IncludedSkills   int                       `json:"includedSkills"`
	Tokens           int                       `json:"tokens"`
	SkillFrontmatter []ContextUsageSkillSource `json:"skillFrontmatter"`
}

type ContextUsageSkillSource struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Tokens int    `json:"tokens"`
}

type ContextUsageMessageBreakdown struct {
	ToolCallTokens          int                        `json:"toolCallTokens"`
	ToolResultTokens        int                        `json:"toolResultTokens"`
	AttachmentTokens        int                        `json:"attachmentTokens"`
	AssistantMessageTokens  int                        `json:"assistantMessageTokens"`
	UserMessageTokens       int                        `json:"userMessageTokens"`
	RedirectedContextTokens int                        `json:"redirectedContextTokens"`
	UnattributedTokens      int                        `json:"unattributedTokens"`
	ToolCallsByType         []ContextUsageToolCallType `json:"toolCallsByType"`
	AttachmentsByType       []ContextUsageAttachment   `json:"attachmentsByType"`
}

type ContextUsageToolCallType struct {
	Name         string `json:"name"`
	CallTokens   int    `json:"callTokens"`
	ResultTokens int    `json:"resultTokens"`
}

type ContextUsageAttachment struct {
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
}

type ContextUsageAPIUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}
