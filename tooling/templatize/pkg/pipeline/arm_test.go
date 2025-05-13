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

package pipeline

import (
	"context"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
)

func TestWaitForExistingDeployment(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name            string
		deploymentState []armresources.ProvisioningState
		missing         bool
		expectedError   *string
		expecetCallCnt  int
		returnError     *error
		timeout         int
	}{
		{
			name:            "Timeout",
			deploymentState: []armresources.ProvisioningState{"Running", "Running"},
			expectedError:   to.Ptr("timeout exeeded waiting for deployment test in rg rg"),
			expecetCallCnt:  1,
			timeout:         1,
		},
		{
			name:           "Missing Deployment",
			missing:        true,
			expecetCallCnt: 1,
			timeout:        1,
		},
		{
			name:            "Retrying",
			deploymentState: []armresources.ProvisioningState{"Running", "Running", "Succeeded"},
			expecetCallCnt:  2,
			timeout:         60,
		},
		{
			name:           "Handle Error",
			missing:        true,
			expecetCallCnt: 1,
			returnError:    to.Ptr(fmt.Errorf("test error")),
			expectedError:  to.Ptr("error getting deployment test error"),
			timeout:        1,
		},
	}

	for _, c := range cases {
		rg := "rg"
		depl := "test"
		callCnt := 0
		t.Run(c.name, func(t *testing.T) {
			a := armClient{
				deploymentRetryWaitTime: 1,
				GetDeployment: func(_ context.Context, rgName, deploymentName string) (armresources.DeploymentsClientGetResponse, error) {
					assert.Equal(t, rgName, rg)
					assert.Equal(t, deploymentName, depl)
					callCnt++

					var retErr error
					if c.returnError != nil {
						retErr = *c.returnError
					}

					returnObj := armresources.DeploymentsClientGetResponse{
						DeploymentExtended: armresources.DeploymentExtended{},
					}
					if !c.missing {
						returnObj.Properties = &armresources.DeploymentPropertiesExtended{
							ProvisioningState: &c.deploymentState[callCnt],
						}
					}
					return returnObj, retErr
				},
			}

			err := a.waitForExistingDeployment(ctx, c.timeout, rg, depl)
			if c.expectedError != nil {
				assert.Equal(t, err.Error(), *c.expectedError)
			}
			assert.Equal(t, callCnt, c.expecetCallCnt)
		})
	}
}
