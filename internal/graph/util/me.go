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

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/me"
)

// User represents the current authenticated user
type User struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	UserPrincipalName string `json:"userPrincipalName"`
	Mail              string `json:"mail"`
}

// GetCurrentUser retrieves information about the current authenticated user
func (c *Client) GetCurrentUser(ctx context.Context) (*User, error) {
	queryParams := &me.MeRequestBuilderGetQueryParameters{
		Select: []string{"id", "displayName", "userPrincipalName", "mail"},
	}
	config := &me.MeRequestBuilderGetRequestConfiguration{
		QueryParameters: queryParams,
	}

	user, err := c.graphClient.Me().Get(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("get current user: %w", err)
	}

	return &User{
		ID:                *user.GetId(),
		DisplayName:       *user.GetDisplayName(),
		UserPrincipalName: *user.GetUserPrincipalName(),
		Mail:              *user.GetMail(),
	}, nil
}
