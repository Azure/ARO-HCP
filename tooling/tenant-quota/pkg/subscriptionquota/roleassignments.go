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

package subscriptionquota

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

type roleAssignmentMetricsGetter interface {
	Get(ctx context.Context) (roleAssignmentMetrics, error)
}

type roleAssignmentMetricsClientFactory func(
	subscriptionID string,
	credential azcore.TokenCredential,
) (roleAssignmentMetricsGetter, error)

// RoleAssignmentSource retrieves role assignment usage and limits per
// subscription from the ARM Authorization API.
type RoleAssignmentSource struct {
	newClient roleAssignmentMetricsClientFactory
}

func NewRoleAssignmentSource() *RoleAssignmentSource {
	return &RoleAssignmentSource{
		newClient: func(subscriptionID string, credential azcore.TokenCredential) (roleAssignmentMetricsGetter, error) {
			return newRoleAssignmentMetricsClient(subscriptionID, credential, nil)
		},
	}
}

func (s *RoleAssignmentSource) Name() string     { return "rbac" }
func (s *RoleAssignmentSource) IsRegional() bool { return false }

func (s *RoleAssignmentSource) Collect(ctx context.Context, cred *azidentity.ClientSecretCredential,
	subscriptionID string, _ string) ([]QuotaResult, []error) {

	client, err := s.newClient(subscriptionID, cred)
	if err != nil {
		return nil, []error{fmt.Errorf("create role assignment metrics client: %w", err)}
	}

	metrics, err := client.Get(ctx)
	if err != nil {
		return nil, []error{fmt.Errorf("get role assignment metrics: %w", err)}
	}

	return []QuotaResult{{
		QuotaName:      "roleAssignments",
		LocalizedName:  "Role Assignments",
		CurrentValue:   float64(metrics.currentCount),
		Limit:          float64(metrics.limit),
		SubscriptionID: subscriptionID,
		Region:         "",
	}}, nil
}
