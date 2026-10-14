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

import "fmt"

// runCapture backs the hidden "capture" helper subcommand. The real
// implementation (pkg/capture, file-suffix restricted to linux) relies on
// Linux-only netns and rtnetlink syscalls, so non-Linux builds fail loudly
// instead of pretending to support it.
func runCapture(string) error {
	return fmt.Errorf("capture subcommand is only supported on linux")
}
