// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package capture

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/rtnl"
)

// Run enters path and writes namespace-local rtnetlink state as JSON. It MUST
// only be called in a dedicated child process whose main exits after returning:
// the calling OS thread remains locked and is never restored to its old netns.
// The caller enforces its configured namespace directory; Run accepts any real
// parent directory, but requires a clean absolute path with no symlink components
// and a cni- basename. No sysfs is read: its mount may describe a different netns.
func Run(path string, output io.Writer) error {
	fd, err := OpenNamespace(path)
	if err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		CloseWithLog("namespace", func() error { return unix.Close(fd) })
		return fmt.Errorf("stat network namespace: %w", err)
	}
	runtime.LockOSThread()
	err = unix.Setns(fd, unix.CLONE_NEWNET)
	CloseWithLog("namespace", func() error { return unix.Close(fd) })
	if err != nil {
		return fmt.Errorf("enter network namespace: %w", err)
	}
	result, err := collect()
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(captureResult{
		Namespace: namespaceIdentity{Device: uint64(stat.Dev), Inode: stat.Ino},
		State:     result,
	})
}

// Identity comes from the same pinned descriptor used by setns, not a second
// path lookup. No capture timestamp is included, so unchanged state can dedupe.
type namespaceIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type captureResult struct {
	Namespace namespaceIdentity       `json:"namespace"`
	State     map[string]rtnl.Section `json:"state"`
}

// OpenNamespace opens a pinned network namespace descriptor after validating
// its path, filesystem and namespace type. The caller owns and must close it.
// It does not enter the namespace or verify a caller's expected device/inode.
func OpenNamespace(path string) (int, error) {
	base := filepath.Base(path)
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !strings.HasPrefix(base, "cni-") || len(base) <= len("cni-") {
		return -1, fmt.Errorf("namespace path must be clean, absolute and have a cni- basename")
	}
	parent := filepath.Dir(path)
	root, err := os.OpenRoot(parent)
	if err != nil {
		return -1, fmt.Errorf("open namespace parent: %w", err)
	}
	defer CloseWithLog("namespace root", root.Close)
	dir, err := root.OpenFile(".", os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return -1, err
	}
	defer CloseWithLog("namespace directory", dir.Close)
	// OpenRoot confines later lookups, but follows symlinks in its own path.
	// Validate the pinned parent with a no-symlinks lookup, failing closed if
	// openat2 is unavailable or the directory changed between the two opens.
	verified, err := unix.Openat2(unix.AT_FDCWD, parent, &unix.OpenHow{
		Flags: unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS,
	})
	if err != nil {
		return -1, fmt.Errorf("validate namespace parent: %w", err)
	}
	var actual, expected unix.Stat_t
	statErr := unix.Fstat(verified, &expected)
	CloseWithLog("verified namespace directory", func() error { return unix.Close(verified) })
	if statErr != nil {
		return -1, statErr
	}
	if err := unix.Fstat(int(dir.Fd()), &actual); err != nil {
		return -1, err
	}
	if actual.Dev != expected.Dev || actual.Ino != expected.Ino {
		return -1, fmt.Errorf("namespace parent changed during validation")
	}
	fd, err := unix.Openat(int(dir.Fd()), base, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, fmt.Errorf("open namespace: %w", err)
	}
	var fs unix.Statfs_t
	if err = unix.Fstatfs(fd, &fs); err == nil && fs.Type != unix.NSFS_MAGIC {
		err = fmt.Errorf("namespace file is not nsfs")
	}
	if err == nil {
		var kind int
		kind, err = unix.IoctlRetInt(fd, unix.NS_GET_NSTYPE)
		if err == nil && kind != unix.CLONE_NEWNET {
			err = fmt.Errorf("namespace is not a network namespace")
		}
	}
	if err != nil {
		CloseWithLog("namespace", func() error { return unix.Close(fd) })
		return -1, fmt.Errorf("validate namespace: %w", err)
	}
	return fd, nil
}

func collect() (map[string]rtnl.Section, error) {
	deadline := time.Now().Add(350 * time.Millisecond)
	result := make(map[string]rtnl.Section, 5)
	for _, request := range []struct {
		name string
		kind uint16
		size int
	}{
		{"links", unix.RTM_GETLINK, unix.SizeofIfInfomsg},
		{"addresses", unix.RTM_GETADDR, unix.SizeofIfAddrmsg},
		{"routes", unix.RTM_GETROUTE, unix.SizeofRtMsg},
		{"rules", unix.RTM_GETRULE, unix.SizeofRtMsg},
		{"neighbors", unix.RTM_GETNEIGH, unix.SizeofNdMsg},
	} {
		entries, err := rtnl.Dump(request.kind, request.size, deadline)
		if err != nil {
			return nil, fmt.Errorf("dump %s: %w", request.name, err)
		}
		result[request.name] = entries
	}
	return result, nil
}
