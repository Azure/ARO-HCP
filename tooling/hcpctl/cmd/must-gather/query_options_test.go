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

package mustgather

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestValidate_ClusterIds_RejectsEmpty(t *testing.T) {
	opts := &RawQueryOptions{
		BaseGatherOptions: BaseGatherOptions{
			Kusto:        "test-kusto",
			Region:       "eastus",
			QueryTimeout: 5 * time.Minute,
			TimestampMin: time.Now().Add(-1 * time.Hour),
			TimestampMax: time.Now(),
		},
		SubscriptionID: "test-sub",
		ResourceGroup:  "test-rg",
		ClusterIds:     []string{"valid-id", ""},
	}

	_, err := opts.Validate(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--cluster-id was specified with an empty value")
}

func TestValidate_ClusterIds_AcceptsValid(t *testing.T) {
	opts := &RawQueryOptions{
		BaseGatherOptions: BaseGatherOptions{
			Kusto:        "test-kusto",
			Region:       "eastus",
			QueryTimeout: 5 * time.Minute,
			TimestampMin: time.Now().Add(-1 * time.Hour),
			TimestampMax: time.Now(),
		},
		SubscriptionID: "test-sub",
		ResourceGroup:  "test-rg",
		ClusterIds:     []string{"cluster-abc-123", "cluster-def-456"},
	}

	validated, err := opts.Validate(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, []string{"cluster-abc-123", "cluster-def-456"}, validated.QueryOptions.ClusterIds)
}
