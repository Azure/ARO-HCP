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

import (
	"strings"
)

// APIVersion represents an Azure ARM API version following the uniform versioning
// guidelines (https://github.com/Azure/azure-rest-api-specs/blob/main/documentation/uniform-versioning.md).
//
// Format: YYYY-MM-DD[-preview]
//
// Ordering: versions are ordered chronologically by date. The uniform versioning
// spec requires that each new API version uses a date later than all preceding
// versions, and that stable and preview versions cannot share the same date
// (promoting preview to stable requires incrementing the date by at least one day).
//
// The comparison functions (LT, LE, GT, GE) implement semantic ordering that
// additionally handles the hypothetical case of a same-date stable and preview
// version by ranking stable after preview. This guards against a violation of the
// uniform versioning guidelines where the date is not incremented when moving from
// preview to stable — naive lexicographic comparison would incorrectly place the
// stable version before the preview version in that scenario.
type APIVersion string

const (
	apiVersionOptionPrefix = "api-version="

	APIVersionV20240610Preview APIVersion = "2024-06-10-preview"
	APIVersionV20251223Preview APIVersion = "2025-12-23-preview"
	APIVersionV20260630Preview APIVersion = "2026-06-30-preview"
)

// APIVersionOption returns the operation option string for an API version,
// suitable for inclusion in operation.Operation.Options.
func APIVersionOption(version APIVersion) string {
	return apiVersionOptionPrefix + string(version)
}

// APIVersionFromOptions extracts the API version from operation options.
// Returns an empty string if no API version option is present.
func APIVersionFromOptions(options []string) APIVersion {
	for _, opt := range options {
		if version, ok := strings.CutPrefix(opt, apiVersionOptionPrefix); ok {
			return APIVersion(version)
		}
	}
	return APIVersion("")
}

func (v APIVersion) EQ(other APIVersion) bool {
	return v == other
}

func (v APIVersion) NE(other APIVersion) bool {
	return v != other
}

func (v APIVersion) LT(other APIVersion) bool {
	return v.compare(other) < 0
}

func (v APIVersion) LE(other APIVersion) bool {
	return v.compare(other) <= 0
}

func (v APIVersion) GT(other APIVersion) bool {
	return v.compare(other) > 0
}

func (v APIVersion) GE(other APIVersion) bool {
	return v.compare(other) >= 0
}

// compare returns -1, 0, or 1 comparing v and other semantically.
// The date portion (YYYY-MM-DD) is compared first. On equal dates, preview
// (suffix found by CutSuffix) ranks before stable (no suffix).
//
// Examples:
//
//	APIVersion("2024-06-10-preview").compare("2026-06-30-preview") // -1 (earlier date)
//	APIVersion("2026-06-30-preview").compare("2026-06-30-preview") //  0 (same)
//	APIVersion("2026-06-30-preview").compare("2024-06-10-preview") //  1 (later date)
//	APIVersion("2026-06-30-preview").compare("2026-06-30")         // -1 (preview < stable)
//	APIVersion("2026-06-30").compare("2026-06-30-preview")         //  1 (stable > preview)
func (v APIVersion) compare(other APIVersion) int {
	dateA, suffixA := strings.CutSuffix(string(v), "-preview")
	dateB, suffixB := strings.CutSuffix(string(other), "-preview")

	if cmp := strings.Compare(dateA, dateB); cmp != 0 {
		return cmp
	}

	switch {
	case suffixA && suffixB:
		return 0
	case suffixA:
		return -1
	case suffixB:
		return 1
	default:
		return 0
	}
}
