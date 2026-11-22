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

package github

import (
	"context"
	"fmt"
)

// BaseContains fails when commit is not part of base, so a stale or foreign
// checkout can never publish a diff computed against an unrelated branch.
func (c *Client) BaseContains(ctx context.Context, base, commit string) error {
	compare, _, err := c.api.Repositories.CompareCommits(ctx, c.owner, c.repo, commit, base, nil)
	if err != nil {
		return fmt.Errorf("compare %s with %s: %w", commit, base, err)
	}
	switch compare.GetStatus() {
	case "ahead", "identical":
		return nil
	}
	return fmt.Errorf("%s is not on %s (compare status %q)", commit, base, compare.GetStatus())
}
