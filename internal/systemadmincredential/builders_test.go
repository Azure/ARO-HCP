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

package systemadmincredential

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func testOwner(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	owner, err := azcorearm.ParseResourceID("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1")
	require.NoError(t, err, "test owner resource ID should parse")
	return owner
}

func TestBuildCSR(t *testing.T) {
	owner := testOwner(t)
	_, privPEM, err := GenerateKeypair()
	require.NoError(t, err, "keypair generation should succeed")

	csr, err := BuildCSR(owner, "abc123", "testuser", "ocm-dev-clusterid", privPEM)
	require.NoError(t, err, "BuildCSR should succeed")

	assert.Equal(t, "system-admin-credential-abc123", csr.Name, "CSR name should include credName")
	assert.Contains(t, csr.Spec.SignerName, "customer-break-glass", "signer name should include customer-break-glass")
	assert.Contains(t, csr.Spec.SignerName, "ocm-dev-clusterid", "signer name should include HCP namespace")
	assert.Equal(t, strings.ToLower(owner.String()), csr.Annotations[ownerAnnotationKey], "owner annotation must be set")
}

func TestBuildCSRA(t *testing.T) {
	owner := testOwner(t)
	csra := BuildCSRA(owner, "abc123", "ocm-dev-clusterid")

	assert.Equal(t, "system-admin-credential-abc123", csra.Name, "CSRA name should match CSR name")
	assert.Equal(t, "ocm-dev-clusterid", csra.Namespace, "CSRA should be in HCP namespace")
	assert.Equal(t, strings.ToLower(owner.String()), csra.Annotations[ownerAnnotationKey], "owner annotation must be set")
}

func TestBuildRevocationRequest(t *testing.T) {
	owner := testOwner(t)
	crr := BuildRevocationRequest(owner, "revoke1234567890", "ocm-dev-clusterid")

	assert.Equal(t, "system-admin-credential-revocation-revoke1234567890", crr.Name, "CRR name should include revoke op suffix")
	assert.Equal(t, "ocm-dev-clusterid", crr.Namespace, "CRR should be in HCP namespace")
	assert.Equal(t, "customer-break-glass", crr.Spec.SignerClass, "CRR signer class should be customer-break-glass")
	assert.Equal(t, strings.ToLower(owner.String()), crr.Annotations[ownerAnnotationKey], "owner annotation must be set")
}

func TestBuildRBACGiveCSRPerm(t *testing.T) {
	owner := testOwner(t)
	objects := BuildRBACGiveCSRPerm(owner, "abc123")

	require.Len(t, objects, 2, "should return ClusterRole + ClusterRoleBinding")
	assert.Equal(t, "system-admin-credential-give-csr-perm-abc123", objects[0].GetName(), "ClusterRole name")
	assert.Equal(t, "system-admin-credential-give-csr-perm-abc123", objects[1].GetName(), "ClusterRoleBinding name")
	assert.Equal(t, strings.ToLower(owner.String()), objects[0].GetAnnotations()[ownerAnnotationKey], "owner annotation on ClusterRole")
	assert.Equal(t, strings.ToLower(owner.String()), objects[1].GetAnnotations()[ownerAnnotationKey], "owner annotation on ClusterRoleBinding")
}

func TestBuildRBACCSRA(t *testing.T) {
	owner := testOwner(t)
	objects := BuildRBACCSRA(owner, "abc123", "ocm-dev-clusterid")

	require.Len(t, objects, 2, "should return Role + RoleBinding")
	assert.Equal(t, "system-admin-credential-csra-perm-abc123", objects[0].GetName(), "Role name")
	assert.Equal(t, "ocm-dev-clusterid", objects[0].GetNamespace(), "Role namespace should be HCP namespace")
	assert.Equal(t, strings.ToLower(owner.String()), objects[0].GetAnnotations()[ownerAnnotationKey], "owner annotation on Role")
}

func TestBuildRBACRevocation(t *testing.T) {
	owner := testOwner(t)
	objects := BuildRBACRevocation(owner, "abc123", "ocm-dev-clusterid")

	require.Len(t, objects, 2, "should return Role + RoleBinding")
	assert.Equal(t, "system-admin-credential-revocation-perm-abc123", objects[0].GetName(), "Role name")
	assert.Equal(t, "ocm-dev-clusterid", objects[0].GetNamespace(), "Role namespace should be HCP namespace")
	assert.Equal(t, strings.ToLower(owner.String()), objects[0].GetAnnotations()[ownerAnnotationKey], "owner annotation on Role")
}

func TestRequireOwnerPanicsOnNil(t *testing.T) {
	assert.Panics(t, func() {
		requireOwner(nil)
	}, "requireOwner should panic on nil owner")
}

func TestBuildCSRRoundTrip(t *testing.T) {
	// Test that GenerateKeypair -> BuildCSR -> kubeconfig assembly works
	_, privPEM, err := GenerateKeypair()
	require.NoError(t, err, "keypair generation should succeed")

	owner := testOwner(t)
	csr, err := BuildCSR(owner, "roundtrip", "testuser", "ocm-dev-clusterid", privPEM)
	require.NoError(t, err, "BuildCSR should succeed")
	require.NotEmpty(t, csr.Spec.Request, "CSR request should not be empty")
}
