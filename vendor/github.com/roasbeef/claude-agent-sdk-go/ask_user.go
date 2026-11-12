package claudeagent

import (
	"context"
	"fmt"
	"strings"
)

// Answers is the response format for answering questions.
//
// Keys are question identifiers like "q_0", "q_1", etc.
// Values are the selected option labels or freeform text responses.
type Answers map[string]string

// QuestionSet contains one or more questions from an AskUserQuestion tool call.
//
// Claude can ask up to 4 questions at once. Each question may have multiple-choice
// options or expect freeform text input.
type QuestionSet struct {
	// ToolUseID is the unique identifier for this tool call.
	// Used to correlate answers with the original question.
	ToolUseID string

	// Questions contains 1-4 questions to answer.
	Questions []QuestionItem

	// SessionID is the current session identifier.
	SessionID string

	// ParentToolUseID is set when the question originated from a subagent.
	// If non-nil, this question was asked by a subagent during task delegation.
	// Use IsFromSubagent() to check this conveniently.
	ParentToolUseID *string
}

// IsFromSubagent returns true if this question was asked by a subagent.
//
// When Claude delegates work to a subagent via the Task tool, and that
// subagent uses AskUserQuestion, the question bubbles up through the main
// message stream with ParentToolUseID set to the Task tool invocation.
func (qs QuestionSet) IsFromSubagent() bool {
	return qs.ParentToolUseID != nil
}

// Answer creates an Answers map for a single question.
//
// This is a convenience method for the common case of answering one question.
//
// Example:
//
//	answer(qs.Answer(0, "Yes"))
func (qs QuestionSet) Answer(index int, value string) Answers {
	return Answers{fmt.Sprintf("q_%d", index): value}
}

// AnswerAll creates an Answers map from multiple QuestionAnswer values.
//
// Example:
//
//	answer(qs.AnswerAll(
//	    qs.Q(0).Select("OAuth"),
//	    qs.Q(1).Text("custom response"),
//	))
func (qs QuestionSet) AnswerAll(answers ...QuestionAnswer) Answers {
	result := make(Answers, len(answers))
	for _, a := range answers {
		result[a.Key] = a.Value
	}
	return result
}

// Q returns a QuestionRef for fluent answer building.
//
// The index is 0-based, corresponding to the position in Questions.
// Returns nil if the index is out of range.
func (qs *QuestionSet) Q(index int) *QuestionRef {
	if index < 0 || index >= len(qs.Questions) {
		return nil
	}
	return &QuestionRef{
		qs:    qs,
		index: index,
	}
}

// QuestionRef enables fluent answer construction for a specific question.
type QuestionRef struct {
	qs    *QuestionSet
	index int
}

// Select creates a QuestionAnswer by selecting an option by its label.
//
// Example:
//
//	qs.Q(0).Select("OAuth 2.0")
func (q *QuestionRef) Select(label string) QuestionAnswer {
	return QuestionAnswer{
		Key:   fmt.Sprintf("q_%d", q.index),
		Value: label,
	}
}

// SelectIndex creates a QuestionAnswer by selecting an option by its index.
//
// Returns an empty QuestionAnswer if the QuestionRef is invalid (nil qs,
// invalid question index, or invalid option index). Callers should prefer
// using QuestionSet.Q() to create valid QuestionRef instances.
//
// Example:
//
//	qs.Q(0).SelectIndex(0) // Select first option
func (q *QuestionRef) SelectIndex(optionIndex int) QuestionAnswer {
	// Defensive nil and bounds checks.
	if q.qs == nil || q.index < 0 || q.index >= len(q.qs.Questions) {
		return QuestionAnswer{
			Key:   fmt.Sprintf("q_%d", q.index),
			Value: "",
		}
	}
	question := q.qs.Questions[q.index]
	if optionIndex < 0 || optionIndex >= len(question.Options) {
		return QuestionAnswer{
			Key:   fmt.Sprintf("q_%d", q.index),
			Value: "",
		}
	}
	return QuestionAnswer{
		Key:   fmt.Sprintf("q_%d", q.index),
		Value: question.Options[optionIndex].Label,
	}
}

// SelectMultiple creates a QuestionAnswer with multiple selected options.
//
// For questions with MultiSelect=true, this combines multiple labels.
//
// Example:
//
//	qs.Q(0).SelectMultiple("Feature A", "Feature B")
func (q *QuestionRef) SelectMultiple(labels ...string) QuestionAnswer {
	return QuestionAnswer{
		Key:   fmt.Sprintf("q_%d", q.index),
		Value: strings.Join(labels, ", "),
	}
}

// Text creates a QuestionAnswer with freeform text.
//
// Use this for questions without predefined options.
//
// Example:
//
//	qs.Q(0).Text("My custom response")
func (q *QuestionRef) Text(response string) QuestionAnswer {
	return QuestionAnswer{
		Key:   fmt.Sprintf("q_%d", q.index),
		Value: response,
	}
}

// QuestionAnswer represents a single question's answer.
//
// Use QuestionSet.AnswerAll() to combine multiple QuestionAnswer values
// into an Answers map.
type QuestionAnswer struct {
	// Key is the question identifier (e.g., "q_0", "q_1").
	Key string

	// Value is the selected option label or freeform text.
	Value string
}

// AnswerFunc sends answers for a QuestionSet back to Claude.
//
// Call this function with the answers to respond to the question.
// The conversation will continue after the answers are received.
type AnswerFunc func(answers Answers) error

// AskUserQuestionHandler handles questions from Claude synchronously.
//
// The handler receives a QuestionSet and should return an Answers map.
// Use QuestionSet helper methods to construct answers easily.
//
// Example:
//
//	func handler(ctx context.Context, qs QuestionSet) (Answers, error) {
//	    // Auto-select first option for all questions
//	    answers := make(Answers)
//	    for i, q := range qs.Questions {
//	        if len(q.Options) > 0 {
//	            answers[fmt.Sprintf("q_%d", i)] = q.Options[0].Label
//	        }
//	    }
//	    return answers, nil
//	}
type AskUserQuestionHandler func(ctx context.Context, qs QuestionSet) (Answers, error)

// QuestionMessage is a Message type representing a question from Claude.
//
// When Claude invokes the AskUserQuestion tool, the Query iterator yields
// a QuestionMessage. Call the Respond method with your answers to continue
// the conversation.
//
// QuestionMessage embeds QuestionSet, so all helper methods (Q, Answer,
// AnswerAll) are available directly on the message.
//
// Example:
//
//	for msg := range client.Query(ctx, prompt) {
//	    switch m := msg.(type) {
//	    case QuestionMessage:
//	        fmt.Println("Claude asks:", m.Questions[0].Question)
//	        m.Respond(m.Answer(0, "Yes"))
//	    case AssistantMessage:
//	        fmt.Println(m.ContentText())
//	    }
//	}
type QuestionMessage struct {
	QuestionSet

	// responder is the function that sends the answer back.
	// Set internally by the client.
	responder func(Answers) error
}

// MessageType implements the Message interface.
func (q QuestionMessage) MessageType() string {
	return "question"
}

// Respond sends the answers back to Claude.
//
// Call this method once to answer the question. The conversation will
// continue after the answer is received.
//
// Example:
//
//	// Answer a single question
//	m.Respond(m.Answer(0, "Yes"))
//
//	// Answer multiple questions
//	m.Respond(m.AnswerAll(
//	    m.Q(0).Select("PostgreSQL"),
//	    m.Q(1).Text("custom value"),
//	))
func (q QuestionMessage) Respond(answers Answers) error {
	if q.responder == nil {
		return &ErrQuestionNotFound{ToolUseID: q.ToolUseID}
	}
	return q.responder(answers)
}
