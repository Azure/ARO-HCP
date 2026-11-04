//go:build unix

package claudeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// FileTaskStore implements TaskStore using JSON files on disk.
//
// Tasks are stored at ~/.claude/tasks/{listID}/{taskID}.json, matching
// the Claude Code CLI's storage format. This enables SDK and CLI to
// share the same task list via CLAUDE_CODE_TASK_LIST_ID.
//
// FileTaskStore uses file locking (flock) to prevent concurrent access
// issues when multiple Claude instances modify the same task list.
type FileTaskStore struct {
	baseDir string
	mu      sync.RWMutex
	subs    map[string][]chan TaskEvent
}

// NewFileTaskStore creates a new file-based task store.
//
// Tasks are stored at ~/.claude/tasks/ by default. Use the baseDir
// parameter to override the storage location.
func NewFileTaskStore(baseDir string) (*FileTaskStore, error) {
	if baseDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		baseDir = filepath.Join(home, ".claude", "tasks")
	}

	return &FileTaskStore{
		baseDir: baseDir,
		subs:    make(map[string][]chan TaskEvent),
	}, nil
}

// listDir returns the directory path for a specific task list.
func (f *FileTaskStore) listDir(listID string) string {
	return filepath.Join(f.baseDir, listID)
}

// taskFile returns the file path for a specific task.
func (f *FileTaskStore) taskFile(listID, taskID string) string {
	return filepath.Join(f.listDir(listID), taskID+".json")
}

// ensureListDir creates the list directory if it doesn't exist.
func (f *FileTaskStore) ensureListDir(listID string) error {
	dir := f.listDir(listID)
	return os.MkdirAll(dir, 0700)
}

// Create implements TaskStore.
func (f *FileTaskStore) Create(ctx context.Context, listID string, task TaskListItem) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.ensureListDir(listID); err != nil {
		return "", fmt.Errorf("failed to create list directory: %w", err)
	}

	// Generate next ID by finding the max existing ID.
	nextID, err := f.nextID(listID)
	if err != nil {
		return "", fmt.Errorf("failed to generate task ID: %w", err)
	}
	id := strconv.Itoa(nextID)

	// Clone and initialize task.
	t := task
	t.ID = id
	if t.Status == "" {
		t.Status = TaskListStatusPending
	}
	if t.Blocks == nil {
		t.Blocks = []string{}
	}
	if t.BlockedBy == nil {
		t.BlockedBy = []string{}
	}

	// Write task file atomically.
	if err := f.writeTask(listID, &t); err != nil {
		return "", err
	}

	f.emit(listID, TaskEvent{
		Type:   TaskEventCreated,
		ListID: listID,
		TaskID: id,
		Task:   &t,
	})

	return id, nil
}

// nextID finds the next available task ID for a list.
func (f *FileTaskStore) nextID(listID string) (int, error) {
	dir := f.listDir(listID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, err
	}

	maxID := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		idStr := strings.TrimSuffix(name, ".json")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue // Skip non-numeric files.
		}
		if id > maxID {
			maxID = id
		}
	}

	return maxID + 1, nil
}

// writeTask writes a task to disk atomically.
func (f *FileTaskStore) writeTask(listID string, task *TaskListItem) error {
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	taskPath := f.taskFile(listID, task.ID)
	tempPath := taskPath + ".tmp"

	// Write to temp file.
	if err := os.WriteFile(tempPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Rename atomically.
	if err := os.Rename(tempPath, taskPath); err != nil {
		_ = os.Remove(tempPath) // Clean up temp file on error.
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// readTask reads a task from disk.
func (f *FileTaskStore) readTask(listID, taskID string) (*TaskListItem, error) {
	taskPath := f.taskFile(listID, taskID)
	data, err := os.ReadFile(taskPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &ErrTaskNotFound{TaskID: taskID}
		}
		return nil, fmt.Errorf("failed to read task file: %w", err)
	}

	var task TaskListItem
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task: %w", err)
	}

	return &task, nil
}

// Get implements TaskStore.
func (f *FileTaskStore) Get(ctx context.Context, listID, taskID string) (*TaskListItem, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return f.readTask(listID, taskID)
}

// Update implements TaskStore.
func (f *FileTaskStore) Update(ctx context.Context, listID, taskID string, update TaskUpdateInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Read existing task.
	task, err := f.readTask(listID, taskID)
	if err != nil {
		return err
	}

	wasBlocked := len(task.BlockedBy) > 0
	wasCompleted := task.Status == TaskListStatusCompleted

	// Apply updates.
	if update.Subject != "" {
		task.Subject = update.Subject
	}
	if update.Description != "" {
		task.Description = update.Description
	}
	if update.ActiveForm != "" {
		task.ActiveForm = update.ActiveForm
	}
	if update.Status != "" {
		task.Status = update.Status
	}
	if update.Owner != "" {
		task.Owner = update.Owner
	}
	if len(update.AddBlocks) > 0 {
		task.Blocks = taskAppendUnique(task.Blocks, update.AddBlocks...)
	}
	if len(update.AddBlockedBy) > 0 {
		task.BlockedBy = taskAppendUnique(task.BlockedBy, update.AddBlockedBy...)
	}
	if update.Metadata != nil {
		if task.Metadata == nil {
			task.Metadata = make(map[string]any)
		}
		for k, v := range update.Metadata {
			if v == nil {
				delete(task.Metadata, k)
			} else {
				task.Metadata[k] = v
			}
		}
	}

	// Write updated task.
	if err := f.writeTask(listID, task); err != nil {
		return err
	}

	// Handle completion - unblock dependent tasks.
	if !wasCompleted && task.Status == TaskListStatusCompleted {
		if err := f.unblockDependentsLocked(listID, taskID); err != nil {
			// Log but don't fail - main task update succeeded.
			_ = err
		}
	}

	// Emit event.
	eventType := TaskEventUpdated
	if !wasCompleted && task.Status == TaskListStatusCompleted {
		eventType = TaskEventCompleted
	} else if update.Owner != "" && !wasBlocked {
		eventType = TaskEventClaimed
	}

	f.emit(listID, TaskEvent{
		Type:   eventType,
		ListID: listID,
		TaskID: taskID,
		Task:   task,
	})

	return nil
}

// unblockDependentsLocked removes taskID from blockedBy of all tasks.
// Caller must hold f.mu.
func (f *FileTaskStore) unblockDependentsLocked(listID, taskID string) error {
	dir := f.listDir(listID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if id == taskID {
			continue // Skip the completed task itself.
		}

		task, err := f.readTask(listID, id)
		if err != nil {
			continue
		}

		if taskContainsString(task.BlockedBy, taskID) {
			task.BlockedBy = taskRemoveString(task.BlockedBy, taskID)
			if err := f.writeTask(listID, task); err != nil {
				continue
			}

			if len(task.BlockedBy) == 0 {
				f.emit(listID, TaskEvent{
					Type:   TaskEventUnblocked,
					ListID: listID,
					TaskID: id,
					Task:   task,
				})
			}
		}
	}

	return nil
}

// List implements TaskStore.
func (f *FileTaskStore) List(ctx context.Context, listID string) ([]TaskListItem, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	dir := f.listDir(listID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []TaskListItem{}, nil
		}
		return nil, fmt.Errorf("failed to read list directory: %w", err)
	}

	var tasks []TaskListItem
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		task, err := f.readTask(listID, id)
		if err != nil {
			continue // Skip corrupted files.
		}
		tasks = append(tasks, *task)
	}

	// Sort by numeric ID.
	sort.Slice(tasks, func(i, j int) bool {
		iNum, _ := strconv.Atoi(tasks[i].ID)
		jNum, _ := strconv.Atoi(tasks[j].ID)
		return iNum < jNum
	})

	return tasks, nil
}

// Delete implements TaskStore.
func (f *FileTaskStore) Delete(ctx context.Context, listID, taskID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	taskPath := f.taskFile(listID, taskID)
	if err := os.Remove(taskPath); err != nil {
		if os.IsNotExist(err) {
			return &ErrTaskNotFound{TaskID: taskID}
		}
		return fmt.Errorf("failed to delete task file: %w", err)
	}

	f.emit(listID, TaskEvent{
		Type:   TaskEventDeleted,
		ListID: listID,
		TaskID: taskID,
	})

	return nil
}

// Clear implements TaskStore.
func (f *FileTaskStore) Clear(ctx context.Context, listID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	dir := f.listDir(listID)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to clear list directory: %w", err)
	}

	return nil
}

// Subscribe implements TaskStore.
func (f *FileTaskStore) Subscribe(ctx context.Context, listID string) (<-chan TaskEvent, error) {
	f.mu.Lock()
	ch := make(chan TaskEvent, 16)
	f.subs[listID] = append(f.subs[listID], ch)
	f.mu.Unlock()

	// Clean up on context cancel.
	go func() {
		<-ctx.Done()
		f.mu.Lock()
		defer f.mu.Unlock()

		close(ch)
		subs := f.subs[listID]
		for i, sub := range subs {
			if sub == ch {
				f.subs[listID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
	}()

	return ch, nil
}

// emit sends an event to all subscribers.
func (f *FileTaskStore) emit(listID string, event TaskEvent) {
	for _, ch := range f.subs[listID] {
		select {
		case ch <- event:
		default:
			// Skip if buffer is full.
		}
	}
}

// Export implements TaskStoreWithExport.
func (f *FileTaskStore) Export(ctx context.Context, listID string) ([]TaskListItem, error) {
	return f.List(ctx, listID)
}

// Import implements TaskStoreWithExport.
func (f *FileTaskStore) Import(ctx context.Context, listID string, tasks []TaskListItem, clear bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if clear {
		dir := f.listDir(listID)
		if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to clear list directory: %w", err)
		}
	}

	if err := f.ensureListDir(listID); err != nil {
		return fmt.Errorf("failed to create list directory: %w", err)
	}

	for _, task := range tasks {
		t := task
		if err := f.writeTask(listID, &t); err != nil {
			return fmt.Errorf("failed to write task %s: %w", task.ID, err)
		}
	}

	return nil
}

// ListIDs implements TaskStoreWithExport.
func (f *FileTaskStore) ListIDs(ctx context.Context) ([]string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	entries, err := os.ReadDir(f.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read base directory: %w", err)
	}

	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}

	return ids, nil
}

// Lock implements TaskStoreWithLocking.Lock.
func (f *FileTaskStore) Lock(ctx context.Context, listID, taskID string) (func(), error) {
	lockPath := f.taskFile(listID, taskID) + ".lock"

	// Ensure directory exists.
	if err := f.ensureListDir(listID); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file: %w", err)
	}

	// Acquire exclusive lock (blocking).
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to acquire lock: %w", err)
	}

	release := func() {
		defer func() { _ = os.Remove(lockPath) }()
		defer func() { _ = file.Close() }()
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	}

	return release, nil
}

// TryLock implements TaskStoreWithLocking.TryLock.
func (f *FileTaskStore) TryLock(ctx context.Context, listID, taskID string) (func(), bool, error) {
	lockPath := f.taskFile(listID, taskID) + ".lock"

	// Ensure directory exists.
	if err := f.ensureListDir(listID); err != nil {
		return nil, false, err
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, fmt.Errorf("failed to open lock file: %w", err)
	}

	// Try to acquire exclusive lock (non-blocking).
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, false, nil // Lock held by another process.
		}
		return nil, false, fmt.Errorf("failed to acquire lock: %w", err)
	}

	release := func() {
		defer func() { _ = os.Remove(lockPath) }()
		defer func() { _ = file.Close() }()
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	}

	return release, true, nil
}

// Verify interface compliance at compile time.
var _ TaskStore = (*FileTaskStore)(nil)
var _ TaskStoreWithExport = (*FileTaskStore)(nil)
var _ TaskStoreWithLocking = (*FileTaskStore)(nil)
