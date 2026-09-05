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
		want     map[VMFamily]QuotaUsage
	}{
		{
			name:     "no families",
			families: sets.New[VMFamily](),
			want:     map[VMFamily]QuotaUsage{},
		},
		{
			name:     "multiple families all get max int64",
			families: sets.New[VMFamily]("standardEDSv6Family", "standardDDSv6Family"),
			want: map[VMFamily]QuotaUsage{
				"standardEDSv6Family": {Limit: math.MaxInt64},
				"standardDDSv6Family": {Limit: math.MaxInt64},
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
		families sets.Set[VMFamily]
		usages   map[VMFamily]QuotaUsage
		fetchErr error
		want     map[VMFamily]QuotaUsage
		wantErr  bool
	}{
		{
			name:     "preserves limit and current usage",
			families: sets.New[VMFamily]("standardEDSv6Family"),
			usages: map[VMFamily]QuotaUsage{
				"standardEDSv6Family": {Limit: 100, CurrentValue: 40},
			},
			want: map[VMFamily]QuotaUsage{"standardEDSv6Family": {Limit: 100, CurrentValue: 40}},
		},
		{
			name:     "preserves usage exceeding limit",
			families: sets.New[VMFamily]("standardEDSv6Family"),
			usages: map[VMFamily]QuotaUsage{
				"standardEDSv6Family": {Limit: 10, CurrentValue: 40},
			},
			want: map[VMFamily]QuotaUsage{"standardEDSv6Family": {Limit: 10, CurrentValue: 40}},
		},
		{
			name:     "propagates fetch error",
			families: sets.New[VMFamily]("standardEDSv6Family"),
			fetchErr: errors.New("boom"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetch := func(_ context.Context, gotFamilies sets.Set[VMFamily]) (map[VMFamily]QuotaUsage, error) {
				assert.True(t, gotFamilies.Equal(tt.families), "SubscriptionQuotaBudget passed unexpected families to fetch")
				return tt.usages, tt.fetchErr
			}
			budgets, err := SubscriptionQuotaBudget(context.Background(), tt.families, fetch)
			if tt.wantErr {
				assert.Error(t, err, "expected SubscriptionQuotaBudget to propagate fetch error")
				return
			}
			require.NoError(t, err, "SubscriptionQuotaBudget")
			assert.Equal(t, tt.want, budgets)
		})
	}
}
