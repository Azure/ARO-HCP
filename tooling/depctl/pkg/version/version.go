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

// Package version carries build provenance injected at link time.
package version

import "runtime"

// Values are injected at build time; the defaults identify a source build.
var (
	commit    = "unknown"
	buildDate = "unknown"
)

// VersionInfo is the provenance reported by the version command.
type VersionInfo struct {
	Commit    string
	BuildDate string
}

// GetVersionInfo returns the provenance this binary was built with.
func GetVersionInfo() VersionInfo {
	return VersionInfo{Commit: commit, BuildDate: buildDate}
}

// Platform returns the Go platform of the binary.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }
