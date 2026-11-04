package claudeagent

// TaskListItem represents a persistent task in the shared task list.
//
// Tasks are managed via the TaskCreate, TaskUpdate, TaskGet, and TaskList
// tools. They persist at ~/.claude/tasks/{listID}/{taskID}.json and can be
// shared across multiple Claude Code instances via CLAUDE_CODE_TASK_LIST_ID.
type TaskListItem struct {
	// ID is the unique task identifier (auto-generated integer string).
	ID string `json:"id"`

	// Subject is a brief title for the task in imperative form.
	// Example: "Set up database connection"
	Subject string `json:"subject"`

	// Description provides detailed information about what needs to be done.
	Description string `json:"description"`

	// ActiveForm is the present continuous form shown in spinners when
	// the task is in_progress. Example: "Setting up database connection"
	ActiveForm string `json:"activeForm,omitempty"`

	// Owner is the agent ID that has claimed this task.
	// Empty string means the task is unclaimed.
	Owner string `json:"owner,omitempty"`

	// Status represents the current state of the task.
	Status TaskListStatus `json:"status"`

	// Blocks contains task IDs that cannot start until this task completes.
	// These are dependent tasks waiting on this one.
	Blocks []string `json:"blocks,omitempty"`

	// BlockedBy contains task IDs that must complete before this task can start.
	// These are dependencies that block this task.
	BlockedBy []string `json:"blockedBy,omitempty"`

	// Metadata contains arbitrary key-value pairs for custom data.
	// Common uses: priority, estimates, tags, external IDs.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// TaskListStatus represents the lifecycle state of a task.
type TaskListStatus string

const (
	// TaskListStatusPending indicates the task is waiting to be started.
	TaskListStatusPending TaskListStatus = "pending"

	// TaskListStatusInProgress indicates the task is actively being worked on.
	TaskListStatusInProgress TaskListStatus = "in_progress"

	// TaskListStatusCompleted indicates the task has been finished.
	TaskListStatusCompleted TaskListStatus = "completed"
)

// IsBlocked returns true if the task has unresolved dependencies.
func (t *TaskListItem) IsBlocked() bool {
	return len(t.BlockedBy) > 0
}

// IsClaimed returns true if the task has an owner.
func (t *TaskListItem) IsClaimed() bool {
	return t.Owner != ""
}

// IsAvailable returns true if the task can be claimed (pending, no owner, not blocked).
func (t *TaskListItem) IsAvailable() bool {
	return t.Status == TaskListStatusPending && !t.IsClaimed() && !t.IsBlocked()
}

// TaskCreateInput is the input for the TaskCreate tool.
//
// Used when Claude creates a new task via the TaskCreate tool.
type TaskCreateInput struct {
	// Subject is a brief title for the task in imperative form.
	Subject string `json:"subject"`

	// Description provides detailed information about what needs to be done.
	Description string `json:"description"`

	// ActiveForm is the present continuous form shown when task is in_progress.
	ActiveForm string `json:"activeForm,omitempty"`

	// Metadata contains arbitrary key-value pairs for custom data.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// TaskUpdateInput is the input for the TaskUpdate tool.
//
// All fields except TaskID are optional - only provided fields are updated.
// AddBlocks and AddBlockedBy append to the existing arrays rather than replace.
type TaskUpdateInput struct {
	// TaskID is the ID of the task to update (required).
	TaskID string `json:"taskId"`

	// Subject updates the task title.
	Subject string `json:"subject,omitempty"`

	// Description updates the task description.
	Description string `json:"description,omitempty"`

	// ActiveForm updates the spinner text.
	ActiveForm string `json:"activeForm,omitempty"`

	// Status updates the task status.
	Status TaskListStatus `json:"status,omitempty"`

	// Owner updates the task owner.
	Owner string `json:"owner,omitempty"`

	// AddBlocks appends task IDs to the blocks array.
	AddBlocks []string `json:"addBlocks,omitempty"`

	// AddBlockedBy appends task IDs to the blockedBy array.
	AddBlockedBy []string `json:"addBlockedBy,omitempty"`

	// Metadata merges with existing metadata. Set a key to nil to delete it.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// TaskGetInput is the input for the TaskGet tool.
type TaskGetInput struct {
	// TaskID is the ID of the task to retrieve.
	TaskID string `json:"taskId"`
}

// TaskListInput is the input for the TaskList tool.
// Currently has no parameters but defined for consistency and future expansion.
type TaskListInput struct{}

// TaskEvent represents a change to a task in the store.
//
// Events are emitted by TaskStore.Subscribe for real-time task updates.
type TaskEvent struct {
	// Type indicates what kind of change occurred.
	Type TaskEventType `json:"type"`

	// ListID is the task list where the change occurred.
	ListID string `json:"listId"`

	// TaskID is the ID of the affected task.
	TaskID string `json:"taskId"`

	// Task contains the full task state after the change.
	// Nil for delete events.
	Task *TaskListItem `json:"task,omitempty"`

	// AgentID identifies which agent made the change (if known).
	AgentID string `json:"agentId,omitempty"`
}

// TaskEventType identifies the kind of task change.
type TaskEventType string

const (
	// TaskEventCreated indicates a new task was created.
	TaskEventCreated TaskEventType = "created"

	// TaskEventUpdated indicates a task was modified.
	TaskEventUpdated TaskEventType = "updated"

	// TaskEventDeleted indicates a task was removed.
	TaskEventDeleted TaskEventType = "deleted"

	// TaskEventClaimed indicates a task was assigned an owner.
	TaskEventClaimed TaskEventType = "claimed"

	// TaskEventCompleted indicates a task status changed to completed.
	TaskEventCompleted TaskEventType = "completed"

	// TaskEventUnblocked indicates a task's blockedBy list became empty.
	TaskEventUnblocked TaskEventType = "unblocked"
)

// ErrTaskNotFound is returned when a task doesn't exist.
type ErrTaskNotFound struct {
	TaskID string
}

func (e *ErrTaskNotFound) Error() string {
	return "task not found: " + e.TaskID
}

// ErrTaskAlreadyExists is returned when creating a task with an existing ID.
type ErrTaskAlreadyExists struct {
	TaskID string
}

func (e *ErrTaskAlreadyExists) Error() string {
	return "task already exists: " + e.TaskID
}

// ErrInvalidTaskStatus is returned for invalid status transitions.
type ErrInvalidTaskStatus struct {
	TaskID string
	From   TaskListStatus
	To     TaskListStatus
}

func (e *ErrInvalidTaskStatus) Error() string {
	return "invalid status transition for task " + e.TaskID + ": " + string(e.From) + " -> " + string(e.To)
}

// ErrTaskBlocked is returned when trying to start a blocked task.
type ErrTaskBlocked struct {
	TaskID    string
	BlockedBy []string
}

func (e *ErrTaskBlocked) Error() string {
	return "task " + e.TaskID + " is blocked by other tasks"
}
