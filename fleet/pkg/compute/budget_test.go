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

package compute

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestUnlimitedBudget(t *testing.T) {
	tests := []struct {
		name     string
		families sets.Set[VMFamily]
		want     map[VMFamily]int64
	}{
		{
			name:     "no families",
			families: sets.New[VMFamily](),
			want:     map[VMFamily]int64{},
		},
		{
			name:     "multiple families all get max int64",
			families: sets.New[VMFamily]("standardEDSv6Family", "standardDDSv6Family"),
			want: map[VMFamily]int64{
				"standardEDSv6Family": math.MaxInt64,
				"standardDDSv6Family": math.MaxInt64,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			budgets, err := UnlimitedBudget(context.Background(), tt.families, nil)
			require.NoError(t, err, "UnlimitedBudget")
			assert.Equal(t, tt.want, budgets)
		})
	}
}

func TestSubscriptionQuotaBudget(t *testing.T) {
	tests := []struct {
		name     string
		usages   map[VMFamily]QuotaUsage
		fetchErr error
		want     map[VMFamily]int64
		wantErr  bool
	}{
		{
			name: "limit minus current usage",
			usages: map[VMFamily]QuotaUsage{
				"standardEDSv6Family": {Limit: 100, CurrentValue: 40},
			},
			want: map[VMFamily]int64{"standardEDSv6Family": 60},
		},
		{
			name: "current usage exceeding limit floors at zero",
			usages: map[VMFamily]QuotaUsage{
				"standardEDSv6Family": {Limit: 10, CurrentValue: 40},
			},
			want: map[VMFamily]int64{"standardEDSv6Family": 0},
		},
		{
			name:     "propagates fetch error",
			fetchErr: errors.New("boom"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetch := func(sets.Set[VMFamily]) (map[VMFamily]QuotaUsage, error) {
				return tt.usages, tt.fetchErr
			}
			budgets, err := SubscriptionQuotaBudget(context.Background(), sets.New[VMFamily](), fetch)
			if tt.wantErr {
				assert.Error(t, err, "expected SubscriptionQuotaBudget to propagate fetch error")
				return
			}
			require.NoError(t, err, "SubscriptionQuotaBudget")
			assert.Equal(t, tt.want, budgets)
		})
	}
}
