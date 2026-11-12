package claudeagent

import (
	"context"
	"strconv"
	"sync"
)

// TaskStore is the interface for task persistence backends.
//
// The default implementation (FileTaskStore) stores tasks as JSON files at
// ~/.claude/tasks/{listID}/{taskID}.json, matching the CLI's storage format.
//
// Custom implementations enable distributed coordination patterns:
//   - PostgresTaskStore: PostgreSQL with LISTEN/NOTIFY for subscriptions
//   - RedisTaskStore: Redis with pub/sub for real-time updates
//   - EtcdTaskStore: etcd for distributed consensus
//   - MemoryTaskStore: In-memory for testing
type TaskStore interface {
	// Create adds a new task and returns its auto-generated ID.
	// The task's ID field is ignored - a new ID is always generated.
	Create(ctx context.Context, listID string, task TaskListItem) (string, error)

	// Get retrieves a task by ID.
	// Returns ErrTaskNotFound if the task doesn't exist.
	Get(ctx context.Context, listID, taskID string) (*TaskListItem, error)

	// Update modifies an existing task.
	// Only non-zero fields in the update are applied.
	// AddBlocks/AddBlockedBy append to existing arrays.
	// Returns ErrTaskNotFound if the task doesn't exist.
	Update(ctx context.Context, listID, taskID string, update TaskUpdateInput) error

	// List returns all tasks in a list, sorted by ID (numeric order).
	// Returns empty slice if the list doesn't exist.
	List(ctx context.Context, listID string) ([]TaskListItem, error)

	// Delete removes a task by ID.
	// Returns ErrTaskNotFound if the task doesn't exist.
	// Note: Deleting a task that blocks other tasks leaves those tasks blocked.
	Delete(ctx context.Context, listID, taskID string) error

	// Clear removes all tasks in a list.
	// No error if the list doesn't exist or is already empty.
	Clear(ctx context.Context, listID string) error

	// Subscribe returns a channel for task change notifications.
	// Returns nil, nil if the backend doesn't support subscriptions.
	// The channel is closed when the context is canceled.
	Subscribe(ctx context.Context, listID string) (<-chan TaskEvent, error)
}

// TaskStoreWithLocking extends TaskStore with distributed locking.
//
// Useful for preventing race conditions when multiple Claude instances
// or SDK clients modify the same tasks concurrently.
type TaskStoreWithLocking interface {
	TaskStore

	// Lock acquires an exclusive lock on a task.
	// Returns a release function that must be called when done.
	// Blocks until the lock is acquired or context is canceled.
	Lock(ctx context.Context, listID, taskID string) (release func(), err error)

	// TryLock attempts to acquire a lock without blocking.
	// Returns false if the lock is held by another process.
	TryLock(ctx context.Context, listID, taskID string) (release func(), acquired bool, err error)
}

// TaskStoreWithExport extends TaskStore with bulk operations.
//
// Useful for backup, restore, and migration scenarios.
type TaskStoreWithExport interface {
	TaskStore

	// Export returns all tasks as a JSON-serializable structure.
	Export(ctx context.Context, listID string) ([]TaskListItem, error)

	// Import replaces all tasks from a JSON structure.
	// If clear is true, existing tasks are deleted first.
	// If clear is false, imported tasks are merged (existing IDs updated, new IDs created).
	Import(ctx context.Context, listID string, tasks []TaskListItem, clear bool) error

	// ListIDs returns all task list IDs in the store.
	ListIDs(ctx context.Context) ([]string, error)
}

// MemoryTaskStore is an in-memory TaskStore for testing.
//
// All data is lost when the store is garbage collected.
// Thread-safe for concurrent access.
type MemoryTaskStore struct {
	mu     sync.RWMutex
	lists  map[string]map[string]*TaskListItem
	subs   map[string][]chan TaskEvent
	nextID map[string]int
}

// NewMemoryTaskStore creates a new in-memory task store.
func NewMemoryTaskStore() *MemoryTaskStore {
	return &MemoryTaskStore{
		lists:  make(map[string]map[string]*TaskListItem),
		subs:   make(map[string][]chan TaskEvent),
		nextID: make(map[string]int),
	}
}

// Create implements TaskStore.
func (m *MemoryTaskStore) Create(ctx context.Context, listID string, task TaskListItem) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.lists[listID] == nil {
		m.lists[listID] = make(map[string]*TaskListItem)
	}

	// Generate next ID.
	m.nextID[listID]++
	id := strconv.Itoa(m.nextID[listID])

	// Clone task and set ID.
	t := task
	t.ID = id
	if t.Status == "" {
		t.Status = TaskListStatusPending
	}
	m.lists[listID][id] = &t

	m.emitLocked(listID, TaskEvent{
		Type:   TaskEventCreated,
		ListID: listID,
		TaskID: id,
		Task:   &t,
	})

	return id, nil
}

// Get implements TaskStore.
func (m *MemoryTaskStore) Get(_ context.Context, listID, taskID string) (*TaskListItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.lists[listID] == nil {
		return nil, &ErrTaskNotFound{TaskID: taskID}
	}
	t, ok := m.lists[listID][taskID]
	if !ok {
		return nil, &ErrTaskNotFound{TaskID: taskID}
	}
	// Return a copy to prevent external mutation.
	copy := *t
	return &copy, nil
}

// Update implements TaskStore.
func (m *MemoryTaskStore) Update(_ context.Context, listID, taskID string, update TaskUpdateInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.lists[listID] == nil {
		return &ErrTaskNotFound{TaskID: taskID}
	}
	t, ok := m.lists[listID][taskID]
	if !ok {
		return &ErrTaskNotFound{TaskID: taskID}
	}

	wasBlocked := len(t.BlockedBy) > 0
	wasCompleted := t.Status == TaskListStatusCompleted

	// Apply updates.
	if update.Subject != "" {
		t.Subject = update.Subject
	}
	if update.Description != "" {
		t.Description = update.Description
	}
	if update.ActiveForm != "" {
		t.ActiveForm = update.ActiveForm
	}
	if update.Status != "" {
		t.Status = update.Status
	}
	if update.Owner != "" {
		t.Owner = update.Owner
	}
	if len(update.AddBlocks) > 0 {
		t.Blocks = taskAppendUnique(t.Blocks, update.AddBlocks...)
	}
	if len(update.AddBlockedBy) > 0 {
		t.BlockedBy = taskAppendUnique(t.BlockedBy, update.AddBlockedBy...)
	}
	if update.Metadata != nil {
		if t.Metadata == nil {
			t.Metadata = make(map[string]any)
		}
		for k, v := range update.Metadata {
			if v == nil {
				delete(t.Metadata, k)
			} else {
				t.Metadata[k] = v
			}
		}
	}

	// Emit appropriate events.
	eventType := TaskEventUpdated
	if !wasCompleted && t.Status == TaskListStatusCompleted {
		eventType = TaskEventCompleted
		// Unblock tasks that were waiting on this one.
		m.unblockDependentsLocked(listID, taskID)
	} else if update.Owner != "" && !wasBlocked {
		eventType = TaskEventClaimed
	}

	copy := *t
	m.emitLocked(listID, TaskEvent{
		Type:   eventType,
		ListID: listID,
		TaskID: taskID,
		Task:   &copy,
	})

	return nil
}

// unblockDependentsLocked removes taskID from the blockedBy list of all tasks.
// Caller must hold m.mu.
func (m *MemoryTaskStore) unblockDependentsLocked(listID, taskID string) {
	if m.lists[listID] == nil {
		return
	}
	for id, t := range m.lists[listID] {
		if taskContainsString(t.BlockedBy, taskID) {
			t.BlockedBy = taskRemoveString(t.BlockedBy, taskID)
			if len(t.BlockedBy) == 0 {
				copy := *t
				m.emitLocked(listID, TaskEvent{
					Type:   TaskEventUnblocked,
					ListID: listID,
					TaskID: id,
					Task:   &copy,
				})
			}
		}
	}
}

// List implements TaskStore.
func (m *MemoryTaskStore) List(_ context.Context, listID string) ([]TaskListItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.lists[listID] == nil {
		return []TaskListItem{}, nil
	}

	// Collect and sort by numeric ID.
	result := make([]TaskListItem, 0, len(m.lists[listID]))
	for _, t := range m.lists[listID] {
		result = append(result, *t)
	}
	sortTasksByID(result)
	return result, nil
}

// Delete implements TaskStore.
func (m *MemoryTaskStore) Delete(_ context.Context, listID, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.lists[listID] == nil {
		return &ErrTaskNotFound{TaskID: taskID}
	}
	if _, ok := m.lists[listID][taskID]; !ok {
		return &ErrTaskNotFound{TaskID: taskID}
	}

	delete(m.lists[listID], taskID)

	m.emitLocked(listID, TaskEvent{
		Type:   TaskEventDeleted,
		ListID: listID,
		TaskID: taskID,
	})

	return nil
}

// Clear implements TaskStore.
func (m *MemoryTaskStore) Clear(_ context.Context, listID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.lists, listID)
	delete(m.nextID, listID)
	return nil
}

// Subscribe implements TaskStore.
func (m *MemoryTaskStore) Subscribe(ctx context.Context, listID string) (<-chan TaskEvent, error) {
	m.mu.Lock()
	ch := make(chan TaskEvent, 16)
	m.subs[listID] = append(m.subs[listID], ch)
	m.mu.Unlock()

	// Clean up on context cancel.
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		defer m.mu.Unlock()

		close(ch)
		// Remove from subscribers (simple linear search).
		subs := m.subs[listID]
		for i, sub := range subs {
			if sub == ch {
				m.subs[listID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
	}()

	return ch, nil
}

// emitLocked sends an event to all subscribers.
// Caller must hold m.mu.
func (m *MemoryTaskStore) emitLocked(listID string, event TaskEvent) {
	for _, ch := range m.subs[listID] {
		select {
		case ch <- event:
		default:
			// Skip if buffer is full.
		}
	}
}

// Helper functions for task store operations.

func taskAppendUnique(slice []string, items ...string) []string {
	seen := make(map[string]bool)
	for _, s := range slice {
		seen[s] = true
	}
	for _, item := range items {
		if !seen[item] {
			slice = append(slice, item)
			seen[item] = true
		}
	}
	return slice
}

func taskContainsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func taskRemoveString(slice []string, s string) []string {
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

func sortTasksByID(tasks []TaskListItem) {
	// Simple insertion sort for small lists.
	for i := 1; i < len(tasks); i++ {
		j := i
		for j > 0 && compareTaskIDs(tasks[j-1].ID, tasks[j].ID) > 0 {
			tasks[j-1], tasks[j] = tasks[j], tasks[j-1]
			j--
		}
	}
}

func compareTaskIDs(a, b string) int {
	// Compare as integers if both are numeric.
	aNum := parseTaskID(a)
	bNum := parseTaskID(b)
	if aNum != bNum {
		if aNum < bNum {
			return -1
		}
		return 1
	}
	// Fall back to string comparison.
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// parseTaskID converts a task ID string to an integer for sorting purposes.
// Returns 0 for non-numeric IDs or on parse errors.
func parseTaskID(id string) int {
	n, err := strconv.Atoi(id)
	if err != nil {
		return 0
	}
	return n
}
