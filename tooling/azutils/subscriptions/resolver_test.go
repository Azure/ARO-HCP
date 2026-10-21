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

package subscriptions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

func sub(name, id string) *armsubscriptions.Subscription {
	return &armsubscriptions.Subscription{
		DisplayName:    to.Ptr(name),
		SubscriptionID: to.Ptr(id),
	}
}

func TestBuildNameMap(t *testing.T) {
	tests := []struct {
		name    string
		subs    []*armsubscriptions.Subscription
		want    map[string]string
		wantErr string
	}{
		{
			name: "maps display names to IDs",
			subs: []*armsubscriptions.Subscription{
				sub("prod", "sub-1"),
				sub("staging", "sub-2"),
			},
			want: map[string]string{
				"prod":    "sub-1",
				"staging": "sub-2",
			},
		},
		{
			name: "duplicate display names return error",
			subs: []*armsubscriptions.Subscription{
				sub("prod", "sub-1"),
				sub("prod", "sub-2"),
			},
			wantErr: "ambiguous subscription name",
		},
		{
			name: "nil display name is skipped",
			subs: []*armsubscriptions.Subscription{
				{DisplayName: nil, SubscriptionID: to.Ptr("sub-1")},
				sub("prod", "sub-2"),
			},
			want: map[string]string{
				"prod": "sub-2",
			},
		},
		{
			name: "nil subscription ID is skipped",
			subs: []*armsubscriptions.Subscription{
				{DisplayName: to.Ptr("ghost"), SubscriptionID: nil},
				sub("prod", "sub-2"),
			},
			want: map[string]string{
				"prod": "sub-2",
			},
		},
		{
			name: "empty list returns empty map",
			subs: []*armsubscriptions.Subscription{},
			want: map[string]string{},
		},
		{
			name: "nil list returns empty map",
			subs: nil,
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildNameMap(tt.subs)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveNames(t *testing.T) {
	nameToID := map[string]string{
		"prod":    "sub-1",
		"staging": "sub-2",
		"dev":     "sub-3",
	}

	tests := []struct {
		name    string
		names   []string
		want    map[string]string
		wantErr string
	}{
		{
			name:  "resolves single name",
			names: []string{"prod"},
			want:  map[string]string{"prod": "sub-1"},
		},
		{
			name:  "resolves multiple names",
			names: []string{"prod", "dev"},
			want: map[string]string{
				"prod": "sub-1",
				"dev":  "sub-3",
			},
		},
		{
			name:    "missing name returns error",
			names:   []string{"prod", "nonexistent"},
			wantErr: `subscription "nonexistent" not found`,
		},
		{
			name:    "error includes visible count",
			names:   []string{"missing"},
			wantErr: "credential can see 3 subscriptions",
		},
		{
			name:  "empty names returns empty map",
			names: []string{},
			want:  map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveNames(nameToID, tt.names)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
