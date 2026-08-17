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

package versionrollout

import (
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectVersionWithOffset(t *testing.T) {
	t.Parallel()
	// deliberately unsorted
	candidates := []semver.Version{
		semver.MustParse("4.21.4"),
		semver.MustParse("4.21.8"),
		semver.MustParse("4.21.6"),
	}
	tests := []struct {
		name   string
		cands  []semver.Version
		offset int
		want   *semver.Version
	}{
		{name: "empty", cands: nil, offset: 2, want: nil},
		{name: "offset 0 is latest", cands: candidates, offset: 0, want: v("4.21.8")},
		{name: "offset 1", cands: candidates, offset: 1, want: v("4.21.6")},
		{name: "offset 2", cands: candidates, offset: 2, want: v("4.21.4")},
		{name: "offset beyond clamps to oldest", cands: candidates, offset: 9, want: v("4.21.4")},
		{name: "negative offset clamps to latest", cands: candidates, offset: -1, want: v("4.21.8")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := selectVersionWithOffset(tc.cands, tc.offset)
			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.True(t, got.EQ(*tc.want), "got %v want %v", got, tc.want)
		})
	}
}
