// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See LICENSE in the project root for license information.

package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AzureAD/microsoft-authentication-extensions-for-go/cache/accessor"
	"github.com/AzureAD/microsoft-authentication-extensions-for-go/cache/internal/lock"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

var (
	// retryDelay lets tests prevent delays when faking errors in Replace
	retryDelay = 10 * time.Millisecond
	// timeout lets tests set the default amount of time allowed to read from the accessor
	timeout = time.Second
)

// locker helps tests fake Lock
type locker interface {
	Lock(context.Context) error
	Unlock() error
}

// Cache caches authentication data in external storage, using a file lock to coordinate
// access with other processes.
type Cache struct {
	// a provides read/write access to storage
	a accessor.Accessor
	// data is accessor's data as of the last sync
	data []byte
	// l coordinates with other processes
	l locker
	// m coordinates this process's goroutines
	m *sync.Mutex
	// sync is when this Cache last read from or wrote to a
	sync time.Time
	// ts is the path to a file used to timestamp Export and Replace operations
	ts string
}

// New is the constructor for Cache. "p" is the path to a file used to track when stored
// data changes. [Cache.Export] will create this file and any directories in its path which don't
// already exist.
func New(a accessor.Accessor, p string) (*Cache, error) {
	lock, err := lock.New(p+".lockfile", retryDelay)
	if err != nil {
		return nil, err
	}
	return &Cache{a: a, l: lock, m: &sync.Mutex{}, ts: p}, err
}

// Export writes the bytes marshaled by "m" to the accessor.
// MSAL clients call this method automatically.
func (c *Cache) Export(ctx context.Context, m cache.Marshaler, h cache.ExportHints) (err error) {
	c.m.Lock()
	defer c.m.Unlock()

	data, err := m.Marshal()
	if err != nil {
		return err
	}
	err = c.l.Lock(ctx)
	if err != nil {
		return err
	}
	defer func() {
		e := c.l.Unlock()
		if err == nil {
			err = e
		}
	}()
	if err = c.a.Write(ctx, data); err == nil {
		// touch the timestamp file to record the time of this write; discard any
		// error because this is just an optimization to avoid redundant reads
		c.sync = time.Now()
		if er := os.Chtimes(c.ts, c.sync, c.sync); errors.Is(er, os.ErrNotExist) {
			if er = os.MkdirAll(filepath.Dir(c.ts), 0700); er == nil {
				f, _ := os.OpenFile(c.ts, os.O_CREATE, 0600)
				_ = f.Close()
			}
		}
		c.data = data
	}
	return err
}

// Replace reads bytes from the accessor and unmarshals them to "u".
// MSAL clients call this method automatically.
func (c *Cache) Replace(ctx context.Context, u cache.Unmarshaler, h cache.ReplaceHints) error {
	c.m.Lock()
	defer c.m.Unlock()

	// If the timestamp file indicates cached data hasn't changed since we last read or wrote it,
	// return c.data, which is the data as of that time. Discard any error from reading the timestamp
	// because this is just an optimization to prevent unnecessary reads. If we don't know whether
	// cached data has changed, we assume it has.
	read := true
	data := c.data
	f, err := os.Stat(c.ts)
	if err == nil {
		mt := f.ModTime()
		read = !mt.Equal(c.sync)
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	// Unmarshal the accessor's data, reading it first if needed. We don't acquire the file lock before
	// reading from the accessor because it isn't strictly necessary and is relatively expensive. In the
	// unlikely event that a read overlaps with a write and returns malformed data, Unmarshal will return
	// an error and we'll try another read.
	for {
		if read {
			data, err = c.a.Read(ctx)
			if err != nil {
				break
			}
		}
		err = u.Unmarshal(data)
		if err == nil {
			break
		} else if !read {
			// c.data is apparently corrupt; Read from the accessor before trying again
			read = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
			// Unmarshal error; try again
		}
	}
	// Update the sync time only if we read from the accessor and unmarshaled its data. Otherwise
	// the data hasn't changed since the last read/write, or reading failed and we'll try again on
	// the next call.
	if err == nil && read {
		c.data = data
		if f, err := os.Stat(c.ts); err == nil {
			c.sync = f.ModTime()
		}
	}
	return err
}

var _ cache.ExportReplace = (*Cache)(nil)
