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

package kusto

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitTimeRange(t *testing.T) {
	start := time.Date(2026, 9, 8, 18, 40, 15, 0, time.UTC)
	end := start.Add(12 * time.Minute)

	ranges := splitTimeRange(start, end, 5*time.Minute)
	require.Len(t, ranges, 3)
	assert.Equal(t, start, ranges[0].start)
	assert.Equal(t, start.Add(5*time.Minute-kustoTimePrecision), ranges[0].end)
	assert.Equal(t, ranges[0].end.Add(kustoTimePrecision), ranges[1].start)
	assert.Equal(t, end, ranges[2].end)
}

func TestBisectTimeRange(t *testing.T) {
	start := time.Date(2026, 9, 8, 18, 40, 15, 0, time.UTC)
	left, right, ok := bisectTimeRange(queryTimeRange{start: start, end: start.Add(time.Second)})
	require.True(t, ok)
	assert.Equal(t, left.end.Add(kustoTimePrecision), right.start)
	assert.Equal(t, start, left.start)
	assert.Equal(t, start.Add(time.Second), right.end)

	_, _, ok = bisectTimeRange(queryTimeRange{start: start, end: start})
	assert.False(t, ok)
}
