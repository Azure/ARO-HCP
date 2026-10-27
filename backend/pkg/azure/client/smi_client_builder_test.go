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

package client

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/msi-dataplane/pkg/dataplane"
)

type fakeManagedIdentitiesDataplaneClient struct {
	creds *dataplane.ManagedIdentityCredentials
	err   error
}

func (f *fakeManagedIdentitiesDataplaneClient) GetUserAssignedIdentitiesCredentials(_ context.Context, _ dataplane.UserAssignedIdentitiesRequest) (*dataplane.ManagedIdentityCredentials, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.creds, nil
}

type fakeFPAMIDataplaneClientBuilder struct {
	client   ManagedIdentitiesDataplaneClient
	buildErr error
}

func (b *fakeFPAMIDataplaneClientBuilder) BuilderType() FPAMIDataplaneClientBuilderType {
	return FPAMIDataplaneClientBuilderTypeValue
}

func (b *fakeFPAMIDataplaneClientBuilder) ManagedIdentitiesDataplane(_ string) (ManagedIdentitiesDataplaneClient, error) {
	if b.buildErr != nil {
		return nil, b.buildErr
	}
	return b.client, nil
}

func TestServiceManagedIdentityClientBuilder_NotFoundPropagation(t *testing.T) {
	const smiResourceIDStr = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"
	smiResourceID, err := azcorearm.ParseResourceID(smiResourceIDStr)
	require.NoError(t, err, "failed to parse test SMI resource ID")

	// smiBuilderMethod lets the scenarios below run identically against each of
	// serviceManagedIdentityClientBuilder's public methods, since all three go through
	// credentialsForServiceManagedIdentity and wrap its error the same way.
	type smiBuilderMethod struct {
		name string
		call func(b *serviceManagedIdentityClientBuilder, ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) error
	}

	methods := []smiBuilderMethod{
		{
			name: "UserAssignedIdentitiesClient",
			call: func(b *serviceManagedIdentityClientBuilder, ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) error {
				_, err := b.UserAssignedIdentitiesClient(ctx, clusterIdentityURL, smiResourceID, subscriptionID)
				return err
			},
		},
		{
			name: "SubnetsClient",
			call: func(b *serviceManagedIdentityClientBuilder, ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) error {
				_, err := b.SubnetsClient(ctx, clusterIdentityURL, smiResourceID, subscriptionID)
				return err
			},
		},
		{
			name: "NetworkSecurityGroupsClient",
			call: func(b *serviceManagedIdentityClientBuilder, ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID, subscriptionID string) error {
				_, err := b.NetworkSecurityGroupsClient(ctx, clusterIdentityURL, smiResourceID, subscriptionID)
				return err
			},
		},
	}

	missingFieldsCredential := dataplane.UserAssignedIdentityCredentials{
		ResourceID: ptr.To(smiResourceIDStr),
		// ClientID, ClientSecret, TenantID, AuthenticationEndpoint are all nil, as the
		// Managed Identities Data Plane returns them when the SMI no longer exists.
	}

	scenarios := []struct {
		name         string
		client       ManagedIdentitiesDataplaneClient
		wantNotFound bool
	}{
		{
			name: "returns ErrSMIClientBuilderIdentityNotFound when the dataplane response has nil required fields",
			client: &fakeManagedIdentitiesDataplaneClient{
				creds: &dataplane.ManagedIdentityCredentials{ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{missingFieldsCredential}},
			},
			wantNotFound: true,
		},
		{
			name:         "does not classify a generic dataplane transport error as SMI not found",
			client:       &fakeManagedIdentitiesDataplaneClient{err: errors.New("simulated transport failure")},
			wantNotFound: false,
		},
	}

	for _, m := range methods {
		for _, s := range scenarios {
			t.Run(m.name+": "+s.name, func(t *testing.T) {
				builder := &serviceManagedIdentityClientBuilder{
					fpaMIdataplaneClientBuilder: &fakeFPAMIDataplaneClientBuilder{client: s.client},
					azCoreARMClientOptions:      &azcorearm.ClientOptions{},
				}

				err := m.call(builder, context.Background(), "https://example.com", smiResourceID, smiResourceID.SubscriptionID)

				require.Error(t, err)
				assert.Equal(t, s.wantNotFound, errors.Is(err, ErrSMIClientBuilderIdentityNotFound),
					"errors.Is(err, ErrSMIClientBuilderIdentityNotFound) mismatch, got err: %v", err)
			})
		}
	}

	t.Run("UserAssignedIdentitiesClient: does not short-circuit as SMI not found when the dataplane response has all required fields set", func(t *testing.T) {
		completeCredential := dataplane.UserAssignedIdentityCredentials{
			ResourceID:             ptr.To(smiResourceIDStr),
			ClientID:               ptr.To("client-id"),
			ClientSecret:           ptr.To("client-secret"),
			TenantID:               ptr.To("tenant-id"),
			AuthenticationEndpoint: ptr.To("https://login.microsoftonline.com/tenant-id"),
		}
		builder := &serviceManagedIdentityClientBuilder{
			fpaMIdataplaneClientBuilder: &fakeFPAMIDataplaneClientBuilder{
				client: &fakeManagedIdentitiesDataplaneClient{
					creds: &dataplane.ManagedIdentityCredentials{ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{completeCredential}},
				},
			},
			azCoreARMClientOptions: &azcorearm.ClientOptions{},
		}

		_, err := builder.UserAssignedIdentitiesClient(context.Background(), "https://example.com", smiResourceID, smiResourceID.SubscriptionID)

		// completeCredential's ClientSecret isn't a real certificate, so dataplane.GetCredential
		// still fails trying to decode/parse it. What this test asserts is that the failure comes
		// from that deeper step, not from our own required-fields check short-circuiting the call
		// as ErrSMIClientBuilderIdentityNotFound.
		require.Error(t, err)
		assert.False(t, errors.Is(err, ErrSMIClientBuilderIdentityNotFound), "expected the required-fields check to pass through to dataplane.GetCredential, got: %v", err)
	})
}
