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

package repository

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var nativeRequestFilterKey = regexp.MustCompile(`^filter\..+\.(clean|process|smudge)$`)

// NativeRequestPatch reads the complete tracked candidate without changing the index.
func (r *Repo) NativeRequestPatch(ctx context.Context, base string) ([]byte, error) {
	if !IsCommit(base) {
		return nil, fmt.Errorf("native request base must be a full lowercase commit SHA")
	}
	resolved, err := r.NativeGit(ctx, nil, "", "rev-parse", "--verify", base+"^{commit}")
	if err != nil || strings.TrimSpace(string(resolved)) != base {
		return nil, fmt.Errorf("inventory base commit is unavailable")
	}
	patch, err := r.NativeGit(ctx, nil, "", "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-color", "--no-relative", "--full-index", "--binary", "--diff-algorithm=myers", "--no-indent-heuristic", "--unified=3", "--inter-hunk-context=0", "--src-prefix=a/", "--dst-prefix=b/", "--output-indicator-new=+", "--output-indicator-old=-", "--output-indicator-context= ", base, "--", ".")
	if err != nil {
		return nil, fmt.Errorf("native request diff: %w", err)
	}
	return patch, nil
}
