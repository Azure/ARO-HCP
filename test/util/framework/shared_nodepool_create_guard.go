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

package framework

import (
	"context"
	"encoding/json"
	"fmt"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func (client *GuardedNegativeClusterClient) RejectNegativeNodePoolReplicas20240610(ctx context.Context, name string, nodePool hcpsdk20240610preview.NodePool) error {
	if nodePool.Properties == nil || nodePool.Properties.Replicas == nil || *nodePool.Properties.Replicas >= 0 {
		return fmt.Errorf("guarded node pool CREATE requires a negative replica count")
	}
	encoded, err := json.Marshal(nodePool)
	if err != nil {
		return fmt.Errorf("encode guarded node pool CREATE: %w", err)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		return fmt.Errorf("decode guarded node pool CREATE body: %w", err)
	}
	return client.rejectNodePoolCreate20240610(ctx, name, body)
}
