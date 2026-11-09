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

package stackdesires

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func stackDesireWithAuth(t *testing.T, authType string) *coreapi.StackDesire {
	t.Helper()
	target, err := azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.Resources/deploymentStacks/s")
	require.NoError(t, err)
	return &coreapi.StackDesire{
		Spec: coreapi.StackDesireSpec{
			TargetResourceID: target,
			Authentication:   coreapi.StackDesireAuthentication{Type: authType},
		},
	}
}

func TestStackClientInsecureARMPermissionsManager(t *testing.T) {
	ctrl := gomock.NewController(t)
	builder := azureclient.NewMockInsecureARMPermissionsManagerClientBuilder(ctrl)
	builder.EXPECT().DeploymentStacksClient("sub-1").Return(nil, nil)

	c := &controller{insecureARMPermissionsManager: builder}
	desire := stackDesireWithAuth(t, coreapi.StackDesireAuthenticationTypeInsecureARMPermissionsManager)

	_, err := c.stackClient(context.Background(), desire)
	require.NoError(t, err)
}

func TestStackClientInsecureARMPermissionsManagerNotConfigured(t *testing.T) {
	c := &controller{}
	desire := stackDesireWithAuth(t, coreapi.StackDesireAuthenticationTypeInsecureARMPermissionsManager)

	_, err := c.stackClient(context.Background(), desire)
	require.Error(t, err)
}
