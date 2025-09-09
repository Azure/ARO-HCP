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
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/models"
)

// ServicePrincipal represents a Microsoft Entra service principal
type ServicePrincipal struct {
	ID          string `json:"id"`
	AppID       string `json:"appId"`
	DisplayName string `json:"displayName"`
}

// CreateServicePrincipal creates a new Microsoft Entra service principal
func (c *Client) CreateServicePrincipal(ctx context.Context, appId string) (*ServicePrincipal, error) {
	sp := models.NewServicePrincipal()
	sp.SetAppId(&appId)

	// Eventual consistency of MSGraph means sometimes you have to wait until the
	// App registration is propagated before creating a service principal
	var createdSp models.ServicePrincipalable
	pollErr := wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		var err error
		createdSp, err = c.graphClient.ServicePrincipals().Post(ctx, sp, nil)
		if err != nil {
			// Retry on error
			return false, nil
		}
		return true, nil
	})
	if pollErr != nil {
		return nil, fmt.Errorf("failed to create service principal: %w", pollErr)
	}

	return &ServicePrincipal{
		ID:    *createdSp.GetId(),
		AppID: *createdSp.GetAppId(),
	}, nil
}
