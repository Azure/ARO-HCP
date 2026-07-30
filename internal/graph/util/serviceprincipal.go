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

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/models"
)

// ServicePrincipal represents a Microsoft Entra service principal
type ServicePrincipal struct {
	ID          string `json:"id"`
	AppID       string `json:"appId"`
	DisplayName string `json:"displayName"`
}

// CreateServicePrincipal creates a new Microsoft Entra service principal.
// Transient errors and eventual-consistency delays are retried by the
// transport-level graphRetryHandler middleware.
func (c *Client) CreateServicePrincipal(ctx context.Context, appId string) (*ServicePrincipal, error) {
	sp := models.NewServicePrincipal()
	sp.SetAppId(&appId)

	createdSp, err := c.graphClient.ServicePrincipals().Post(ctx, sp, nil)
	if err != nil {
		return nil, fmt.Errorf("create service principal: %w", odataErrorWithDiagnostics(err))
	}

	return &ServicePrincipal{
		ID:    *createdSp.GetId(),
		AppID: *createdSp.GetAppId(),
	}, nil
}
