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

package status

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/statusutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	kubeapplierlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

const (
	testComponentName      = "console"
	testComponentNamespace = "openshift-console"
)

func newTestExternalAuthForAvailable(opts ...func(*coreapi.HCPOpenShiftClusterExternalAuth)) *coreapi.HCPOpenShiftClusterExternalAuth {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName +
			"/externalAuths/" + statusutils.TestExternalAuthName,
	))
	ea := &coreapi.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ProxyResource: coreapi.ProxyResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: statusutils.TestExternalAuthName,
				Type: resourceID.ResourceType.String(),
			},
		},
		Properties: coreapi.HCPOpenShiftClusterExternalAuthProperties{
			Clients: []coreapi.ExternalAuthClientProfile{
				{
					Component: coreapi.ExternalAuthClientComponentProfile{
						Name:                testComponentName,
						AuthClientNamespace: testComponentNamespace,
					},
					Type: metadataapi.ExternalAuthClientTypeConfidential,
				},
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
			ClusterServiceID: ptrTo(metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))),
		},
	}
	for _, opt := range opts {
		opt(ea)
	}
	return ea
}

func newTestServiceProviderExternalAuth(opts ...func(*coreapi.ServiceProviderExternalAuth)) *coreapi.ServiceProviderExternalAuth {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName +
			"/externalAuths/" + statusutils.TestExternalAuthName +
			"/serviceProviderExternalAuths/" + coreapi.ServiceProviderExternalAuthResourceName,
	))
	spea := &coreapi.ServiceProviderExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
	for _, opt := range opts {
		opt(spea)
	}
	return spea
}

func newHostedClusterReadDesire(t *testing.T, hc *v1beta1.HostedCluster) *kubeapplierapi.ReadDesire {
	t.Helper()
	raw, err := json.Marshal(hc)
	require.NoError(t, err)
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
			statusutils.TestSubscriptionID, statusutils.TestResourceGroupName, statusutils.TestClusterName,
			kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster)))
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Status: kubeapplierapi.ReadDesireStatus{
			Conditions: []metav1.Condition{
				{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue, Reason: kubeapplierapi.ConditionReasonNoErrors},
			},
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}

func ptrTo[T any](v T) *T { return &v }

func TestExternalAuthAvailableController_SyncOnce(t *testing.T) {
	parentClusterID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName,
	))

	// consoleCondType is the per-client condition type for the default
	// "console" component.
	consoleCondType := coreapi.PerClientAvailableConditionType(testComponentName)

	type expectedCondition struct {
		condType string
		status   metav1.ConditionStatus
		reason   string
		message  string
	}

	tests := []struct {
		name string

		externalAuth                *coreapi.HCPOpenShiftClusterExternalAuth
		serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth
		hostedCluster               *v1beta1.HostedCluster

		expectNoWrite      bool
		expectConditions   []expectedCondition
		expectNoConditions bool
	}{
		{
			name: "skip when external auth is being deleted",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				now := metav1.Now()
				ea.ServiceProviderProperties.DeletionTimestamp = &now
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			expectNoWrite:               true,
		},
		{
			name: "skip when external auth has no ClusterServiceID",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.ClusterServiceID = nil
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			expectNoWrite:               true,
		},
		{
			name:                        "skip when SPEA not yet created",
			externalAuth:                newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: nil,
			expectNoWrite:               true,
		},
		{
			name: "no clients defined -> no conditions written",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Clients = nil
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			expectNoConditions:          true,
		},
		{
			name:                        "confidential client: HC not found -> False/HostedClusterNotReady",
			externalAuth:                newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			hostedCluster:               nil,
			expectConditions: []expectedCondition{{
				condType: consoleCondType,
				status:   metav1.ConditionFalse,
				reason:   coreapi.ExternalAuthReasonHostedClusterNotReady,
				message:  "Waiting for HostedCluster to be observed",
			}},
		},
		{
			name:                        "confidential client: HC found, no Configuration -> Unknown/HostedClusterNotReady",
			externalAuth:                newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{Configuration: nil},
			},
			expectConditions: []expectedCondition{{
				condType: consoleCondType,
				status:   metav1.ConditionUnknown,
				reason:   coreapi.ExternalAuthReasonHostedClusterNotReady,
				message:  "HostedCluster authentication status not yet available",
			}},
		},
		{
			name:                        "confidential client: Available True -> ConsoleAvailable True",
			externalAuth:                newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{{
								ComponentName:      testComponentName,
								ComponentNamespace: testComponentNamespace,
								Conditions: []metav1.Condition{
									{Type: "Available", Status: metav1.ConditionTrue, Reason: coreapi.HostedClusterOIDCConfigAvailable},
									{Type: "Degraded", Status: metav1.ConditionFalse, Reason: coreapi.HostedClusterOIDCConfigAvailable},
								},
							}},
						},
					},
				},
			},
			expectConditions: []expectedCondition{{
				condType: consoleCondType,
				status:   metav1.ConditionTrue,
				reason:   coreapi.ExternalAuthReasonOIDCConfigAvailable,
			}},
		},
		{
			name:                        "confidential client: Degraded OIDCClientSecretGet -> AwaitingSecret",
			externalAuth:                newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{{
								ComponentName:      testComponentName,
								ComponentNamespace: testComponentNamespace,
								Conditions: []metav1.Condition{
									{Type: "Available", Status: metav1.ConditionFalse, Reason: "SomeReason"},
									{Type: "Degraded", Status: metav1.ConditionTrue, Reason: coreapi.HostedClusterOIDCClientSecretGet, Message: "secret not found"},
								},
							}},
						},
					},
				},
			},
			expectConditions: []expectedCondition{{
				condType: consoleCondType,
				status:   metav1.ConditionFalse,
				reason:   coreapi.ExternalAuthConfidentialReasonAwaitingSecret,
				message:  "The external auth provider is waiting for the client secret to be created in the openshift-config namespace",
			}},
		},
		{
			name:                        "confidential client: Degraded with other reason -> forward reason",
			externalAuth:                newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{{
								ComponentName:      testComponentName,
								ComponentNamespace: testComponentNamespace,
								Conditions: []metav1.Condition{
									{Type: "Degraded", Status: metav1.ConditionTrue, Reason: "OtherDegraded", Message: "something else is wrong"},
								},
							}},
						},
					},
				},
			},
			expectConditions: []expectedCondition{{
				condType: consoleCondType,
				status:   metav1.ConditionFalse,
				reason:   "OtherDegraded",
				message:  "something else is wrong",
			}},
		},
		{
			name:                        "confidential client: Available False, no Degraded -> forward reason",
			externalAuth:                newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{{
								ComponentName:      testComponentName,
								ComponentNamespace: testComponentNamespace,
								Conditions: []metav1.Condition{
									{Type: "Available", Status: metav1.ConditionFalse, Reason: "SomeReason", Message: "not available yet"},
									{Type: "Degraded", Status: metav1.ConditionFalse, Reason: "SomeReason"},
								},
							}},
						},
					},
				},
			},
			expectConditions: []expectedCondition{{
				condType: consoleCondType,
				status:   metav1.ConditionFalse,
				reason:   "SomeReason",
				message:  "not available yet",
			}},
		},
		{
			name:                        "confidential client: no matching OIDC status -> HostedClusterNotReady",
			externalAuth:                newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{{
								ComponentName:      "other-component",
								ComponentNamespace: "other-namespace",
								Conditions: []metav1.Condition{
									{Type: "Available", Status: metav1.ConditionTrue, Reason: coreapi.HostedClusterOIDCConfigAvailable},
								},
							}},
						},
					},
				},
			},
			expectConditions: []expectedCondition{{
				condType: consoleCondType,
				status:   metav1.ConditionFalse,
				reason:   coreapi.ExternalAuthReasonHostedClusterNotReady,
				message:  "OIDC client status not yet reported by the hosted cluster",
			}},
		},
		{
			name: "public client always available regardless of HC status",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Clients = []coreapi.ExternalAuthClientProfile{{
					Component: coreapi.ExternalAuthClientComponentProfile{
						Name:                "cli",
						AuthClientNamespace: "openshift-console",
					},
					Type: metadataapi.ExternalAuthClientTypePublic,
				}}
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			hostedCluster:               nil, // HC not even found
			expectConditions: []expectedCondition{{
				condType: coreapi.PerClientAvailableConditionType("cli"),
				status:   metav1.ConditionTrue,
				reason:   coreapi.ExternalAuthReasonOIDCConfigAvailable,
				message:  "Public client does not require a secret",
			}},
		},
		{
			name: "public client available even when HC reports degraded",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Clients = []coreapi.ExternalAuthClientProfile{{
					Component: coreapi.ExternalAuthClientComponentProfile{
						Name:                "cli",
						AuthClientNamespace: "openshift-console",
					},
					Type: metadataapi.ExternalAuthClientTypePublic,
				}}
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{{
								ComponentName:      "cli",
								ComponentNamespace: "openshift-console",
								Conditions: []metav1.Condition{
									{Type: "Degraded", Status: metav1.ConditionTrue, Reason: coreapi.HostedClusterOIDCClientSecretGet},
								},
							}},
						},
					},
				},
			},
			expectConditions: []expectedCondition{{
				condType: coreapi.PerClientAvailableConditionType("cli"),
				status:   metav1.ConditionTrue,
				reason:   coreapi.ExternalAuthReasonOIDCConfigAvailable,
				message:  "Public client does not require a secret",
			}},
		},
		{
			name: "multi-client: console (confidential) awaiting + cli (public) available",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Clients = []coreapi.ExternalAuthClientProfile{
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "console",
							AuthClientNamespace: "openshift-console",
						},
						Type: metadataapi.ExternalAuthClientTypeConfidential,
					},
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "cli",
							AuthClientNamespace: "openshift-console",
						},
						Type: metadataapi.ExternalAuthClientTypePublic,
					},
				}
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{
								{
									ComponentName:      "console",
									ComponentNamespace: "openshift-console",
									Conditions: []metav1.Condition{
										{Type: "Degraded", Status: metav1.ConditionTrue, Reason: coreapi.HostedClusterOIDCClientSecretGet, Message: "secret not found"},
									},
								},
							},
						},
					},
				},
			},
			expectConditions: []expectedCondition{
				{
					condType: coreapi.PerClientAvailableConditionType("console"),
					status:   metav1.ConditionFalse,
					reason:   coreapi.ExternalAuthConfidentialReasonAwaitingSecret,
					message:  "The external auth provider is waiting for the client secret to be created in the openshift-config namespace",
				},
				{
					condType: coreapi.PerClientAvailableConditionType("cli"),
					status:   metav1.ConditionTrue,
					reason:   coreapi.ExternalAuthReasonOIDCConfigAvailable,
					message:  "Public client does not require a secret",
				},
			},
		},
		{
			name: "multi-client: both available",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Clients = []coreapi.ExternalAuthClientProfile{
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "console",
							AuthClientNamespace: "openshift-console",
						},
						Type: metadataapi.ExternalAuthClientTypeConfidential,
					},
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "cli",
							AuthClientNamespace: "openshift-console",
						},
						Type: metadataapi.ExternalAuthClientTypePublic,
					},
				}
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{
								{
									ComponentName:      "console",
									ComponentNamespace: "openshift-console",
									Conditions: []metav1.Condition{
										{Type: "Available", Status: metav1.ConditionTrue, Reason: coreapi.HostedClusterOIDCConfigAvailable},
										{Type: "Degraded", Status: metav1.ConditionFalse, Reason: coreapi.HostedClusterOIDCConfigAvailable},
									},
								},
							},
						},
					},
				},
			},
			expectConditions: []expectedCondition{
				{
					condType: coreapi.PerClientAvailableConditionType("console"),
					status:   metav1.ConditionTrue,
					reason:   coreapi.ExternalAuthReasonOIDCConfigAvailable,
				},
				{
					condType: coreapi.PerClientAvailableConditionType("cli"),
					status:   metav1.ConditionTrue,
					reason:   coreapi.ExternalAuthReasonOIDCConfigAvailable,
					message:  "Public client does not require a secret",
				},
			},
		},
		{
			name:         "no-op when SPEA conditions already match",
			externalAuth: newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{{
					Type:   consoleCondType,
					Status: metav1.ConditionTrue,
					Reason: coreapi.ExternalAuthReasonOIDCConfigAvailable,
				}}
			}),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{{
								ComponentName:      testComponentName,
								ComponentNamespace: testComponentNamespace,
								Conditions: []metav1.Condition{
									{Type: "Available", Status: metav1.ConditionTrue, Reason: coreapi.HostedClusterOIDCConfigAvailable},
									{Type: "Degraded", Status: metav1.ConditionFalse, Reason: coreapi.HostedClusterOIDCConfigAvailable},
								},
							}},
						},
					},
				},
			},
			expectNoWrite: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			parentCluster := &coreapi.HCPOpenShiftCluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   parentClusterID,
					PartitionKey: strings.ToLower(parentClusterID.SubscriptionID),
				},
				TrackedResource: coreapi.TrackedResource{
					Resource: coreapi.Resource{ID: parentClusterID, Name: statusutils.TestClusterName, Type: parentClusterID.ResourceType.String()},
				},
			}

			seed := []any{parentCluster, tc.externalAuth}
			if tc.serviceProviderExternalAuth != nil {
				seed = append(seed, tc.serviceProviderExternalAuth)
			}
			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, seed)
			require.NoError(t, err)

			var readDesireLister kubeapplierlistertesting.SliceReadDesireLister
			if tc.hostedCluster != nil {
				readDesireLister.Desires = []*kubeapplierapi.ReadDesire{
					newHostedClusterReadDesire(t, tc.hostedCluster),
				}
			}

			syncer := &externalAuthAvailableController{
				externalAuthLister:                &corelistertesting.DBExternalAuthLister{ResourcesDBClient: mockDB},
				serviceProviderExternalAuthLister: &corelistertesting.DBServiceProviderExternalAuthLister{ResourcesDBClient: mockDB},
				readDesireLister:                  &readDesireLister,
				resourcesDBClient:                 mockDB,
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPExternalAuthKey{
				SubscriptionID:      statusutils.TestSubscriptionID,
				ResourceGroupName:   statusutils.TestResourceGroupName,
				HCPClusterName:      statusutils.TestClusterName,
				HCPExternalAuthName: statusutils.TestExternalAuthName,
			})
			require.NoError(t, err)

			if tc.expectNoWrite {
				if tc.serviceProviderExternalAuth == nil {
					return
				}
				updatedSPEA, err := mockDB.ServiceProviderExternalAuths(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName, statusutils.TestClusterName, statusutils.TestExternalAuthName).Get(ctx, coreapi.ServiceProviderExternalAuthResourceName)
				require.NoError(t, err)
				assert.Equal(t, tc.serviceProviderExternalAuth.Status.Conditions, updatedSPEA.Status.Conditions,
					"conditions should not have changed")
				return
			}

			updatedSPEA, err := mockDB.ServiceProviderExternalAuths(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName, statusutils.TestClusterName, statusutils.TestExternalAuthName).Get(ctx, coreapi.ServiceProviderExternalAuthResourceName)
			require.NoError(t, err)

			if tc.expectNoConditions {
				assert.Empty(t, updatedSPEA.Status.Conditions, "expected no conditions on SPEA")
				return
			}

			for _, expected := range tc.expectConditions {
				cond := apimeta.FindStatusCondition(updatedSPEA.Status.Conditions, expected.condType)
				require.NotNil(t, cond, fmt.Sprintf("controller must set the %s condition on SPEA", expected.condType))
				assert.Equal(t, expected.status, cond.Status, "status for %s", expected.condType)
				assert.Equal(t, expected.reason, cond.Reason, "reason for %s", expected.condType)
				if expected.message != "" {
					assert.Equal(t, expected.message, cond.Message, "message for %s", expected.condType)
				}
			}
		})
	}
}

