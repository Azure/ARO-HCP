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

package databasetesting

import (
	"context"
	"strings"
	"testing"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestConditionsValidationOnPersist_ExternalAuth(t *testing.T) {
	mock := NewMockResourcesDBClient()
	ctx := context.Background()

	subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"
	authName := "default"

	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName))

	authResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/externalAuths/" + authName))

	internalID, err := api.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123")
	if err != nil {
		t.Fatalf("Failed to create internal ID: %v", err)
	}

	// Create a cluster first (required parent)
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceID,
				Name: clusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  &internalID,
		},
	}
	_, err = mock.HCPClusters(subscriptionID, resourceGroupName).Create(ctx, cluster, nil)
	if err != nil {
		t.Fatalf("Failed to create cluster: %v", err)
	}

	validExternalAuth := func() *api.HCPOpenShiftClusterExternalAuth {
		return &api.HCPOpenShiftClusterExternalAuth{
			ProxyResource: arm.ProxyResource{
				Resource: arm.Resource{
					ID:   authResourceID,
					Name: authName,
					Type: api.ExternalAuthResourceType.String(),
				},
			},
			Properties: api.HCPOpenShiftClusterExternalAuthProperties{
				ProvisioningState: arm.ProvisioningStateSucceeded,
				Issuer: api.TokenIssuerProfile{
					URL:       "https://login.microsoftonline.com/tenant/v2.0",
					Audiences: []string{"client-id"},
				},
				Clients: []api.ExternalAuthClientProfile{
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "console",
							AuthClientNamespace: "openshift-console",
						},
						ClientID: "client-id",
						Type:     api.ExternalAuthClientTypeConfidential,
					},
				},
				Claim: api.ExternalAuthClaimProfile{
					Mappings: api.TokenClaimMappingsProfile{
						Username: api.UsernameClaimProfile{
							Claim:        "sub",
							PrefixPolicy: api.UsernameClaimPrefixPolicyNone,
						},
					},
				},
			},
		}
	}

	externalAuthCRUD := mock.HCPClusters(subscriptionID, resourceGroupName).ExternalAuth(clusterName)

	t.Run("valid conditions pass on Create", func(t *testing.T) {
		ea := validExternalAuth()
		ea.Properties.Conditions = []api.Condition{
			{
				Type:               api.ConditionTypeAvailable,
				Status:             api.ConditionStatusTypeTrue,
				LastTransitionTime: time.Now(),
				Reason:             "AsExpected",
				Message:            "Healthy",
			},
		}
		_, err := externalAuthCRUD.Create(ctx, ea, nil)
		if err != nil {
			t.Fatalf("Expected Create with valid conditions to succeed, got: %v", err)
		}
	})

	t.Run("invalid condition type fails on Replace", func(t *testing.T) {
		ea, err := externalAuthCRUD.Get(ctx, authName)
		if err != nil {
			t.Fatalf("Failed to get external auth: %v", err)
		}

		ea.Properties.Conditions = []api.Condition{
			{
				Type:               "InvalidType",
				Status:             api.ConditionStatusTypeTrue,
				LastTransitionTime: time.Now(),
				Reason:             "AsExpected",
				Message:            "Healthy",
			},
		}
		_, err = externalAuthCRUD.Replace(ctx, ea, nil)
		if err == nil {
			t.Fatal("Expected Replace with invalid condition type to fail")
		}
		if !strings.Contains(err.Error(), "conditions validation failed") {
			t.Errorf("Expected conditions validation error, got: %v", err)
		}
	})

	t.Run("missing lastTransitionTime fails on Replace", func(t *testing.T) {
		ea, err := externalAuthCRUD.Get(ctx, authName)
		if err != nil {
			t.Fatalf("Failed to get external auth: %v", err)
		}

		ea.Properties.Conditions = []api.Condition{
			{
				Type:   api.ConditionTypeAvailable,
				Status: api.ConditionStatusTypeTrue,
				Reason: "AsExpected",
			},
		}
		_, err = externalAuthCRUD.Replace(ctx, ea, nil)
		if err == nil {
			t.Fatal("Expected Replace with missing lastTransitionTime to fail")
		}
		if !strings.Contains(err.Error(), "conditions validation failed") {
			t.Errorf("Expected conditions validation error, got: %v", err)
		}
	})

	t.Run("duplicate condition types fails on Replace", func(t *testing.T) {
		ea, err := externalAuthCRUD.Get(ctx, authName)
		if err != nil {
			t.Fatalf("Failed to get external auth: %v", err)
		}

		ea.Properties.Conditions = []api.Condition{
			{
				Type:               api.ConditionTypeAvailable,
				Status:             api.ConditionStatusTypeTrue,
				LastTransitionTime: time.Now(),
				Reason:             "AsExpected",
			},
			{
				Type:               api.ConditionTypeAvailable,
				Status:             api.ConditionStatusTypeFalse,
				LastTransitionTime: time.Now(),
				Reason:             "Duplicate",
			},
		}
		_, err = externalAuthCRUD.Replace(ctx, ea, nil)
		if err == nil {
			t.Fatal("Expected Replace with duplicate condition types to fail")
		}
		if !strings.Contains(err.Error(), "conditions validation failed") {
			t.Errorf("Expected conditions validation error, got: %v", err)
		}
	})
}
