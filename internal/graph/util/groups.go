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

package util

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/models"
)

// Group represents a Microsoft Entra group
type Group struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

// CreateSecurityGroup creates a new security group
func (c *Client) CreateSecurityGroup(ctx context.Context, displayName, description string) (*Group, error) {
	group := models.NewGroup()
	group.SetDisplayName(&displayName)
	group.SetDescription(&description)
	group.SetMailEnabled(ptr.To(false))
	group.SetMailNickname(ptr.To(strings.ReplaceAll(strings.ToLower(displayName), " ", "-")))
	group.SetSecurityEnabled(ptr.To(true))

	createdGroup, err := c.graphClient.Groups().Post(ctx, group, nil)
	if err != nil {
		return nil, fmt.Errorf("create security group: %w", err)
	}

	return &Group{
		ID:          *createdGroup.GetId(),
		DisplayName: *createdGroup.GetDisplayName(),
		Description: *createdGroup.GetDescription(),
	}, nil
}
