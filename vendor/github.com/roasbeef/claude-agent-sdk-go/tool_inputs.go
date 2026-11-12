package claudeagent

// Tool input types for parsing tool calls from the SDK.
// These match the TypeScript SDK input types for common Claude Code tools.

// TaskInput is the input for the Task tool (subagent invocation).
type TaskInput struct {
	Description  string `json:"description"`
	Prompt       string `json:"prompt"`
	SubagentType string `json:"subagent_type"`
}

// BashInput is the input for the Bash tool.
type BashInput struct {
	Command         string `json:"command"`
	Timeout         *int   `json:"timeout,omitempty"`
	Description     string `json:"description,omitempty"`
	RunInBackground bool   `json:"run_in_background,omitempty"`
}

// FileEditInput is the input for the Edit tool.
type FileEditInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// FileReadInput is the input for the Read tool.
type FileReadInput struct {
	FilePath string `json:"file_path"`
	Offset   *int   `json:"offset,omitempty"`
	Limit    *int   `json:"limit,omitempty"`
}

// FileWriteInput is the input for the Write tool.
type FileWriteInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// GlobInput is the input for the Glob tool.
type GlobInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

// GrepInput is the input for the Grep tool.
type GrepInput struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	Glob            string `json:"glob,omitempty"`
	Type            string `json:"type,omitempty"`
	OutputMode      string `json:"output_mode,omitempty"` // "content", "files_with_matches", "count"
	CaseInsensitive bool   `json:"-i,omitempty"`
	ShowLineNumbers bool   `json:"-n,omitempty"`
	LinesBefore     *int   `json:"-B,omitempty"`
	LinesAfter      *int   `json:"-A,omitempty"`
	ContextLines    *int   `json:"-C,omitempty"`
	HeadLimit       *int   `json:"head_limit,omitempty"`
	Multiline       bool   `json:"multiline,omitempty"`
}

// LSPInput is the input for the LSP tool.
type LSPInput struct {
	Operation string `json:"operation"`
	FilePath  string `json:"filePath"`
	Line      int    `json:"line"`
	Character int    `json:"character"`
}

// WebFetchInput is the input for the WebFetch tool.
type WebFetchInput struct {
	URL    string `json:"url"`
	Prompt string `json:"prompt"`
}

// WebSearchInput is the input for the WebSearch tool.
type WebSearchInput struct {
	Query          string   `json:"query"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

// NotebookEditInput is the input for the NotebookEdit tool.
type NotebookEditInput struct {
	NotebookPath string `json:"notebook_path"`
	CellID       string `json:"cell_id,omitempty"`
	CellType     string `json:"cell_type,omitempty"` // "code" or "markdown"
	EditMode     string `json:"edit_mode,omitempty"` // "replace", "insert", "delete"
	NewSource    string `json:"new_source"`
}

// TodoWriteInput is the input for the TodoWrite tool.
type TodoWriteInput struct {
	Todos []TodoWriteItem `json:"todos"`
}

// TodoWriteItem is a single todo item for the TodoWrite tool.
type TodoWriteItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"` // "pending", "in_progress", "completed"
	ActiveForm string `json:"activeForm"`
}

// SkillInput is the input for the Skill tool.
type SkillInput struct {
	Skill string `json:"skill"`
	Args  string `json:"args,omitempty"`
}

// AskUserQuestionInput is the input for the AskUserQuestion tool.
//
// This tool allows Claude to ask the user one or more questions, optionally
// with multiple-choice options. The questions array contains 1-4 questions.
type AskUserQuestionInput struct {
	Questions []QuestionItem `json:"questions"`
}

// QuestionItem represents a single question in an AskUserQuestion request.
type QuestionItem struct {
	// Question is the full question text to display to the user.
	Question string `json:"question"`

	// Header is a short label displayed as a chip/tag (max 12 chars).
	// Example: "Auth method", "Library", "Approach".
	Header string `json:"header,omitempty"`

	// Options are the available choices for this question.
	// If empty, the question expects freeform text input.
	Options []QuestionOption `json:"options,omitempty"`

	// MultiSelect allows the user to select multiple options.
	MultiSelect bool `json:"multiSelect,omitempty"`
}

// QuestionOption represents a selectable option for a question.
type QuestionOption struct {
	// Label is the display text for this option (1-5 words).
	Label string `json:"label"`

	// Description explains what this option means or what happens if chosen.
	Description string `json:"description"`
}
