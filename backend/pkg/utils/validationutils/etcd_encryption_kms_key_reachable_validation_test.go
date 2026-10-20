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

package validationutils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

const (
	kmsTestVaultName  = "customer-kv"
	kmsTestKeyName    = "etcd-kms-key"
	kmsTestKeyVersion = "aa560717f07040a29d42d143966f5dcb"
	kmsTestIdentityID = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/kms"
)

var kmsTestClusterCreatedAt = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// fakeKeyVaultKeysClient is a hand-rolled stand-in for KeyVaultKeysClient,
// matching the fake-client convention already used in this package.
type fakeKeyVaultKeysClient struct {
	getKeyErr error
}

func (f *fakeKeyVaultKeysClient) GetKey(context.Context, string, string, *azkeys.GetKeyOptions) (azkeys.GetKeyResponse, error) {
	if f.getKeyErr != nil {
		return azkeys.GetKeyResponse{}, f.getKeyErr
	}
	return azkeys.GetKeyResponse{}, nil
}

type fakeKeyVaultKeysClientBuilder struct {
	client   azureclient.KeyVaultKeysClient
	buildErr error

	gotVaultName string
}

func (f *fakeKeyVaultKeysClientBuilder) Build(vaultName string, _ azcore.TokenCredential) (azureclient.KeyVaultKeysClient, error) {
	f.gotVaultName = vaultName
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	return f.client, nil
}

type fakeTokenRetriever struct{}

func (f *fakeTokenRetriever) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token"}, nil
}

type fakeTokenRetrieverBuilder struct {
	buildErr error

	gotIdentityResourceID string
}

func (f *fakeTokenRetrieverBuilder) Build(_ string, identityResourceID *azcorearm.ResourceID) (azureclient.MIDataplaneBasedIdentityAccessTokenRetriever, error) {
	if identityResourceID != nil {
		f.gotIdentityResourceID = identityResourceID.String()
	}
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	return &fakeTokenRetriever{}, nil
}

// keyVaultResponseError builds an *azcore.ResponseError carrying a Key Vault
// style error body, so the inner error code can be exercised.
func keyVaultResponseError(statusCode int, outerCode string, innerCode string) error {
	body := fmt.Sprintf(`{"error":{"code":%q,"message":"Operation returned an invalid status code"}}`, outerCode)
	if innerCode != "" {
		body = fmt.Sprintf(
			`{"error":{"code":%q,"message":"Client address 10.0.0.1 object id 11111111-2222-3333-4444-555555555555 is not allowed","innererror":{"code":%q}}}`,
			outerCode, innerCode,
		)
	}
	return &azcore.ResponseError{
		StatusCode: statusCode,
		ErrorCode:  outerCode,
		RawResponse: &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		},
	}
}

func kmsReachableTestCluster(visibility metadataapi.KeyVaultVisibility) *coreapi.Cluster {
	return &coreapi.Cluster{
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:         mustParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/mycluster"),
				SystemData: &coreapi.SystemData{CreatedAt: ptr.To(kmsTestClusterCreatedAt)},
			},
		},
		CustomerProperties: coreapi.ClusterCustomerProperties{
			Etcd: coreapi.EtcdProfile{
				DataEncryption: coreapi.EtcdDataEncryptionProfile{
					KeyManagementMode: metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
					CustomerManaged: &coreapi.CustomerManagedEncryptionProfile{
						EncryptionType: metadataapi.CustomerManagedEncryptionTypeKMS,
						Kms: &coreapi.KmsEncryptionProfile{
							Visibility:   visibility,
							KeyVaultType: coreapi.KmsKeyVaultTypeKeyVault,
							ActiveKey: coreapi.KmsKey{
								Name:      kmsTestKeyName,
								VaultName: kmsTestVaultName,
								Version:   kmsTestKeyVersion,
							},
						},
					},
				},
			},
			Platform: coreapi.CustomerPlatformProfile{
				OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
					UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
						ControlPlaneOperators: map[string]*azcorearm.ResourceID{
							"kms": mustParseResourceID(kmsTestIdentityID),
						},
					},
				},
			},
		},
	}
}

// newKMSReachableValidation wires the validation with fakes. pastGracePeriod
// controls whether the fake clock is advanced beyond the RBAC propagation
// grace period relative to the cluster's CreatedAt.
func newKMSReachableValidation(getKeyErr error, pastGracePeriod bool) *EtcdEncryptionKMSKeyReachableValidation {
	now := kmsTestClusterCreatedAt.Add(time.Minute)
	if pastGracePeriod {
		now = kmsTestClusterCreatedAt.Add(kmsKeyRBACPropagationGracePeriod + time.Minute)
	}
	return NewEtcdEncryptionKMSKeyReachableValidation(
		&fakeTokenRetrieverBuilder{},
		&fakeKeyVaultKeysClientBuilder{client: &fakeKeyVaultKeysClient{getKeyErr: getKeyErr}},
		clocktesting.NewFakePassiveClock(now),
	)
}

// TestEtcdEncryptionKMSKeyReachableValidationOutcomeMatrix covers every probe
// outcome against both Key Vault visibilities. The Private column is the heart
// of this validation: the service cluster has no route to a private endpoint,
// so it must never return a verdict there.
func TestEtcdEncryptionKMSKeyReachableValidationOutcomeMatrix(t *testing.T) {
	for _, tc := range []struct {
		name           string
		getKeyErr      error
		publicOutcome  OutcomeType
		publicReason   string
		privateOutcome OutcomeType
		privateReason  string
	}{
		{
			name:           "key is reachable",
			getKeyErr:      nil,
			publicOutcome:  OutcomeTypePassed,
			publicReason:   coreapi.ControllerConditionReasonAsExpected,
			privateOutcome: OutcomeTypeSkipped,
			privateReason:  "NotApplicable",
		},
		{
			name:           "key does not exist",
			getKeyErr:      keyVaultResponseError(http.StatusNotFound, "KeyNotFound", ""),
			publicOutcome:  OutcomeTypeFailed,
			publicReason:   "KmsKeyNotFound",
			privateOutcome: OutcomeTypeFailed,
			privateReason:  "KmsKeyNotFound",
		},
		{
			name:           "network access denied",
			getKeyErr:      keyVaultResponseError(http.StatusForbidden, "Forbidden", keyVaultInnerErrorCodeForbiddenByConnection),
			publicOutcome:  OutcomeTypeFailed,
			publicReason:   "KeyVaultNetworkAccessDenied",
			privateOutcome: OutcomeTypeSkipped,
			privateReason:  "NotApplicable",
		},
		{
			name:           "authorization denied past grace period",
			getKeyErr:      keyVaultResponseError(http.StatusForbidden, "Forbidden", keyVaultInnerErrorCodeForbiddenByRbac),
			publicOutcome:  OutcomeTypeFailed,
			publicReason:   "KeyVaultAccessDenied",
			privateOutcome: OutcomeTypeFailed,
			privateReason:  "KeyVaultAccessDenied",
		},
		{
			name:           "unauthorized past grace period",
			getKeyErr:      keyVaultResponseError(http.StatusUnauthorized, "Unauthorized", ""),
			publicOutcome:  OutcomeTypeFailed,
			publicReason:   "KeyVaultAccessDenied",
			privateOutcome: OutcomeTypeFailed,
			privateReason:  "KeyVaultAccessDenied",
		},
		{
			name:           "forbidden with an unrecognised inner code",
			getKeyErr:      keyVaultResponseError(http.StatusForbidden, "Forbidden", "SomethingNewFromKeyVault"),
			publicOutcome:  OutcomeTypeUnknown,
			publicReason:   "KeyVaultProbeInconclusive",
			privateOutcome: OutcomeTypeSkipped,
			privateReason:  "NotApplicable",
		},
		{
			name:           "forbidden without a parseable inner code",
			getKeyErr:      keyVaultResponseError(http.StatusForbidden, "Forbidden", ""),
			publicOutcome:  OutcomeTypeUnknown,
			publicReason:   "KeyVaultProbeInconclusive",
			privateOutcome: OutcomeTypeSkipped,
			privateReason:  "NotApplicable",
		},
		{
			name:           "key vault server error",
			getKeyErr:      keyVaultResponseError(http.StatusInternalServerError, "InternalServerError", ""),
			publicOutcome:  OutcomeTypeUnknown,
			publicReason:   "KeyVaultProbeInconclusive",
			privateOutcome: OutcomeTypeSkipped,
			privateReason:  "NotApplicable",
		},
		{
			name:           "key vault throttling",
			getKeyErr:      keyVaultResponseError(http.StatusTooManyRequests, "TooManyRequests", ""),
			publicOutcome:  OutcomeTypeUnknown,
			publicReason:   "KeyVaultProbeInconclusive",
			privateOutcome: OutcomeTypeSkipped,
			privateReason:  "NotApplicable",
		},
		{
			name:           "dns or dial failure",
			getKeyErr:      errors.New("dial tcp: lookup customer-kv.vault.azure.net: no such host"),
			publicOutcome:  OutcomeTypeUnknown,
			publicReason:   "KeyVaultProbeInconclusive",
			privateOutcome: OutcomeTypeSkipped,
			privateReason:  "NotApplicable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, visibility := range []struct {
				visibility      metadataapi.KeyVaultVisibility
				expectedOutcome OutcomeType
				expectedReason  string
			}{
				{metadataapi.KeyVaultVisibilityPublic, tc.publicOutcome, tc.publicReason},
				{metadataapi.KeyVaultVisibilityPrivate, tc.privateOutcome, tc.privateReason},
			} {
				t.Run(string(visibility.visibility), func(t *testing.T) {
					validation := newKMSReachableValidation(tc.getKeyErr, true)

					result := validation.Validate(context.Background(), nil, kmsReachableTestCluster(visibility.visibility))

					require.NoError(t, result.Validate(), "validation result must be well-formed")
					require.Equal(t, visibility.expectedOutcome, result.Outcome.Type)
					require.Equal(t, visibility.expectedReason, result.Reason())
				})
			}
		})
	}
}

// TestEtcdEncryptionKMSKeyReachableValidationRBACGracePeriod covers the one
// outcome that depends on how long ago the cluster was created.
func TestEtcdEncryptionKMSKeyReachableValidationRBACGracePeriod(t *testing.T) {
	rbacDenial := keyVaultResponseError(http.StatusForbidden, "Forbidden", keyVaultInnerErrorCodeForbiddenByRbac)

	t.Run("within the grace period the denial is inconclusive", func(t *testing.T) {
		validation := newKMSReachableValidation(rbacDenial, false)

		result := validation.Validate(context.Background(), nil, kmsReachableTestCluster(metadataapi.KeyVaultVisibilityPublic))

		require.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		require.Equal(t, "KeyVaultProbeInconclusive", result.Reason())
	})

	t.Run("past the grace period the denial fails the validation", func(t *testing.T) {
		validation := newKMSReachableValidation(rbacDenial, true)

		result := validation.Validate(context.Background(), nil, kmsReachableTestCluster(metadataapi.KeyVaultVisibilityPublic))

		require.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
		require.Equal(t, "KeyVaultAccessDenied", result.Reason())
	})

	t.Run("a cluster with no CreatedAt is treated as past the grace period", func(t *testing.T) {
		validation := newKMSReachableValidation(rbacDenial, false)
		cluster := kmsReachableTestCluster(metadataapi.KeyVaultVisibilityPublic)
		cluster.SystemData = nil

		result := validation.Validate(context.Background(), nil, cluster)

		require.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
		require.Equal(t, "KeyVaultAccessDenied", result.Reason())
	})
}

// TestEtcdEncryptionKMSKeyReachableValidationGuards covers the cases that short
// circuit before any Key Vault call is made.
func TestEtcdEncryptionKMSKeyReachableValidationGuards(t *testing.T) {
	t.Run("platform managed encryption is skipped", func(t *testing.T) {
		cluster := kmsReachableTestCluster(metadataapi.KeyVaultVisibilityPublic)
		cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = metadataapi.EtcdDataEncryptionKeyManagementModeTypePlatformManaged

		result := newKMSReachableValidation(nil, true).Validate(context.Background(), nil, cluster)

		require.Equal(t, OutcomeTypeSkipped, result.Outcome.Type)
		require.Equal(t, "NotApplicable", result.Reason())
	})

	t.Run("customer managed without a kms profile is skipped", func(t *testing.T) {
		cluster := kmsReachableTestCluster(metadataapi.KeyVaultVisibilityPublic)
		cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms = nil

		result := newKMSReachableValidation(nil, true).Validate(context.Background(), nil, cluster)

		require.Equal(t, OutcomeTypeSkipped, result.Outcome.Type)
		require.Equal(t, "NotApplicable", result.Reason())
	})

	t.Run("managed HSM is skipped", func(t *testing.T) {
		cluster := kmsReachableTestCluster(metadataapi.KeyVaultVisibilityPublic)
		cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.KeyVaultType = coreapi.KmsKeyVaultTypeManagedHSM

		result := newKMSReachableValidation(nil, true).Validate(context.Background(), nil, cluster)

		require.Equal(t, OutcomeTypeSkipped, result.Outcome.Type)
		require.Equal(t, "NotApplicable", result.Reason())
	})

	t.Run("a missing kms identity is an internal error and never blames the user", func(t *testing.T) {
		cluster := kmsReachableTestCluster(metadataapi.KeyVaultVisibilityPublic)
		cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]*azcorearm.ResourceID{}

		result := newKMSReachableValidation(nil, true).Validate(context.Background(), nil, cluster)

		require.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		require.Equal(t, "InternalError", result.Reason())
	})

	t.Run("a token retriever build failure is an internal error", func(t *testing.T) {
		validation := NewEtcdEncryptionKMSKeyReachableValidation(
			&fakeTokenRetrieverBuilder{buildErr: errors.New("mi dataplane unavailable")},
			&fakeKeyVaultKeysClientBuilder{client: &fakeKeyVaultKeysClient{}},
			clocktesting.NewFakePassiveClock(kmsTestClusterCreatedAt),
		)

		result := validation.Validate(context.Background(), nil, kmsReachableTestCluster(metadataapi.KeyVaultVisibilityPublic))

		require.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		require.Equal(t, "InternalError", result.Reason())
	})

	t.Run("a key vault client build failure is an internal error", func(t *testing.T) {
		validation := NewEtcdEncryptionKMSKeyReachableValidation(
			&fakeTokenRetrieverBuilder{},
			&fakeKeyVaultKeysClientBuilder{buildErr: errors.New("no key vault DNS suffix configured")},
			clocktesting.NewFakePassiveClock(kmsTestClusterCreatedAt),
		)

		result := validation.Validate(context.Background(), nil, kmsReachableTestCluster(metadataapi.KeyVaultVisibilityPublic))

		require.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		require.Equal(t, "InternalError", result.Reason())
	})
}

// TestEtcdEncryptionKMSKeyReachableValidationProbesTheConfiguredKey asserts the
// probe is aimed at the vault and identity the cluster actually declares.
func TestEtcdEncryptionKMSKeyReachableValidationProbesTheConfiguredKey(t *testing.T) {
	tokenBuilder := &fakeTokenRetrieverBuilder{}
	keysBuilder := &fakeKeyVaultKeysClientBuilder{client: &fakeKeyVaultKeysClient{}}
	validation := NewEtcdEncryptionKMSKeyReachableValidation(tokenBuilder, keysBuilder, clocktesting.NewFakePassiveClock(kmsTestClusterCreatedAt))

	result := validation.Validate(context.Background(), nil, kmsReachableTestCluster(metadataapi.KeyVaultVisibilityPublic))

	require.Equal(t, OutcomeTypePassed, result.Outcome.Type)
	require.Equal(t, kmsTestVaultName, keysBuilder.gotVaultName, "the probe must target the vault named in the cluster payload")
	require.Equal(t, strings.ToLower(kmsTestIdentityID), strings.ToLower(tokenBuilder.gotIdentityResourceID), "the probe must authenticate as the cluster's kms identity")
}

// TestEtcdEncryptionKMSKeyReachableValidationUserMessagesOmitCallerDetails
// guards the rule that Key Vault's caller diagnostics -- client IP and identity
// object ID -- never reach a customer-visible message.
func TestEtcdEncryptionKMSKeyReachableValidationUserMessagesOmitCallerDetails(t *testing.T) {
	for _, getKeyErr := range []error{
		keyVaultResponseError(http.StatusForbidden, "Forbidden", keyVaultInnerErrorCodeForbiddenByConnection),
		keyVaultResponseError(http.StatusForbidden, "Forbidden", keyVaultInnerErrorCodeForbiddenByRbac),
		keyVaultResponseError(http.StatusNotFound, "KeyNotFound", ""),
	} {
		for _, visibility := range []metadataapi.KeyVaultVisibility{metadataapi.KeyVaultVisibilityPublic, metadataapi.KeyVaultVisibilityPrivate} {
			result := newKMSReachableValidation(getKeyErr, true).Validate(context.Background(), nil, kmsReachableTestCluster(visibility))

			// ToCondition carries the user-facing message; it is what ends up on
			// the cluster's RequirementsValid condition.
			userMessage := result.ToCondition("EtcdEncryptionKMSKeyReachableValidation").Message
			require.NotContains(t, userMessage, "10.0.0.1", "user message must not leak the Key Vault caller address")
			require.NotContains(t, userMessage, "11111111-2222-3333-4444-555555555555", "user message must not leak the Key Vault caller object id")
		}
	}
}
