// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See LICENSE in the project root for license information.

package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/AzureAD/microsoft-authentication-extensions-for-go/cache/internal/flock"
)

// timeout lets tests set the default amount of time allowed to acquire the lock
var timeout = 5 * time.Second

// flocker helps tests fake flock
type flocker interface {
	Fh() *os.File
	Path() string
	TryLockContext(context.Context, time.Duration) (bool, error)
	Unlock() error
}

// Lock uses a file lock to coordinate access to resources shared with other processes.
// Callers are responsible for preventing races within a process. Lock applies advisory
// locks on Linux and macOS and is therefore unreliable on these platforms when several
// processes concurrently try to acquire the lock.
type Lock struct {
	f          flocker
	retryDelay time.Duration
}

// New is the constructor for Lock. "p" is the path to the lock file.
func New(p string, retryDelay time.Duration) (*Lock, error) {
	// ensure all dirs in the path exist before flock tries to create the file
	err := os.MkdirAll(filepath.Dir(p), os.ModePerm)
	if err != nil {
		return nil, err
	}
	return &Lock{f: flock.New(p), retryDelay: retryDelay}, nil
}

// Lock acquires the file lock on behalf of the process. The behavior of concurrent
// and repeated calls is undefined. For example, Linux may or may not allow goroutines
// scheduled on different threads to hold the lock simultaneously.
func (l *Lock) Lock(ctx context.Context) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	for {
		// flock opens the file before locking it and returns errors due to an existing
		// lock or one acquired by another process after this process has opened the
		// file. We ignore some errors here because in such cases we want to retry until
		// the deadline.
		locked, err := l.f.TryLockContext(ctx, l.retryDelay)
		if err != nil {
			if !(errors.Is(err, os.ErrPermission) || isWindowsSharingViolation(err)) {
				return err
			}
		} else if locked {
			if fh := l.f.Fh(); fh != nil {
				s := fmt.Sprintf("{%d} {%s}", os.Getpid(), os.Args[0])
				_, _ = fh.WriteString(s)
			}
			return nil
		}
	}
}

// Unlock releases the lock and deletes the lock file.
func (l *Lock) Unlock() error {
	err := l.f.Unlock()
	if err == nil {
		err = os.Remove(l.f.Path())
	}
	// ignore errors caused by another process deleting the file or locking between the above Unlock and Remove
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || isWindowsSharingViolation(err) {
		return nil
	}
	return err
}

func isWindowsSharingViolation(err error) bool {
	return runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(32))
}
