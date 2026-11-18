package claudeagent

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync"
)

// RalphConfig configures a Ralph Wiggum loop.
//
// The Ralph Wiggum technique is an iterative AI development pattern where
// Claude repeatedly works on a task until a completion signal is detected.
// Named after the Simpsons character who persists despite obstacles.
type RalphConfig struct {
	// Task is the initial task prompt for Claude.
	Task string

	// CompletionPromise is the signal text that indicates task completion.
	// Claude should output this wrapped in <promise></promise> tags when done.
	// Default: "TASK COMPLETE"
	CompletionPromise string

	// MaxIterations is the maximum number of iterations before giving up.
	// Default: 10
	MaxIterations int
}

// RalphLoop implements the Ralph Wiggum iterative development pattern.
//
// It uses a Stop hook to intercept session exit attempts and reinject the
// task prompt if the completion promise hasn't been detected. This creates
// a loop where Claude keeps working until either:
// - The completion promise is found in Claude's output
// - MaxIterations is reached
// - The context is canceled
type RalphLoop struct {
	config RalphConfig

	// Internal state (protected by mutex for Stop hook access).
	mu        sync.Mutex
	running   bool
	iteration int
	complete  bool
	totalCost float64
}

// Iteration represents one iteration of the Ralph loop.
type Iteration struct {
	// Number is the 1-based iteration number.
	Number int

	// Messages contains all messages from this iteration.
	Messages []Message

	// Complete indicates whether the completion promise was detected.
	Complete bool

	// Error is set if an error occurred during this iteration.
	Error error

	// SessionID is the CLI session ID.
	SessionID string

	// CostUSD is the cost of this iteration in USD.
	CostUSD float64

	// TotalCostUSD is the cumulative cost across all iterations.
	TotalCostUSD float64
}

// NewRalphLoop creates a new Ralph Wiggum loop with the given configuration.
//
// Example:
//
//	loop := claudeagent.NewRalphLoop(claudeagent.RalphConfig{
//	    Task:              "Build a REST API with tests",
//	    CompletionPromise: "RALPH_COMPLETE",
//	    MaxIterations:     10,
//	})
//
//	for iter := range loop.Run(ctx, claudeagent.WithModel("claude-sonnet-4-5-20250929")) {
//	    fmt.Printf("Iteration %d\n", iter.Number)
//	    if iter.Complete {
//	        fmt.Println("Task completed!")
//	    }
//	}
func NewRalphLoop(cfg RalphConfig) *RalphLoop {
	if cfg.CompletionPromise == "" {
		cfg.CompletionPromise = "TASK COMPLETE"
	}
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 10
	}
	return &RalphLoop{
		config: cfg,
	}
}

// Run executes the Ralph loop, yielding an Iteration for each cycle.
//
// The loop continues until the completion promise is detected, max iterations
// is reached, or the context is canceled. Client options are passed through
// to NewClient, allowing customization of model, permissions, etc.
//
// The Stop hook intercepts session exit attempts and reinjects the task
// prompt with iteration context. Claude sees its previous work (modified
// files, git commits) each iteration.
//
// Concurrency: Run() is not safe to call concurrently on the same RalphLoop
// instance. Each concurrent execution should use its own RalphLoop via
// NewRalphLoop().
//
// Hooks: The Ralph loop uses its own Stop hook to control iteration. Do not
// pass WithHooks with Stop hooks in clientOpts as they will be overwritten.
// Other hook types (PreToolUse, PostToolUse, etc.) work normally.
func (r *RalphLoop) Run(ctx context.Context, clientOpts ...Option) iter.Seq[*Iteration] {
	return func(yield func(*Iteration) bool) {
		// Check if already running and set running flag.
		r.mu.Lock()
		if r.running {
			r.mu.Unlock()
			yield(&Iteration{
				Error: fmt.Errorf("RalphLoop is already running; use a new instance for concurrent execution"),
			})
			return
		}
		r.running = true
		r.iteration = 0
		r.complete = false
		r.totalCost = 0
		r.mu.Unlock()

		// Ensure running flag is cleared when done.
		defer func() {
			r.mu.Lock()
			r.running = false
			r.mu.Unlock()
		}()

		// Build the initial prompt with completion instructions.
		initialPrompt := r.buildPrompt(1)

		// Create the Stop hook that implements Ralph logic.
		stopHook := func(ctx context.Context, input HookInput) (HookResult, error) {
			r.mu.Lock()
			defer r.mu.Unlock()

			// Increment iteration count first, then check termination.
			// This ensures we stop after MaxIterations, not MaxIterations+1.
			r.iteration++

			// Check termination conditions.
			if r.complete || r.iteration >= r.config.MaxIterations {
				// Allow exit.
				return HookResult{
					Continue: true,
					Decision: "approve",
				}, nil
			}

			// Block exit and reinject prompt for next iteration.
			nextPrompt := r.buildPrompt(r.iteration + 1)

			return HookResult{
				Continue: false,
				Decision: "block",
				Reason:   nextPrompt,
				SystemMessage: fmt.Sprintf(
					"Ralph Loop: Iteration %d of %d",
					r.iteration+1, r.config.MaxIterations,
				),
			}, nil
		}

		// Combine user options with Ralph Stop hook.
		// Note: The Ralph Stop hook is appended last, which means if the user
		// passes WithHooks in clientOpts, the Ralph hook will replace their
		// hooks entirely. Users should not configure Stop hooks in clientOpts
		// as they will conflict with the Ralph loop's control flow.
		opts := append([]Option{}, clientOpts...)
		opts = append(opts, WithHooks(map[HookType][]HookConfig{
			HookTypeStop: {{Matcher: "*", Callback: stopHook}},
		}))

		// Create client.
		client, err := NewClient(opts...)
		if err != nil {
			yield(&Iteration{Error: err})
			return
		}
		defer client.Close()

		// Track messages for the current iteration.
		var messages []Message
		var sessionID string
		var iterCost float64
		var previousCost float64
		var iterError error

		// Run the query - Stop hook keeps it looping.
		for msg := range client.Query(ctx, initialPrompt) {
			messages = append(messages, msg)

			// Check for completion promise in assistant messages.
			if am, ok := msg.(AssistantMessage); ok {
				promiseTag := fmt.Sprintf("<promise>%s</promise>", r.config.CompletionPromise)
				if strings.Contains(am.ContentText(), promiseTag) {
					r.mu.Lock()
					r.complete = true
					r.mu.Unlock()
				}
			}

			// Track metadata from result messages.
			// Note: TotalCostUSD is cumulative across the session, so we
			// calculate the per-iteration cost as the delta from previous.
			if rm, ok := msg.(ResultMessage); ok {
				sessionID = rm.SessionID
				r.mu.Lock()
				iterCost = rm.TotalCostUSD - previousCost
				previousCost = rm.TotalCostUSD
				r.totalCost = rm.TotalCostUSD
				r.mu.Unlock()

				// Check for error status in result. Filter out
				// NON-FATAL errors (e.g., lock contention in
				// multi-process scenarios) so they don't kill
				// the loop.
				if rm.Status == "error" || strings.HasPrefix(rm.Subtype, "error") {
					var fatalErrors []string
					for _, e := range rm.Errors {
						if !strings.Contains(e, "NON-FATAL") {
							fatalErrors = append(fatalErrors, e)
						}
					}
					if len(fatalErrors) > 0 {
						iterError = fmt.Errorf("claude error: %s", strings.Join(fatalErrors, "; "))
					} else if len(rm.Errors) == 0 {
						iterError = fmt.Errorf("claude error: %s", rm.Subtype)
					}
					// All errors NON-FATAL: treat as success.
				}
			}
		}

		// Check for context cancellation.
		if iterError == nil && ctx.Err() != nil {
			iterError = ctx.Err()
		}

		// Yield final iteration.
		r.mu.Lock()
		iter := &Iteration{
			Number:       r.iteration + 1,
			Messages:     messages,
			Complete:     r.complete,
			Error:        iterError,
			SessionID:    sessionID,
			CostUSD:      iterCost,
			TotalCostUSD: r.totalCost,
		}
		r.mu.Unlock()

		yield(iter)
	}
}

// buildPrompt constructs the prompt for a given iteration.
func (r *RalphLoop) buildPrompt(iterNum int) string {
	if iterNum == 1 {
		// First iteration: include full task with completion instructions.
		return fmt.Sprintf(
			"%s\n\n"+
				"When you have completed this task, output the completion signal:\n"+
				"<promise>%s</promise>",
			r.config.Task,
			r.config.CompletionPromise,
		)
	}

	// Subsequent iterations: add iteration context.
	return fmt.Sprintf(
		"[Ralph Loop - Iteration %d/%d]\n"+
			"Your previous work is visible in the files. Continue toward completion.\n"+
			"When finished, output: <promise>%s</promise>\n\n"+
			"Task: %s",
		iterNum, r.config.MaxIterations,
		r.config.CompletionPromise,
		r.config.Task,
	)
}

// Config returns the loop configuration.
func (r *RalphLoop) Config() RalphConfig {
	return r.config
}

// IsComplete returns whether the completion promise was detected.
func (r *RalphLoop) IsComplete() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.complete
}

// CurrentIteration returns the current iteration number (0 if not started).
func (r *RalphLoop) CurrentIteration() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.iteration
}

// TotalCost returns the cumulative cost across all iterations.
func (r *RalphLoop) TotalCost() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.totalCost
}
