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

package ips

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v8"
	"github.com/stretchr/testify/assert"
)

func TestExtractServiceTags(t *testing.T) {
	tests := []struct {
		name   string
		ipTags []*armnetwork.IPTag
		want   []IPTag
	}{
		{
			name:   "empty",
			ipTags: []*armnetwork.IPTag{},
			want:   []IPTag{},
		},
		{
			name: "single",
			ipTags: []*armnetwork.IPTag{
				{
					IPTagType: to.Ptr("FirstPartyUsage"),
					Tag:       to.Ptr("NonProd"),
				},
			},
			want: []IPTag{
				{
					ServiceTagType:  "FirstPartyUsage",
					ServiceTagValue: "NonProd",
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := extractServiceTags(test.ipTags)
			assert.Equal(t, test.want, got)
		})
	}
}
