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

package bincache

import (
	"context"
)

// MustGatherClean is the BinarySpec for the openshift/must-gather-clean tool.
var MustGatherClean = BinarySpec{
	Name:                "must-gather-clean",
	Owner:               "openshift",
	Repo:                "must-gather-clean",
	AssetPattern:        "{name}-{os}-{arch}.tar.gz",
	WindowsAssetPattern: "{name}-{os}-{arch}.exe.zip",
	FlagHint:            "--must-gather-clean-binary",
	ChecksumAsset:       "SHA256_SUM",
}

// ResolveMustGatherClean resolves the path to the must-gather-clean binary.
// If explicitPath is non-empty, it verifies the file exists and returns it.
// Otherwise, it downloads the latest release and caches it locally.
func ResolveMustGatherClean(ctx context.Context, explicitPath string) (string, error) {
	return Resolve(ctx, MustGatherClean, explicitPath)
}
