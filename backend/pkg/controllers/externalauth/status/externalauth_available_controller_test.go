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
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
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
	ServiceProviderExternalAuth := &coreapi.ServiceProviderExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
	for _, opt := range opts {
		opt(ServiceProviderExternalAuth)
	}
	return ServiceProviderExternalAuth
}

func newHostedClusterReadDesire(t *testing.T, hc *v1beta1.HostedCluster) *kubeapplierapi.ReadDesire {
	t.Helper()
	raw, err := json.Marshal(hc)
	require.NoError(t, err)
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(
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

	type expectedCondition struct {
		status  metav1.ConditionStatus
		reason  string
		message string
	}

	tests := []struct {
		name string

		externalAuth                *coreapi.HCPOpenShiftClusterExternalAuth
		serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth
		hostedCluster               *v1beta1.HostedCluster

		expectNoWrite    bool
		expectCondition  *expectedCondition
		expectNoCondition bool
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
			name:                        "skip when ServiceProviderExternalAuth not yet created",
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
			expectNoCondition:           true,
		},
		{
			name:                        "confidential client: HC not found -> False/HostedClusterNotReady",
			externalAuth:                newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			hostedCluster:               nil,
			expectCondition: &expectedCondition{
				status:  metav1.ConditionFalse,
				reason:  coreapi.ExternalAuthReasonHostedClusterNotReady,
				message: "console: Waiting for HostedCluster to be observed",
			},
		},
		{
			name:                        "confidential client: HC found, no Configuration -> Unknown/HostedClusterNotReady",
			externalAuth:                newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{Configuration: nil},
			},
			expectCondition: &expectedCondition{
				status:  metav1.ConditionUnknown,
				reason:  coreapi.ExternalAuthReasonHostedClusterNotReady,
				message: "console: HostedCluster authentication status not yet available",
			},
		},
		{
			name:                        "confidential client: Available True -> Available True",
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
			expectCondition: &expectedCondition{
				status: metav1.ConditionTrue,
				reason: coreapi.ExternalAuthReasonOIDCConfigAvailable,
			},
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
			expectCondition: &expectedCondition{
				status:  metav1.ConditionFalse,
				reason:  coreapi.ExternalAuthReasonAwaitingSecret,
				message: "console: The external auth provider is waiting for the client secret to be created in the openshift-config namespace",
			},
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
			expectCondition: &expectedCondition{
				status:  metav1.ConditionFalse,
				reason:  "OtherDegraded",
				message: "console: something else is wrong",
			},
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
			expectCondition: &expectedCondition{
				status:  metav1.ConditionFalse,
				reason:  "SomeReason",
				message: "console: not available yet",
			},
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
			expectCondition: &expectedCondition{
				status:  metav1.ConditionFalse,
				reason:  coreapi.ExternalAuthReasonHostedClusterNotReady,
				message: "console: OIDC client status not yet reported by the hosted cluster",
			},
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
			hostedCluster:               nil,
			expectCondition: &expectedCondition{
				status:  metav1.ConditionTrue,
				reason:  coreapi.ExternalAuthReasonOIDCConfigAvailable,
				message: "cli: Public client does not require a secret",
			},
		},
		{
			name: "multi-client worst-wins: console awaiting secret, cli public -> AwaitingSecret",
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
			expectCondition: &expectedCondition{
				status:  metav1.ConditionFalse,
				reason:  coreapi.ExternalAuthReasonAwaitingSecret,
				message: "console: The external auth provider is waiting for the client secret to be created in the openshift-config namespace; cli: Public client does not require a secret",
			},
		},
		{
			name: "multi-client all available -> Available True",
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
			expectCondition: &expectedCondition{
				status: metav1.ConditionTrue,
				reason: coreapi.ExternalAuthReasonOIDCConfigAvailable,
			},
		},
		{
			name:         "no-op when ServiceProviderExternalAuth conditions already match",
			externalAuth: newTestExternalAuthForAvailable(),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(ServiceProviderExternalAuth *coreapi.ServiceProviderExternalAuth) {
				ServiceProviderExternalAuth.Status.Conditions = []metav1.Condition{{
					Type:    coreapi.ExternalAuthAvailableCondition,
					Status:  metav1.ConditionTrue,
					Reason:  coreapi.ExternalAuthReasonOIDCConfigAvailable,
					Message: "console: ",
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
				updatedServiceProviderExternalAuth, err := mockDB.ServiceProviderExternalAuths(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName, statusutils.TestClusterName, statusutils.TestExternalAuthName).Get(ctx, coreapi.ServiceProviderExternalAuthResourceName)
				require.NoError(t, err)
				assert.Equal(t, tc.serviceProviderExternalAuth.Status.Conditions, updatedServiceProviderExternalAuth.Status.Conditions,
					"conditions should not have changed")
				return
			}

			updatedServiceProviderExternalAuth, err := mockDB.ServiceProviderExternalAuths(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName, statusutils.TestClusterName, statusutils.TestExternalAuthName).Get(ctx, coreapi.ServiceProviderExternalAuthResourceName)
			require.NoError(t, err)

			if tc.expectNoCondition {
				assert.Empty(t, updatedServiceProviderExternalAuth.Status.Conditions, "expected no conditions on ServiceProviderExternalAuth")
				return
			}

			cond := apimeta.FindStatusCondition(updatedServiceProviderExternalAuth.Status.Conditions, coreapi.ExternalAuthAvailableCondition)
			require.NotNil(t, cond, "controller must set the Available condition on ServiceProviderExternalAuth")
			assert.Equal(t, tc.expectCondition.status, cond.Status, "Available condition status")
			assert.Equal(t, tc.expectCondition.reason, cond.Reason, "Available condition reason")
			if tc.expectCondition.message != "" {
				assert.Equal(t, tc.expectCondition.message, cond.Message, "Available condition message")
			}
		})
	}
}

func TestConditionPriority(t *testing.T) {
	tests := []struct {
		name     string
		cond     metav1.Condition
		expected int
	}{
		{"AwaitingSecret is highest priority (worst)", metav1.Condition{Status: metav1.ConditionFalse, Reason: coreapi.ExternalAuthReasonAwaitingSecret}, 0},
		{"False with other reason", metav1.Condition{Status: metav1.ConditionFalse, Reason: "OtherReason"}, 1},
		{"Unknown", metav1.Condition{Status: metav1.ConditionUnknown, Reason: "SomeReason"}, 2},
		{"True is lowest priority (best)", metav1.Condition{Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthReasonOIDCConfigAvailable}, 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, conditionPriority(tc.cond), "unexpected priority for %+v", tc.cond)
		})
	}
}
