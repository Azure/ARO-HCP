// Copyright 2025 Microsoft Corporation
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

package version

// VersionInfo holds version information
type VersionInfo struct {
	Commit    string
	BuildDate string
}

var (
	// These variables are set during build via ldflags
	commit    = "unknown"
	buildDate = "unknown"
)

// GetVersionInfo returns the version information
func GetVersionInfo() VersionInfo {
	return VersionInfo{
		Commit:    commit,
		BuildDate: buildDate,
	}
}
