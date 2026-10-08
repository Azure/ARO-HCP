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

//go:build !linux

package cmd

import (
	"context"

	"github.com/go-logr/logr"
)

// runNetwatch backs the netwatch control loop. The real implementation
// (pkg/netwatch, build-tagged linux) relies on Linux-only netlink/conntrack
// syscalls, so non-Linux builds log that the feature is disabled and idle
// until ctx is cancelled, rather than pretending to watch anything.
func runNetwatch(ctx context.Context, logger logr.Logger) error {
	logger.Info("netwatch is only supported on linux; disabled on this platform")
	<-ctx.Done()
	return ctx.Err()
}
