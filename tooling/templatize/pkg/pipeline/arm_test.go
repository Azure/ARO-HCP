package pipeline

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"gotest.tools/v3/assert"
)

func TestWaitForExistingDeployment(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name            string
		deploymentState []armresources.ProvisioningState
		missing         bool
		expectedError   *string
		expecetCallCnt  int
		timeout         int
	}{
		{
			name:            "Timeout",
			deploymentState: []armresources.ProvisioningState{"Running", "Running"},
			expectedError:   to.Ptr("Timeout exeeded waiting for deployment test in rg rg"),
			expecetCallCnt:  1,
		},
		{
			name:           "Missing Deployment",
			missing:        true,
			expecetCallCnt: 1,
		},
		{
			name:            "Retrying",
			deploymentState: []armresources.ProvisioningState{"Running", "Running", "Succeeded"},
			expecetCallCnt:  2,
			timeout:         60,
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
					returnObj := armresources.DeploymentsClientGetResponse{
						DeploymentExtended: armresources.DeploymentExtended{},
					}
					if !c.missing {
						returnObj.DeploymentExtended.Properties = &armresources.DeploymentPropertiesExtended{
							ProvisioningState: &c.deploymentState[callCnt],
						}
					}
					return returnObj, nil
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
