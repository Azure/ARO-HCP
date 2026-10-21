package claudeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"sync"
	"time"
)

// ErrNotInitialized indicates stream metadata was requested before SDK init.
var ErrNotInitialized = errors.New("sdk not initialized")

// Client is the high-level API for interacting with Claude Code CLI.
//
// Client manages the subprocess transport, control protocol, and provides
// ergonomic methods for querying and streaming interactions. It uses Go 1.23+
// iter.Seq for streaming message iteration.
type Client struct {
	options   Options
	transport Transport
	protocol  *Protocol
	skills    []Skill
	mu        sync.Mutex
	connected bool

	// Message routing.
	msgCh     chan Message
	msgCtx    context.Context
	msgCancel context.CancelFunc
}

// NewClient creates a new Claude agent client with the given options.
//
// The client is not connected until Connect() is called. Options are validated
// and merged with defaults.
//
// Example:
//
//	client, err := claudeagent.NewClient(
//	    claudeagent.WithSystemPrompt("You are a helpful assistant"),
//	    claudeagent.WithModel("claude-sonnet-4-5-20250929"),
//	)
func NewClient(opts ...Option) (*Client, error) {
	// Start with defaults
	options := DefaultOptions()

	// Apply options
	for _, opt := range opts {
		opt(&options)
	}

	// Validate configuration
	if err := validateOptions(&options); err != nil {
		return nil, err
	}

	client := &Client{
		options: options,
	}

	// Load Skills if enabled
	if options.SkillsConfig.EnableSkills {
		loader := NewSkillLoader(
			options.SkillsConfig.UserSkillsDir,
			options.SkillsConfig.ProjectSkillsDir,
		)
		skills, err := loader.Load()
		if err != nil {
			// Log warning but continue (Skills loading is not critical)
			// In production, use structured logging here
			_ = err
		}
		client.skills = skills
	}

	return client, nil
}

// Connect establishes connection to the Claude CLI subprocess.
//
// This spawns the CLI process and sets up communication pipes.
// Connect must be called before Query or Stream.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil // Already connected
	}

	var transport Transport
	if c.options.Transport != nil {
		transport = c.options.Transport
	} else {
		subprocess, err := NewSubprocessTransport(&c.options)
		if err != nil {
			return err
		}

		// Wire stderr callback to transport if configured. The Options.Stderr
		// callback receives each line as a string, while the transport expects
		// an io.Writer. The adapter bridges the two interfaces.
		if c.options.Stderr != nil {
			subprocess.SetStderrLogger(&stderrCallbackWriter{
				callback: c.options.Stderr,
			})
		}
		transport = subprocess
	}

	// Connect transport.
	if err := transport.Connect(ctx); err != nil {
		return err
	}
	c.transport = transport

	// Create protocol handler.
	c.protocol = NewProtocol(transport, &c.options)

	// Create message channel for routing.
	c.msgCh = make(chan Message, 64)
	c.msgCtx, c.msgCancel = context.WithCancel(context.Background())

	// Start message pump that routes all messages.
	go c.messagePump()

	// Small delay to ensure message pump is ready to receive.
	time.Sleep(50 * time.Millisecond)

	// Initialize the SDK control protocol.
	if err := c.protocol.Initialize(ctx); err != nil {
		c.msgCancel()
		transport.Close()
		return fmt.Errorf("failed to initialize: %w", err)
	}

	c.connected = true
	return nil
}

// messagePump reads from transport and routes messages.
func (c *Client) messagePump() {
	defer close(c.msgCh)
	for msg, err := range c.transport.ReadMessages(c.msgCtx) {
		if err != nil {
			continue
		}
		// Route control messages to protocol handler.
		if isControlMessage(msg) {
			_ = c.protocol.HandleControlMessage(c.msgCtx, msg)
			continue
		}
		// Send non-control messages to consumer channel.
		select {
		case c.msgCh <- msg:
		case <-c.msgCtx.Done():
			return
		}
	}
}

// Query performs a one-shot query and returns an iterator over response messages.
//
// The iterator yields messages as they arrive from Claude, including:
// - AssistantMessage: Text responses and tool requests
// - QuestionMessage: Questions from Claude (call Respond() to answer)
// - TodoUpdateMessage: Task tracking updates
// - SubagentResultMessage: Subagent outcomes
// - ResultMessage: Final completion status
//
// When Claude invokes the AskUserQuestion tool:
// - If WithAskUserQuestionHandler is configured, questions are handled automatically
// - Otherwise, a QuestionMessage is yielded. Call its Respond() method to answer.
//
// The iterator stops when the result message is received or the context is canceled.
//
// Example:
//
//	for msg := range client.Query(ctx, "Help me configure the project") {
//	    switch m := msg.(type) {
//	    case QuestionMessage:
//	        fmt.Println("Claude asks:", m.Questions[0].Question)
//	        m.Respond(m.Answer(0, "Yes"))
//	    case AssistantMessage:
//	        fmt.Println(m.ContentText())
//	    case ResultMessage:
//	        fmt.Printf("Done: %s\n", m.Status)
//	    }
//	}
func (c *Client) Query(ctx context.Context, prompt string) iter.Seq[Message] {
	return func(yield func(Message) bool) {
		// Ensure connected.
		if !c.connected {
			if err := c.Connect(ctx); err != nil {
				return
			}
		}

		// Send user message in TypeScript SDK format.
		userMsg := UserMessage{
			Type:      "user",
			SessionID: c.options.SessionOptions.SessionID,
			Message: APIUserMessage{
				Role: "user",
				Content: []UserContentBlock{
					{Type: "text", Text: prompt},
				},
			},
			ParentToolUseID: nil,
		}

		if err := c.protocol.SendMessage(ctx, userMsg); err != nil {
			return
		}

		// Read messages from channel until result.
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-c.msgCh:
				if !ok {
					return // Channel closed.
				}

				// Check for AskUserQuestion tool calls.
				if c.options.AskUserQuestionHandler != nil {
					// Handler configured - use callback API.
					if handled := c.handleAskUserQuestion(ctx, msg); handled {
						continue
					}
				} else {
					// No handler - yield QuestionMessage if present.
					if questionMsg := c.extractQuestionMessage(ctx, msg); questionMsg != nil {
						if !yield(*questionMsg) {
							return
						}
						continue
					}
				}

				// Yield message to consumer.
				if !yield(msg) {
					return
				}

				// Stop on result message.
				if _, ok := msg.(ResultMessage); ok {
					return
				}
			}
		}
	}
}

// extractQuestionMessage checks if the message contains an AskUserQuestion tool call
// and returns a QuestionMessage if found. Returns nil if no question is present.
//
// The responder uses context.Background() to ensure the answer can be sent even if
// the original query context has been canceled. This allows users to respond to
// questions asynchronously without worrying about context lifecycle.
func (c *Client) extractQuestionMessage(_ context.Context, msg Message) *QuestionMessage {
	assistant, ok := msg.(AssistantMessage)
	if !ok {
		return nil
	}

	for _, block := range assistant.Message.Content {
		if block.Type == "tool_use" && block.Name == "AskUserQuestion" {
			// Parse the question input.
			var input AskUserQuestionInput
			if err := json.Unmarshal(block.Input, &input); err != nil {
				continue
			}

			// Capture for closure.
			toolUseID := block.ID

			// Create QuestionMessage with embedded QuestionSet.
			// Use context.Background() for responder to allow async responses
			// even after the original query context is canceled.
			return &QuestionMessage{
				QuestionSet: QuestionSet{
					ToolUseID:       toolUseID,
					Questions:       input.Questions,
					SessionID:       c.options.SessionOptions.SessionID,
					ParentToolUseID: assistant.ParentToolUseID,
				},
				responder: func(answers Answers) error {
					return c.sendToolResult(context.Background(), toolUseID, answers)
				},
			}
		}
	}

	return nil
}

// handleAskUserQuestion checks if the message contains an AskUserQuestion tool call
// and handles it using the configured handler. Returns true if the message was handled.
func (c *Client) handleAskUserQuestion(ctx context.Context, msg Message) bool {
	assistant, ok := msg.(AssistantMessage)
	if !ok {
		return false
	}

	for _, block := range assistant.Message.Content {
		if block.Type == "tool_use" && block.Name == "AskUserQuestion" {
			// Parse the question input.
			var input AskUserQuestionInput
			if err := json.Unmarshal(block.Input, &input); err != nil {
				continue
			}

			// Create QuestionSet.
			qs := QuestionSet{
				ToolUseID:       block.ID,
				Questions:       input.Questions,
				SessionID:       c.options.SessionOptions.SessionID,
				ParentToolUseID: assistant.ParentToolUseID,
			}

			// Call the handler.
			answers, err := c.options.AskUserQuestionHandler(ctx, qs)
			if err != nil {
				// Send error back to Claude so conversation doesn't hang.
				// Ignore sendToolError result - we've already logged the original error.
				_ = c.sendToolError(ctx, block.ID, fmt.Sprintf("question handler error: %v", err))
				return true
			}

			// Send the tool result.
			if err := c.sendToolResult(ctx, block.ID, answers); err != nil {
				// Send error back to Claude so conversation doesn't hang.
				// Ignore sendToolError result - best effort notification.
				_ = c.sendToolError(ctx, block.ID, fmt.Sprintf("failed to send answer: %v", err))
				return true
			}

			return true
		}
	}

	return false
}

// Questions returns an iterator for interactive Q&A sessions.
//
// This method combines message streaming with question handling. When Claude
// invokes the AskUserQuestion tool, the iterator yields a QuestionSet and an
// AnswerFunc. Call the AnswerFunc with your answers to continue the conversation.
//
// The iterator yields (QuestionSet, AnswerFunc) pairs. Regular messages are
// processed internally but not yielded. Use Query() or Stream() if you need
// access to all messages.
//
// Example:
//
//	for qs, answer := range client.Questions(ctx, "Help me configure the project") {
//	    fmt.Printf("Claude asks: %s\n", qs.Questions[0].Question)
//
//	    // Use helper methods to construct answers
//	    if err := answer(qs.Answer(0, "Yes")); err != nil {
//	        log.Fatal(err)
//	    }
//	}
func (c *Client) Questions(ctx context.Context, prompt string) iter.Seq2[QuestionSet, AnswerFunc] {
	return func(yield func(QuestionSet, AnswerFunc) bool) {
		// Ensure connected.
		if !c.connected {
			if err := c.Connect(ctx); err != nil {
				return
			}
		}

		// Send user message.
		userMsg := UserMessage{
			Type:      "user",
			SessionID: c.options.SessionOptions.SessionID,
			Message: APIUserMessage{
				Role: "user",
				Content: []UserContentBlock{
					{Type: "text", Text: prompt},
				},
			},
			ParentToolUseID: nil,
		}

		if err := c.protocol.SendMessage(ctx, userMsg); err != nil {
			return
		}

		// Read messages from channel until result.
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-c.msgCh:
				if !ok {
					return // Channel closed.
				}

				// Check for AskUserQuestion tool calls in assistant messages.
				if assistant, ok := msg.(AssistantMessage); ok {
					for _, block := range assistant.Message.Content {
						if block.Type == "tool_use" && block.Name == "AskUserQuestion" {
							// Parse the question input.
							var input AskUserQuestionInput
							if err := json.Unmarshal(block.Input, &input); err != nil {
								continue
							}

							// Create QuestionSet.
							qs := QuestionSet{
								ToolUseID:       block.ID,
								Questions:       input.Questions,
								SessionID:       c.options.SessionOptions.SessionID,
								ParentToolUseID: assistant.ParentToolUseID,
							}

							// Create answer function that sends tool result.
							toolUseID := block.ID
							answerFunc := func(answers Answers) error {
								return c.sendToolResult(ctx, toolUseID, answers)
							}

							// Yield to consumer.
							if !yield(qs, answerFunc) {
								return
							}
						}
					}
				}

				// Stop on result message.
				if _, ok := msg.(ResultMessage); ok {
					return
				}
			}
		}
	}
}

// sendToolResult sends a tool result back to Claude.
func (c *Client) sendToolResult(ctx context.Context, toolUseID string, answers Answers) error {
	// Format answers in the expected structure.
	result := map[string]interface{}{
		"answers": answers,
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal answers: %w", err)
	}

	// Send as a user message with tool result.
	msg := UserMessage{
		Type:            "user",
		SessionID:       c.options.SessionOptions.SessionID,
		ParentToolUseID: &toolUseID,
		ToolUseResult:   json.RawMessage(resultJSON),
		Message: APIUserMessage{
			Role: "user",
			Content: []UserContentBlock{
				{Type: "tool_result", Text: string(resultJSON)},
			},
		},
	}

	return c.protocol.SendMessage(ctx, msg)
}

// sendToolError sends an error result back to Claude for a tool use.
// This prevents the conversation from hanging when question handling fails.
func (c *Client) sendToolError(ctx context.Context, toolUseID string, errMsg string) error {
	// Format error in the expected structure.
	result := map[string]interface{}{
		"error": errMsg,
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal error: %w", err)
	}

	// Send as a user message with tool result indicating error.
	msg := UserMessage{
		Type:            "user",
		SessionID:       c.options.SessionOptions.SessionID,
		ParentToolUseID: &toolUseID,
		ToolUseResult:   json.RawMessage(resultJSON),
		Message: APIUserMessage{
			Role: "user",
			Content: []UserContentBlock{
				{Type: "tool_result", Text: string(resultJSON)},
			},
		},
	}

	return c.protocol.SendMessage(ctx, msg)
}

// Stream returns a bidirectional stream for interactive conversations.
//
// Streams allow multiple rounds of user prompts and assistant responses
// within a single session. Use Send() to submit prompts and range over
// Messages() to receive responses.
//
// Example:
//
//	stream, err := client.Stream(ctx)
//	defer stream.Close()
//
//	stream.Send(ctx, "What's the weather?")
//	for msg := range stream.Messages() {
//	    // Process messages
//	}
func (c *Client) Stream(ctx context.Context) (*Stream, error) {
	// Ensure connected
	if !c.connected {
		if err := c.Connect(ctx); err != nil {
			return nil, err
		}
	}

	return &Stream{
		client:    c,
		ctx:       ctx,
		sessionID: c.options.SessionOptions.SessionID,
		sendCh:    make(chan string, 4),
		closeCh:   make(chan struct{}),
	}, nil
}

// Close terminates the Claude CLI subprocess and cleans up resources.
//
// Close should be called when the client is no longer needed. After Close,
// the client cannot be used again.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	c.connected = false

	// Cancel message pump.
	if c.msgCancel != nil {
		c.msgCancel()
	}

	if c.transport != nil {
		return c.transport.Close()
	}

	return nil
}

// ListSkills returns all loaded Skills (user + project).
//
// Skills are loaded during client creation based on SkillsConfig.
// The returned slice is a copy, safe for concurrent access.
func (c *Client) ListSkills() []Skill {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Return a copy to avoid external mutation
	result := make([]Skill, len(c.skills))
	copy(result, c.skills)
	return result
}

// GetSkill retrieves a Skill by name.
//
// Returns ErrSkillNotFound if the Skill does not exist.
func (c *Client) GetSkill(name string) (*Skill, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.skills {
		if c.skills[i].Name == name {
			// Return a copy to avoid external mutation
			skill := c.skills[i]
			return &skill, nil
		}
	}

	return nil, &ErrSkillNotFound{Name: name}
}

// ReloadSkills rescans filesystem and reloads all Skills.
//
// This is useful for picking up new Skills or changes to existing Skills
// without restarting the client. Returns ErrSkillsDisabled if Skills are
// disabled in configuration.
func (c *Client) ReloadSkills() error {
	if !c.options.SkillsConfig.EnableSkills {
		return &ErrSkillsDisabled{}
	}

	loader := NewSkillLoader(
		c.options.SkillsConfig.UserSkillsDir,
		c.options.SkillsConfig.ProjectSkillsDir,
	)

	skills, err := loader.Load()
	if err != nil {
		return fmt.Errorf("failed to reload Skills: %w", err)
	}

	c.mu.Lock()
	c.skills = skills
	c.mu.Unlock()

	return nil
}

// ValidateSkill validates a Skill at the given path without loading it.
//
// This is useful for checking Skill validity before adding it to a Skills
// directory. The path should point to a SKILL.md file.
func (c *Client) ValidateSkill(path string) error {
	loader := NewSkillLoader("", "")
	return loader.ValidateSKILLMd(path)
}

// TaskManager returns a TaskManager for the configured task list.
//
// If TaskListID is not set, an empty string is used as the list ID.
// If TaskStore is configured, that store is used; otherwise a new
// FileTaskStore is created.
//
// The returned TaskManager can be used to create, update, and query
// tasks that are shared with the Claude CLI subprocess.
//
// Example:
//
//	client, _ := claudeagent.NewClient(
//	    claudeagent.WithTaskListID("my-project"),
//	)
//	tm, _ := client.TaskManager()
//	task, _ := tm.Create(ctx, "Build auth", "Implement OAuth2")
func (c *Client) TaskManager() (*TaskManager, error) {
	if c.options.TaskStore != nil {
		return NewTaskManagerWithStore(c.options.TaskListID, c.options.TaskStore), nil
	}
	return NewTaskManager(c.options.TaskListID)
}

// Stream represents a bidirectional conversation stream.
//
// Streams maintain session state and allow multiple rounds of interaction.
// They must be closed when done to free resources.
type Stream struct {
	client    *Client
	ctx       context.Context
	sessionID string
	sendCh    chan string
	closeCh   chan struct{}
	closeOnce sync.Once
}

// Send submits a user message to the stream.
//
// Messages are queued and sent asynchronously. The response will appear
// in the Messages() iterator.
func (s *Stream) Send(ctx context.Context, prompt string) error {
	select {
	case <-s.closeCh:
		return &ErrTransportClosed{}
	case <-ctx.Done():
		return ctx.Err()
	case s.sendCh <- prompt:
		return nil
	}
}

// Messages returns an iterator over response messages.
//
// The iterator yields all messages from the stream until Close() is called
// or the context is canceled.
//
// Example:
//
//	for msg := range stream.Messages() {
//	    switch m := msg.(type) {
//	    case *AssistantMessage:
//	        fmt.Println(m.ContentText())
//	    case *StreamEvent:
//	        if m.Event == "delta" {
//	            fmt.Print(m.Delta)
//	        }
//	    }
//	}
func (s *Stream) Messages() iter.Seq[Message] {
	return func(yield func(Message) bool) {
		// Start send handler.
		go s.handleSends()

		// Read from the shared message channel.
		for {
			select {
			case <-s.closeCh:
				return
			case <-s.ctx.Done():
				return
			case msg, ok := <-s.client.msgCh:
				if !ok {
					return // Channel closed.
				}

				// Yield message to consumer.
				if !yield(msg) {
					return
				}
			}
		}
	}
}

// handleSends processes queued user messages.
func (s *Stream) handleSends() {
	for {
		select {
		case <-s.closeCh:
			return
		case <-s.ctx.Done():
			return
		case prompt := <-s.sendCh:
			userMsg := UserMessage{
				Type:      "user",
				SessionID: s.sessionID,
				Message: APIUserMessage{
					Role: "user",
					Content: []UserContentBlock{
						{Type: "text", Text: prompt},
					},
				},
				ParentToolUseID: nil,
			}

			if err := s.client.protocol.SendMessage(s.ctx, userMsg); err != nil {
				// Log error but continue.
				continue
			}
		}
	}
}

// Interrupt sends an interrupt signal to stop the current generation.
// It blocks until the CLI acknowledges the request or returns an error.
func (s *Stream) Interrupt(ctx context.Context) error {
	_, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype: "interrupt",
	})
	return err
}

// SetPermissionMode dynamically changes the permission mode for this session.
// It blocks until the CLI acknowledges the request or returns an error.
func (s *Stream) SetPermissionMode(ctx context.Context, mode PermissionMode) error {
	_, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype: "set_permission_mode",
		Mode:    string(mode),
	})
	return err
}

// SetModel dynamically changes the model for this session.
// Pass empty string to reset to default.
// It blocks until the CLI acknowledges the request or returns an error.
func (s *Stream) SetModel(ctx context.Context, model string) error {
	_, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype: "set_model",
		Model:   model,
	})
	return err
}

// SetMaxThinkingTokens dynamically changes the max thinking tokens limit.
// Pass nil to remove the limit.
// It blocks until the CLI acknowledges the request or returns an error.
func (s *Stream) SetMaxThinkingTokens(ctx context.Context, tokens *int) error {
	_, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype:           "set_max_thinking_tokens",
		MaxThinkingTokens: tokens,
	})
	return err
}

// RewindFiles restores tracked files to their state at the specified user
// message checkpoint. EnableFileCheckpointing must be true when the session is
// started for the CLI to have checkpoint data.
func (s *Stream) RewindFiles(
	ctx context.Context,
	userMessageID string,
	opts *RewindFilesOptions,
) (*RewindFilesResult, error) {
	body := SDKControlRequestBody{
		Subtype:       "rewind_files",
		UserMessageID: userMessageID,
	}
	if opts != nil && opts.DryRun {
		body.DryRun = &opts.DryRun
	}
	resp, err := s.sendSDKControlRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	bytes, err := json.Marshal(resp.Response.Response)
	if err != nil {
		return nil, fmt.Errorf("rewind_files: marshal: %w", err)
	}
	var out RewindFilesResult
	if err := json.Unmarshal(bytes, &out); err != nil {
		return nil, fmt.Errorf("rewind_files: unmarshal: %w", err)
	}
	return &out, nil
}

// SeedReadState seeds the CLI read-file cache with a path and mtime observed
// by the caller. The CLI uses the mtime to avoid seeding stale reads.
func (s *Stream) SeedReadState(ctx context.Context, path string, mtime int64) error {
	_, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype: "seed_read_state",
		Path:    path,
		MTime:   &mtime,
	})
	return err
}

// ReadFile reads a file from the session filesystem using CLI read permissions.
// Use ReadFileOptions.Encoding="base64" when reading binary content.
// Unlike the TypeScript SDK, CLI errors are returned to the caller instead of
// being swallowed as a nil response.
func (s *Stream) ReadFile(
	ctx context.Context,
	path string,
	opts *ReadFileOptions,
) (*SDKControlReadFileResponse, error) {
	body := SDKControlRequestBody{
		Subtype: "read_file",
		Path:    path,
	}
	if opts != nil {
		if opts.MaxBytes > 0 {
			body.MaxBytes = &opts.MaxBytes
		}
		if opts.Encoding != "" {
			body.Encoding = opts.Encoding
		}
	}
	resp, err := s.sendSDKControlRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	bytes, err := json.Marshal(resp.Response.Response)
	if err != nil {
		return nil, fmt.Errorf("read_file: marshal: %w", err)
	}
	var out SDKControlReadFileResponse
	if err := json.Unmarshal(bytes, &out); err != nil {
		return nil, fmt.Errorf("read_file: unmarshal: %w", err)
	}
	return &out, nil
}

// ReloadPlugins reloads plugins from disk and returns refreshed session metadata.
func (s *Stream) ReloadPlugins(
	ctx context.Context,
) (*SDKControlReloadPluginsResponse, error) {
	resp, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype: "reload_plugins",
	})
	if err != nil {
		return nil, err
	}
	bytes, err := json.Marshal(resp.Response.Response)
	if err != nil {
		return nil, fmt.Errorf("reload_plugins: marshal: %w", err)
	}
	var out SDKControlReloadPluginsResponse
	if err := json.Unmarshal(bytes, &out); err != nil {
		return nil, fmt.Errorf("reload_plugins: unmarshal: %w", err)
	}
	return &out, nil
}

// ApplyFlagSettings merges the provided settings into the flag settings layer,
// updating the active configuration. Top-level keys are shallow-merged by the
// CLI across successive calls and fall back to lower-precedence sources when
// absent (an omitted key is a no-op).
//
// To clear a previously-set top-level key from the flag layer, pass it
// explicitly with a nil value - this marshals to JSON null on the wire, which
// the CLI treats as a clear-this-key signal. Passing nil as the whole settings
// map sends an empty object (no-op for all keys).
//
// Only available in streaming input mode.
func (s *Stream) ApplyFlagSettings(
	ctx context.Context,
	settings map[string]interface{},
) error {
	if settings == nil {
		settings = map[string]interface{}{}
	}
	_, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype:  "apply_flag_settings",
		Settings: &settings,
	})
	return err
}

// StopTask asks the CLI to stop a running task.
func (s *Stream) StopTask(ctx context.Context, taskID string) error {
	_, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype: "stop_task",
		TaskID:  taskID,
	})
	return err
}

// SubmitFeedbackOptions configures Stream.SubmitFeedback.
type SubmitFeedbackOptions struct {
	// Surface identifies the UI surface that originated the feedback.
	Surface string
}

// SubmitFeedback sends free-form session feedback to the CLI.
//
// Only available in streaming input mode.
func (s *Stream) SubmitFeedback(
	ctx context.Context, description string, opts ...SubmitFeedbackOptions,
) error {
	var o SubmitFeedbackOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	_, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype:     "submit_feedback",
		Description: description,
		Surface:     o.Surface,
	})
	return err
}

// BackgroundTasks backgrounds in-flight foreground tasks (Bash commands and
// subagents). When toolUseID is non-empty, only the task started by that
// tool_use block is backgrounded; when empty, all foreground tasks are
// backgrounded, equivalent to pressing Ctrl+B in the terminal.
//
// Each blocking tool call returns immediately with a "running in the
// background" tool_result and the turn continues; the task keeps running
// and emits a task_notification when it settles.
//
// Returns true when at least one task was backgrounded; returns false only
// when toolUseID was non-empty and matched no foreground task.
func (s *Stream) BackgroundTasks(
	ctx context.Context, toolUseID string,
) (bool, error) {
	resp, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype:   "background_tasks",
		ToolUseID: toolUseID,
	})
	if err != nil {
		return false, err
	}

	backgrounded := true
	if v, ok := resp.Response.Response["backgrounded"]; ok {
		if b, ok := v.(bool); ok {
			backgrounded = b
		}
	}
	return backgrounded, nil
}

// sendSDKControlRequest sends an SDK-format control request and waits for the
// matching response.
func (s *Stream) sendSDKControlRequest(
	ctx context.Context, body SDKControlRequestBody,
) (*SDKControlResponse, error) {
	p := s.client.protocol
	requestID := p.nextRequestID()
	req := SDKControlRequest{
		Type:      "control_request",
		RequestID: requestID,
		Request:   body,
	}

	// Register before writing so a fast CLI response cannot race the waiter.
	ch := make(chan SDKControlResponse, 1)
	p.pendingReqs.Store(requestID, ch)

	if err := s.client.transport.Write(ctx, req); err != nil {
		p.pendingReqs.Delete(requestID)
		return nil, fmt.Errorf("control request %q: write: %w", body.Subtype, err)
	}

	select {
	case <-ctx.Done():
		p.pendingReqs.Delete(requestID)
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Response.Subtype == "error" {
			return nil, fmt.Errorf("control request %q: %s", body.Subtype, resp.Response.Error)
		}
		return &resp, nil
	}
}

// InitializationResult returns the cached initialize response.
func (s *Stream) InitializationResult() (*SDKControlInitializeResponse, error) {
	initResp := s.client.protocol.initResult()
	if initResp == nil {
		return nil, ErrNotInitialized
	}
	return cloneInitializeResponse(initResp), nil
}

// SupportedCommands returns the cached list of available slash commands.
// The context is accepted for API compatibility and is not used.
func (s *Stream) SupportedCommands(ctx context.Context) ([]SlashCommand, error) {
	_ = ctx
	initResp := s.client.protocol.initResult()
	if initResp == nil {
		return nil, ErrNotInitialized
	}
	return append([]SlashCommand(nil), initResp.Commands...), nil
}

// SupportedModels returns the cached list of available models.
// The context is accepted for API compatibility and is not used.
func (s *Stream) SupportedModels(ctx context.Context) ([]ModelInfo, error) {
	_ = ctx
	initResp := s.client.protocol.initResult()
	if initResp == nil {
		return nil, ErrNotInitialized
	}
	return append([]ModelInfo(nil), initResp.Models...), nil
}

// SupportedAgents returns the cached list of available agents.
// The context is accepted for API compatibility and is not used.
func (s *Stream) SupportedAgents(ctx context.Context) ([]AgentInfo, error) {
	_ = ctx
	initResp := s.client.protocol.initResult()
	if initResp == nil {
		return nil, ErrNotInitialized
	}
	return append([]AgentInfo(nil), initResp.Agents...), nil
}

// McpServerStatus returns the connection status of all MCP servers.
func (s *Stream) McpServerStatus(ctx context.Context) ([]McpServerStatus, error) {
	resp, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype: "mcp_status",
	})
	if err != nil {
		return nil, err
	}
	raw, ok := resp.Response.Response["mcpServers"]
	if !ok {
		return nil, fmt.Errorf("mcp_status: missing mcpServers in response")
	}
	bytes, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("mcp_status: marshal: %w", err)
	}
	var out []McpServerStatus
	if err := json.Unmarshal(bytes, &out); err != nil {
		return nil, fmt.Errorf("mcp_status: unmarshal: %w", err)
	}
	return out, nil
}

// AccountInfo returns account information for the current session.
func (s *Stream) AccountInfo(ctx context.Context) (*AccountInfo, error) {
	_ = ctx
	initResp := s.client.protocol.initResult()
	if initResp == nil {
		return nil, ErrNotInitialized
	}
	account := initResp.Account
	return &account, nil
}

// GetContextUsage fetches the current context usage from the CLI.
func (s *Stream) GetContextUsage(
	ctx context.Context,
) (*SDKControlGetContextUsageResponse, error) {
	resp, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype: "get_context_usage",
	})
	if err != nil {
		return nil, err
	}
	bytes, err := json.Marshal(resp.Response.Response)
	if err != nil {
		return nil, fmt.Errorf("get_context_usage: marshal: %w", err)
	}
	var out SDKControlGetContextUsageResponse
	if err := json.Unmarshal(bytes, &out); err != nil {
		return nil, fmt.Errorf("get_context_usage: unmarshal: %w", err)
	}
	return &out, nil
}

// ReconnectMcpServer asks the CLI to reconnect the named MCP server.
// Returns an error if the server is unknown or reconnection fails.
func (s *Stream) ReconnectMcpServer(ctx context.Context, serverName string) error {
	_, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype:       "mcp_reconnect",
		MCPServerName: serverName,
	})
	return err
}

// ToggleMcpServer enables or disables the named MCP server.
// Returns an error if the server is unknown or the toggle fails.
func (s *Stream) ToggleMcpServer(
	ctx context.Context,
	serverName string,
	enabled bool,
) error {
	_, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype:       "mcp_toggle",
		MCPServerName: serverName,
		Enabled:       &enabled,
	})
	return err
}

// SetMcpServers replaces the dynamically-added MCP servers for this session
// with the supplied set. Servers omitted from the new map are disconnected;
// new entries are connected.
//
// Process-based servers only: stdio, sse, http. In-process SDK MCP servers
// (Options.SDKMcpServers) are static for the lifetime of the client.
func (s *Stream) SetMcpServers(
	ctx context.Context,
	servers map[string]MCPServerConfig,
) (*McpSetServersResult, error) {
	if servers == nil {
		servers = map[string]MCPServerConfig{}
	}
	resp, err := s.sendSDKControlRequest(ctx, SDKControlRequestBody{
		Subtype: "mcp_set_servers",
		Servers: &servers,
	})
	if err != nil {
		return nil, err
	}
	bytes, err := json.Marshal(resp.Response.Response)
	if err != nil {
		return nil, fmt.Errorf("mcp_set_servers: marshal: %w", err)
	}
	var out McpSetServersResult
	if err := json.Unmarshal(bytes, &out); err != nil {
		return nil, fmt.Errorf("mcp_set_servers: unmarshal: %w", err)
	}
	return &out, nil
}

func cloneInitializeResponse(
	src *SDKControlInitializeResponse,
) *SDKControlInitializeResponse {
	out := *src
	out.Commands = append([]SlashCommand(nil), src.Commands...)
	out.Agents = append([]AgentInfo(nil), src.Agents...)
	out.AvailableOutputStyles = append([]string(nil), src.AvailableOutputStyles...)
	out.Models = append([]ModelInfo(nil), src.Models...)
	return &out
}

// SessionID returns the current session ID.
func (s *Stream) SessionID() string {
	return s.sessionID
}

// Close terminates the stream.
//
// After Close, no more messages can be sent or received on this stream.
// The underlying client connection remains active for other streams.
func (s *Stream) Close() error {
	s.closeOnce.Do(func() {
		close(s.closeCh)
	})
	return nil
}

// stderrCallbackWriter adapts a func(string) callback to the io.Writer
// interface so it can be passed to SubprocessTransport.SetStderrLogger.
// Each Write call invokes the callback with the written data as a string.
type stderrCallbackWriter struct {
	callback func(data string)
}

// Write implements io.Writer. It passes the data to the callback as a string
// and reports all bytes as written.
func (w *stderrCallbackWriter) Write(p []byte) (n int, err error) {
	w.callback(string(p))
	return len(p), nil
}

// validateOptions validates client configuration.
func validateOptions(opts *Options) error {
	// Validate model
	if opts.Model == "" {
		return &ErrInvalidConfiguration{
			Field:  "Model",
			Reason: "model must be specified",
		}
	}

	// Validate permission mode
	validModes := map[PermissionMode]bool{
		PermissionModeDefault:     true,
		PermissionModePlan:        true,
		PermissionModeAcceptEdits: true,
		PermissionModeBypassAll:   true,
		PermissionModeAuto:        true,
		PermissionModeDontAsk:     true,
	}
	if opts.PermissionMode != "" && !validModes[opts.PermissionMode] {
		return &ErrInvalidConfiguration{
			Field:  "PermissionMode",
			Reason: fmt.Sprintf("invalid permission mode: %s", opts.PermissionMode),
		}
	}

	// Validate session options
	if opts.SessionOptions.Resume != "" && opts.SessionOptions.ForkFrom != "" {
		return &ErrInvalidConfiguration{
			Field:  "SessionOptions",
			Reason: "cannot specify both Resume and ForkFrom",
		}
	}

	return nil
}

// isControlMessage checks if a message is a control protocol message.
func isControlMessage(msg Message) bool {
	switch msg.(type) {
	case ControlRequest, ControlResponse,
		SDKControlRequest, SDKControlResponse, SDKControlCancelRequest,
		KeepAliveMessage:
		return true
	default:
		return false
	}
}
