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

package api

import "strings"

const (
	apiVersionOptionPrefix = "api-version="

	APIVersionV20240610Preview = "2024-06-10-preview"
	APIVersionV20251223Preview = "2025-12-23-preview"
	APIVersionV20260630Preview = "2026-06-30-preview"
)

// APIVersionOption returns the operation option string for an API version,
// suitable for inclusion in operation.Operation.Options.
func APIVersionOption(version string) string {
	return apiVersionOptionPrefix + version
}

// apiVersionFromOptions extracts the API version from operation options.
// Returns an empty string if no API version option is present.
func apiVersionFromOptions(options []string) string {
	for _, opt := range options {
		if after, ok := strings.CutPrefix(opt, apiVersionOptionPrefix); ok {
			return after
		}
	}
	return ""
}

func APIVersionEQ(options []string, version string) bool {
	v := apiVersionFromOptions(options)
	return v != "" && v == version
}

func APIVersionNE(options []string, version string) bool {
	v := apiVersionFromOptions(options)
	return v != "" && v != version
}

func APIVersionLT(options []string, version string) bool {
	v := apiVersionFromOptions(options)
	return v != "" && v < version
}

func APIVersionLE(options []string, version string) bool {
	v := apiVersionFromOptions(options)
	return v != "" && v <= version
}

func APIVersionGT(options []string, version string) bool {
	v := apiVersionFromOptions(options)
	return v != "" && v > version
}

func APIVersionGE(options []string, version string) bool {
	v := apiVersionFromOptions(options)
	return v != "" && v >= version
}
