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

package cijoboutcomes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCacheCapacityTTLAndNonSliding(t *testing.T) {
	now := time.Now()
	c := newTTLCache[string, string](2, 15*time.Minute)
	c.now = func() time.Time { return now }
	c.put("a", "original")
	now = now.Add(time.Minute)
	c.put("b", "second")
	now = now.Add(13 * time.Minute)
	value, ok := c.get("a")
	require.True(t, ok)
	require.Equal(t, "original", value)
	c.put("a", "replacement")
	now = now.Add(time.Minute)
	_, ok = c.get("a")
	require.False(t, ok, "neither reads nor duplicate puts extend TTL")
	require.Equal(t, 1, c.size())
	c.put("c", "third")
	c.put("d", "fourth")
	_, ok = c.get("b")
	require.False(t, ok, "oldest entry is evictable even before expiry")
	require.Equal(t, 2, c.size())
	now = now.Add(15 * time.Minute)
	require.Zero(t, c.size())
}
