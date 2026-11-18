package claudeagent

import (
	"context"
)

// TaskManager provides high-level task management for a specific task list.
//
// TaskManager wraps a TaskStore to provide ergonomic APIs for common task
// operations like claiming, completing, and filtering tasks.
//
// Example:
//
//	tm := claudeagent.NewTaskManager("my-project")
//	task, _ := tm.Create(ctx, "Build auth module", "Implement OAuth2 flow")
//	tm.Claim(ctx, task.ID, "backend-agent")
//	tm.Complete(ctx, task.ID)
type TaskManager struct {
	store  TaskStore
	listID string
}

// NewTaskManager creates a task manager with the default FileTaskStore.
//
// The listID identifies the task list, which maps to storage at
// ~/.claude/tasks/{listID}/. Multiple Claude instances using the same
// listID will share the same task list.
func NewTaskManager(listID string) (*TaskManager, error) {
	store, err := NewFileTaskStore("")
	if err != nil {
		return nil, err
	}
	return &TaskManager{
		store:  store,
		listID: listID,
	}, nil
}

// NewTaskManagerWithStore creates a task manager with a custom store.
//
// Use this for testing (with MemoryTaskStore) or for custom backends
// like PostgreSQL or Redis.
func NewTaskManagerWithStore(listID string, store TaskStore) *TaskManager {
	return &TaskManager{
		store:  store,
		listID: listID,
	}
}

// ListID returns the task list identifier.
func (tm *TaskManager) ListID() string {
	return tm.listID
}

// Store returns the underlying TaskStore.
func (tm *TaskManager) Store() TaskStore {
	return tm.store
}

// TaskOption is a functional option for creating tasks.
type TaskOption func(*TaskListItem)

// WithActiveForm sets the active form text shown when the task is in progress.
func WithActiveForm(activeForm string) TaskOption {
	return func(t *TaskListItem) {
		t.ActiveForm = activeForm
	}
}

// WithMetadata sets custom metadata on the task.
func WithMetadata(metadata map[string]any) TaskOption {
	return func(t *TaskListItem) {
		t.Metadata = metadata
	}
}

// WithPriority sets the priority in metadata (convenience for common use case).
func WithPriority(priority string) TaskOption {
	return func(t *TaskListItem) {
		if t.Metadata == nil {
			t.Metadata = make(map[string]any)
		}
		t.Metadata["priority"] = priority
	}
}

// WithEstimate sets the estimate in metadata (convenience for common use case).
func WithEstimate(estimate string) TaskOption {
	return func(t *TaskListItem) {
		if t.Metadata == nil {
			t.Metadata = make(map[string]any)
		}
		t.Metadata["estimate"] = estimate
	}
}

// Create adds a new task to the list.
//
// Tasks start with status "pending" and no owner.
func (tm *TaskManager) Create(ctx context.Context, subject, description string, opts ...TaskOption) (*TaskListItem, error) {
	task := TaskListItem{
		Subject:     subject,
		Description: description,
		Status:      TaskListStatusPending,
	}

	for _, opt := range opts {
		opt(&task)
	}

	id, err := tm.store.Create(ctx, tm.listID, task)
	if err != nil {
		return nil, err
	}

	return tm.store.Get(ctx, tm.listID, id)
}

// Get retrieves a task by ID.
func (tm *TaskManager) Get(ctx context.Context, taskID string) (*TaskListItem, error) {
	return tm.store.Get(ctx, tm.listID, taskID)
}

// TaskUpdateOption is a functional option for updating tasks.
type TaskUpdateOption func(*TaskUpdateInput)

// UpdateSubject changes the task subject.
func UpdateSubject(subject string) TaskUpdateOption {
	return func(u *TaskUpdateInput) {
		u.Subject = subject
	}
}

// UpdateDescription changes the task description.
func UpdateDescription(description string) TaskUpdateOption {
	return func(u *TaskUpdateInput) {
		u.Description = description
	}
}

// UpdateActiveForm changes the active form text.
func UpdateActiveForm(activeForm string) TaskUpdateOption {
	return func(u *TaskUpdateInput) {
		u.ActiveForm = activeForm
	}
}

// UpdateStatus changes the task status.
func UpdateStatus(status TaskListStatus) TaskUpdateOption {
	return func(u *TaskUpdateInput) {
		u.Status = status
	}
}

// UpdateOwner changes the task owner.
func UpdateOwner(owner string) TaskUpdateOption {
	return func(u *TaskUpdateInput) {
		u.Owner = owner
	}
}

// AddBlocks adds tasks that this task blocks.
func AddBlocks(taskIDs ...string) TaskUpdateOption {
	return func(u *TaskUpdateInput) {
		u.AddBlocks = append(u.AddBlocks, taskIDs...)
	}
}

// AddBlockedBy adds tasks that block this task.
func AddBlockedBy(taskIDs ...string) TaskUpdateOption {
	return func(u *TaskUpdateInput) {
		u.AddBlockedBy = append(u.AddBlockedBy, taskIDs...)
	}
}

// UpdateMetadata merges metadata into the task.
func UpdateMetadata(metadata map[string]any) TaskUpdateOption {
	return func(u *TaskUpdateInput) {
		u.Metadata = metadata
	}
}

// Update modifies a task using functional options.
func (tm *TaskManager) Update(ctx context.Context, taskID string, opts ...TaskUpdateOption) error {
	update := TaskUpdateInput{TaskID: taskID}
	for _, opt := range opts {
		opt(&update)
	}
	return tm.store.Update(ctx, tm.listID, taskID, update)
}

// List returns all tasks in the list.
func (tm *TaskManager) List(ctx context.Context) ([]TaskListItem, error) {
	return tm.store.List(ctx, tm.listID)
}

// ListPending returns tasks with status "pending".
func (tm *TaskManager) ListPending(ctx context.Context) ([]TaskListItem, error) {
	tasks, err := tm.store.List(ctx, tm.listID)
	if err != nil {
		return nil, err
	}

	var pending []TaskListItem
	for _, t := range tasks {
		if t.Status == TaskListStatusPending {
			pending = append(pending, t)
		}
	}
	return pending, nil
}

// ListInProgress returns tasks with status "in_progress".
func (tm *TaskManager) ListInProgress(ctx context.Context) ([]TaskListItem, error) {
	tasks, err := tm.store.List(ctx, tm.listID)
	if err != nil {
		return nil, err
	}

	var inProgress []TaskListItem
	for _, t := range tasks {
		if t.Status == TaskListStatusInProgress {
			inProgress = append(inProgress, t)
		}
	}
	return inProgress, nil
}

// ListByOwner returns tasks owned by a specific agent.
func (tm *TaskManager) ListByOwner(ctx context.Context, ownerID string) ([]TaskListItem, error) {
	tasks, err := tm.store.List(ctx, tm.listID)
	if err != nil {
		return nil, err
	}

	var owned []TaskListItem
	for _, t := range tasks {
		if t.Owner == ownerID {
			owned = append(owned, t)
		}
	}
	return owned, nil
}

// ListUnblocked returns pending tasks that have no blockers and no owner.
//
// These are tasks that are available to be claimed.
func (tm *TaskManager) ListUnblocked(ctx context.Context) ([]TaskListItem, error) {
	tasks, err := tm.store.List(ctx, tm.listID)
	if err != nil {
		return nil, err
	}

	var available []TaskListItem
	for _, t := range tasks {
		if t.IsAvailable() {
			available = append(available, t)
		}
	}
	return available, nil
}

// Claim assigns an owner to a task and sets status to in_progress.
//
// Returns ErrTaskNotFound if the task doesn't exist.
// Does not check if the task is already claimed - use ListUnblocked to
// find available tasks first.
func (tm *TaskManager) Claim(ctx context.Context, taskID, ownerID string) error {
	return tm.store.Update(ctx, tm.listID, taskID, TaskUpdateInput{
		TaskID: taskID,
		Owner:  ownerID,
		Status: TaskListStatusInProgress,
	})
}

// Complete marks a task as completed.
//
// This automatically unblocks tasks that were waiting on this one.
func (tm *TaskManager) Complete(ctx context.Context, taskID string) error {
	return tm.store.Update(ctx, tm.listID, taskID, TaskUpdateInput{
		TaskID: taskID,
		Status: TaskListStatusCompleted,
	})
}

// Delete removes a task from the list.
func (tm *TaskManager) Delete(ctx context.Context, taskID string) error {
	return tm.store.Delete(ctx, tm.listID, taskID)
}

// Clear removes all tasks from the list.
func (tm *TaskManager) Clear(ctx context.Context) error {
	return tm.store.Clear(ctx, tm.listID)
}

// Watch returns a channel for task change notifications.
//
// Returns nil, nil if the store doesn't support subscriptions.
func (tm *TaskManager) Watch(ctx context.Context) (<-chan TaskEvent, error) {
	return tm.store.Subscribe(ctx, tm.listID)
}

// NextAvailable returns the first unblocked, unclaimed task.
//
// Returns nil if no tasks are available.
func (tm *TaskManager) NextAvailable(ctx context.Context) (*TaskListItem, error) {
	available, err := tm.ListUnblocked(ctx)
	if err != nil {
		return nil, err
	}
	if len(available) == 0 {
		return nil, nil
	}
	return &available[0], nil
}

// ClaimNext claims the first available task and returns it.
//
// This is a convenience method that combines NextAvailable and Claim.
// Returns nil if no tasks are available.
func (tm *TaskManager) ClaimNext(ctx context.Context, ownerID string) (*TaskListItem, error) {
	task, err := tm.NextAvailable(ctx)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, nil
	}

	if err := tm.Claim(ctx, task.ID, ownerID); err != nil {
		return nil, err
	}

	// Return fresh copy with updated status.
	return tm.Get(ctx, task.ID)
}

// Stats returns summary statistics for the task list.
type TaskStats struct {
	Total      int
	Pending    int
	InProgress int
	Completed  int
	Blocked    int
	Unblocked  int
}

// Stats calculates summary statistics for the task list.
func (tm *TaskManager) Stats(ctx context.Context) (*TaskStats, error) {
	tasks, err := tm.store.List(ctx, tm.listID)
	if err != nil {
		return nil, err
	}

	stats := &TaskStats{Total: len(tasks)}
	for _, t := range tasks {
		switch t.Status {
		case TaskListStatusPending:
			stats.Pending++
		case TaskListStatusInProgress:
			stats.InProgress++
		case TaskListStatusCompleted:
			stats.Completed++
		}
		if len(t.BlockedBy) > 0 {
			stats.Blocked++
		} else if t.Status == TaskListStatusPending {
			stats.Unblocked++
		}
	}

	return stats, nil
}
