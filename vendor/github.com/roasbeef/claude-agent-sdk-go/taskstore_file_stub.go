//go:build !unix

package claudeagent

import (
	"context"
	"errors"
)

// ErrFileTaskStoreNotSupported is returned on platforms where file-based task
// storage with file locking is not supported (e.g., Windows).
var ErrFileTaskStoreNotSupported = errors.New("FileTaskStore requires Unix file locking (flock), use MemoryTaskStore instead")

// FileTaskStore is a stub implementation for non-Unix platforms.
// File-based task storage with file locking requires Unix syscalls.
type FileTaskStore struct{}

// NewFileTaskStore returns an error on non-Unix platforms.
// Use NewMemoryTaskStore for a cross-platform alternative.
func NewFileTaskStore(baseDir string) (*FileTaskStore, error) {
	return nil, ErrFileTaskStoreNotSupported
}

// Create is not supported on this platform.
func (f *FileTaskStore) Create(ctx context.Context, listID string, task TaskListItem) (string, error) {
	return "", ErrFileTaskStoreNotSupported
}

// Get is not supported on this platform.
func (f *FileTaskStore) Get(ctx context.Context, listID, taskID string) (*TaskListItem, error) {
	return nil, ErrFileTaskStoreNotSupported
}

// Update is not supported on this platform.
func (f *FileTaskStore) Update(ctx context.Context, listID, taskID string, update TaskUpdateInput) error {
	return ErrFileTaskStoreNotSupported
}

// List is not supported on this platform.
func (f *FileTaskStore) List(ctx context.Context, listID string) ([]TaskListItem, error) {
	return nil, ErrFileTaskStoreNotSupported
}

// Delete is not supported on this platform.
func (f *FileTaskStore) Delete(ctx context.Context, listID, taskID string) error {
	return ErrFileTaskStoreNotSupported
}

// Clear is not supported on this platform.
func (f *FileTaskStore) Clear(ctx context.Context, listID string) error {
	return ErrFileTaskStoreNotSupported
}

// Subscribe is not supported on this platform.
func (f *FileTaskStore) Subscribe(ctx context.Context, listID string) (<-chan TaskEvent, error) {
	return nil, ErrFileTaskStoreNotSupported
}

// Export is not supported on this platform.
func (f *FileTaskStore) Export(ctx context.Context, listID string) ([]TaskListItem, error) {
	return nil, ErrFileTaskStoreNotSupported
}

// Import is not supported on this platform.
func (f *FileTaskStore) Import(ctx context.Context, listID string, tasks []TaskListItem, clear bool) error {
	return ErrFileTaskStoreNotSupported
}

// ListIDs is not supported on this platform.
func (f *FileTaskStore) ListIDs(ctx context.Context) ([]string, error) {
	return nil, ErrFileTaskStoreNotSupported
}

// Lock is not supported on this platform.
func (f *FileTaskStore) Lock(ctx context.Context, listID, taskID string) (func(), error) {
	return nil, ErrFileTaskStoreNotSupported
}

// TryLock is not supported on this platform.
func (f *FileTaskStore) TryLock(ctx context.Context, listID, taskID string) (func(), bool, error) {
	return nil, false, ErrFileTaskStoreNotSupported
}

// Verify interface compliance at compile time.
var _ TaskStore = (*FileTaskStore)(nil)
var _ TaskStoreWithExport = (*FileTaskStore)(nil)
var _ TaskStoreWithLocking = (*FileTaskStore)(nil)
