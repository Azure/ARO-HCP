package claudeagent

import (
	"fmt"
)

// ErrUnknownMessageType indicates that a message with an unrecognized type
// field was received from the Claude Code CLI.
type ErrUnknownMessageType struct {
	Type string
}

// Error implements the error interface.
func (e *ErrUnknownMessageType) Error() string {
	return fmt.Sprintf("unknown message type: %s", e.Type)
}

// ErrSubprocessFailed indicates that the Claude Code CLI subprocess failed
// to start or terminated unexpectedly.
type ErrSubprocessFailed struct {
	Cause error
}

// Error implements the error interface.
func (e *ErrSubprocessFailed) Error() string {
	return fmt.Sprintf("subprocess failed: %v", e.Cause)
}

// Unwrap implements the unwrap interface for error chains.
func (e *ErrSubprocessFailed) Unwrap() error {
	return e.Cause
}

// ErrCLINotFound indicates that the Claude Code CLI executable could not
// be located in the system PATH or at the configured path.
type ErrCLINotFound struct {
	Path string
}

// Error implements the error interface.
func (e *ErrCLINotFound) Error() string {
	if e.Path == "" {
		return "claude CLI not found in PATH"
	}
	return fmt.Sprintf("claude CLI not found at: %s", e.Path)
}

// ErrCLIVersionIncompatible indicates that the installed Claude Code CLI
// version does not meet the minimum required version.
type ErrCLIVersionIncompatible struct {
	Found    string
	Required string
}

// Error implements the error interface.
func (e *ErrCLIVersionIncompatible) Error() string {
	return fmt.Sprintf("claude CLI version %s is incompatible (required: %s)", e.Found, e.Required)
}

// ErrProtocolViolation indicates that the CLI sent a message that violates
// the control protocol specification.
type ErrProtocolViolation struct {
	Message string
}

// Error implements the error interface.
func (e *ErrProtocolViolation) Error() string {
	return fmt.Sprintf("protocol violation: %s", e.Message)
}

// ErrTransportClosed indicates an attempt to use a transport that has been
// closed.
type ErrTransportClosed struct{}

// Error implements the error interface.
func (e *ErrTransportClosed) Error() string {
	return "transport is closed"
}

// ErrPermissionDenied indicates that a tool execution was denied by the
// permission system.
type ErrPermissionDenied struct {
	ToolName string
	Reason   string
}

// Error implements the error interface.
func (e *ErrPermissionDenied) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("permission denied for tool: %s", e.ToolName)
	}
	return fmt.Sprintf("permission denied for tool %s: %s", e.ToolName, e.Reason)
}

// ErrSessionNotFound indicates that an attempt was made to resume or fork
// a session that does not exist.
type ErrSessionNotFound struct {
	SessionID string
}

// Error implements the error interface.
func (e *ErrSessionNotFound) Error() string {
	return fmt.Sprintf("session not found: %s", e.SessionID)
}

// ErrHookFailed indicates that a hook callback returned an error.
type ErrHookFailed struct {
	HookType string
	Cause    error
}

// Error implements the error interface.
func (e *ErrHookFailed) Error() string {
	return fmt.Sprintf("hook %s failed: %v", e.HookType, e.Cause)
}

// Unwrap implements the unwrap interface for error chains.
func (e *ErrHookFailed) Unwrap() error {
	return e.Cause
}

// ErrInvalidConfiguration indicates that client configuration is invalid.
type ErrInvalidConfiguration struct {
	Field  string
	Reason string
}

// Error implements the error interface.
func (e *ErrInvalidConfiguration) Error() string {
	return fmt.Sprintf("invalid configuration for %s: %s", e.Field, e.Reason)
}

// ErrSkillNotFound is returned when a Skill is not found by name.
type ErrSkillNotFound struct {
	Name string
}

// Error implements the error interface.
func (e *ErrSkillNotFound) Error() string {
	return fmt.Sprintf("skill not found: %s", e.Name)
}

// ErrSkillsDisabled is returned when Skills operations are attempted
// but Skills loading is disabled.
type ErrSkillsDisabled struct{}

// Error implements the error interface.
func (e *ErrSkillsDisabled) Error() string {
	return "skills loading is disabled"
}

// ErrSkillInvalid is returned when a Skill fails validation.
type ErrSkillInvalid struct {
	Field  string
	Reason string
}

// Error implements the error interface.
func (e *ErrSkillInvalid) Error() string {
	return fmt.Sprintf("skill validation failed for %s: %s", e.Field, e.Reason)
}

// ErrProtocol indicates a protocol-level error from the control protocol.
type ErrProtocol struct {
	Message string
}

// Error implements the error interface.
func (e *ErrProtocol) Error() string {
	return fmt.Sprintf("protocol error: %s", e.Message)
}

// ErrQuestionNotFound indicates an attempt to answer a question that doesn't
// exist or has already been answered.
type ErrQuestionNotFound struct {
	ToolUseID string
}

// Error implements the error interface.
func (e *ErrQuestionNotFound) Error() string {
	return fmt.Sprintf("question not found: %s", e.ToolUseID)
}

// ErrQuestionTimeout indicates a question was not answered within the
// configured timeout period.
//
// This error type is provided for callers to type-switch on when implementing
// timeout handling in their question handlers. It can be returned from custom
// handlers or checked when errors occur.
type ErrQuestionTimeout struct {
	ToolUseID string
}

// Error implements the error interface.
func (e *ErrQuestionTimeout) Error() string {
	return fmt.Sprintf("question timed out: %s", e.ToolUseID)
}

// ErrNoQuestionHandler indicates that an AskUserQuestion tool call was
// received but no handler was configured and no Questions() iterator is active.
//
// This error type is provided for callers to type-switch on when handling
// question-related errors. It indicates a configuration issue where questions
// arrive but no handler is set up to process them.
type ErrNoQuestionHandler struct {
	ToolUseID string
}

// Error implements the error interface.
func (e *ErrNoQuestionHandler) Error() string {
	return fmt.Sprintf("no handler for question: %s (set WithAskUserQuestionHandler or use Questions())", e.ToolUseID)
}
