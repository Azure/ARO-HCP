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

package frontend

import (
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestChildDeleteAcceptanceDeadlines(t *testing.T) {
	for _, resource := range []string{"nodepool", "nodepool override", "externalauth"} {
		for _, attempt := range []string{"initial", "retry", "legacy retry"} {
			t.Run(resource+"/"+attempt, func(t *testing.T) {
				ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
				ctx = ContextWithCorrelationData(ctx, &coreapi.CorrelationData{})
				now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
				clock := clocktesting.NewFakeClock(now)
				db := corecosmosstoragetesting.NewMockResourcesDBClient()
				f := &Frontend{resourcesDBClient: db, azureLocation: coreapitesting.TestLocation, clock: clock}
				var deletionTime, dispatchTime, oldDeadline *metav1.Time
				if attempt != "initial" {
					deletionTime = &metav1.Time{Time: now.Add(-2 * time.Hour)}
					dispatchTime = &metav1.Time{Time: now.Add(-time.Hour)}
				}
				if attempt == "retry" {
					oldDeadline = &metav1.Time{Time: now.Add(-time.Minute)}
				}
				var deadline, storedDeletionTime, storedDispatchTime *metav1.Time
				var duration time.Duration
				if resource != "externalauth" {
					resourceID, err := azcorearm.ParseResourceID(coreapitesting.TestNodePoolResourceID)
					require.NoError(t, err)
					obj := coreapi.NewDefaultNodePool(resourceID, coreapitesting.TestLocation)
					obj.SetResourceID(obj.ID)
					obj.SetPartitionKey(obj.ID.SubscriptionID)
					obj.Properties.ProvisioningState = coreapi.ProvisioningStateFailed
					obj.ServiceProviderProperties.DeletionTimestamp = deletionTime
					obj.ServiceProviderProperties.ClusterServiceDeletionTimestamp = dispatchTime
					obj.ServiceProviderProperties.DeleteOperationCompletionDeadline = oldDeadline
					if resource == "nodepool override" {
						override := 2 * time.Hour
						obj.ServiceProviderProperties.DeleteOperationCompletionTimeout = &override
					}
					client := db.HCPClusters(obj.ID.SubscriptionID, obj.ID.ResourceGroupName).NodePools(obj.ID.Parent.Name)
					obj, err = client.Create(ctx, obj, nil)
					require.NoError(t, err)
					transaction := db.NewTransaction(obj.ID.SubscriptionID)
					require.NoError(t, f.addDeleteNodePoolToTransaction(ctx, nil, nil, transaction, obj))
					before, err := client.Get(ctx, obj.ID.Name)
					require.NoError(t, err)
					require.True(t, oldDeadline.Equal(before.ServiceProviderProperties.DeleteOperationCompletionDeadline), "deadline must not be stored before acceptance commits")
					_, err = transaction.Execute(ctx, nil)
					require.NoError(t, err)
					stored, err := client.Get(ctx, obj.ID.Name)
					require.NoError(t, err)
					require.Equal(t, coreapi.ProvisioningStateDeleting, stored.Properties.ProvisioningState)
					require.NotEmpty(t, stored.ServiceProviderProperties.ActiveOperationID)
					deadline = stored.ServiceProviderProperties.DeleteOperationCompletionDeadline
					storedDeletionTime = stored.ServiceProviderProperties.DeletionTimestamp
					storedDispatchTime = stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp
					duration = 50 * time.Minute
					if resource == "nodepool override" {
						duration = 2 * time.Hour
					}
				} else {
					obj := coreapitesting.MinimumValidExternalAuthTestCase()
					obj.SetResourceID(obj.ID)
					obj.SetPartitionKey(obj.ID.SubscriptionID)
					obj.Properties.ProvisioningState = coreapi.ProvisioningStateFailed
					obj.ServiceProviderProperties.DeletionTimestamp = deletionTime
					obj.ServiceProviderProperties.ClusterServiceDeletionTimestamp = dispatchTime
					obj.ServiceProviderProperties.DeleteOperationCompletionDeadline = oldDeadline
					client := db.HCPClusters(obj.ID.SubscriptionID, obj.ID.ResourceGroupName).ExternalAuth(obj.ID.Parent.Name)
					obj, err := client.Create(ctx, obj, nil)
					require.NoError(t, err)
					transaction := db.NewTransaction(obj.ID.SubscriptionID)
					require.NoError(t, f.addDeleteExternalAuthToTransaction(ctx, nil, nil, transaction, obj))
					before, err := client.Get(ctx, obj.ID.Name)
					require.NoError(t, err)
					require.True(t, oldDeadline.Equal(before.ServiceProviderProperties.DeleteOperationCompletionDeadline), "deadline must not be stored before acceptance commits")
					_, err = transaction.Execute(ctx, nil)
					require.NoError(t, err)
					stored, err := client.Get(ctx, obj.ID.Name)
					require.NoError(t, err)
					require.Equal(t, coreapi.ProvisioningStateDeleting, stored.Properties.ProvisioningState)
					require.NotEmpty(t, stored.ServiceProviderProperties.ActiveOperationID)
					deadline = stored.ServiceProviderProperties.DeleteOperationCompletionDeadline
					storedDeletionTime = stored.ServiceProviderProperties.DeletionTimestamp
					storedDispatchTime = stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp
					duration = 30 * time.Minute
				}
				require.NotNil(t, deadline)
				require.WithinDuration(t, now.Add(duration), deadline.Time, 0)
				if deletionTime == nil {
					deletionTime = &metav1.Time{Time: now}
				}
				require.True(t, deletionTime.Equal(storedDeletionTime), "retries must preserve deletion intent")
				require.True(t, dispatchTime.Equal(storedDispatchTime), "retries must preserve deletion progress")
			})
		}
	}
}
