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

package preview

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"

	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/dashboard"
)

const (
	// previewUIDHashLength is the number of hexadecimal characters of the sha1
	// that ends a truncated preview UID.
	previewUIDHashLength = 6
	// previewUIDKeptLength is how much of the full preview UID survives
	// truncation. It leaves room for the separator and the sha1 that follow.
	previewUIDKeptLength = dashboard.MaxUIDLength - previewUIDHashLength - 1
)

// buildFolderUID names the folder that holds the preview of one pull request.
func buildFolderUID(pullNumber int) string {
	return fmt.Sprintf("pr-%d", pullNumber)
}

// buildFolderTitle titles the folder that holds the preview of one pull request.
func buildFolderTitle(pullNumber int) string {
	return fmt.Sprintf("PR-%d", pullNumber)
}

// buildPreviewUID derives the UID a dashboard is previewed under from the UID it
// carries. The prefix keeps a preview apart from the dashboard that main
// deployed, and apart from the preview of every other pull request.
//
// Grafana rejects a UID longer than dashboard.MaxUIDLength, so a longer result
// keeps its leading bytes and ends with a short sha1 of the whole. The sha1
// makes the shortened UID stable across runs, so that a second push updates the
// same dashboard instead of adding one.
func buildPreviewUID(pullNumber int, originalUID string) string {
	uid := fmt.Sprintf("pr-%d-%s", pullNumber, originalUID)
	if len(uid) <= dashboard.MaxUIDLength {
		return uid
	}

	// the following truncates UIDs that surpass Grafana's 40 character limit
	// e.g. pr-12345-1813f692a8e4ac77155348d4c7d2fba8 (41 characters) would become
	// pr-12345-1813f692a8e4ac77155348d4-c52564 (40 characters)
	sum := sha1.Sum([]byte(uid))
	return uid[:previewUIDKeptLength] + "-" + hex.EncodeToString(sum[:])[:previewUIDHashLength]
}
