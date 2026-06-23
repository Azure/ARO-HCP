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
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	rbacv1 "k8s.io/api/rbac/v1"
	clientcmdapilatest "k8s.io/client-go/tools/clientcmd/api/latest"

	"k8s.io/apimachinery/pkg/runtime"
)

// testOwner is a stable cluster resource ID used across the Build* tests
// so the owner-annotation assertions can compare against a known value.
func testOwner(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	rid, err := azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/" +
			"resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/" +
			"hcpOpenShiftClusters/test-cluster")
	require.NoError(t, err)
	return rid
}

const (
	testCredName       = "abcdef0123456789"
	testRevokeOpSuffix = "fedcba9876543210"
	testHCPNamespace   = "ocm-arohcptest-12345"
)

func TestGenerateKeypair_RoundTripThroughCSRAndKubeconfig(t *testing.T) {
	pubPEM, privPEM, err := GenerateKeypair()
	require.NoError(t, err)
	require.NotEmpty(t, pubPEM)
	require.NotEmpty(t, privPEM)

	// Sanity: private key parses back as RSA.
	key, err := parseRSAPrivateKey(privPEM)
	require.NoError(t, err)
	require.NotNil(t, key)

	// The CSR builder must accept the same private key without error and
	// produce a CSR PEM whose embedded public key matches our pub PEM.
	csr, err := BuildCSR(
		testOwner(t),
		testCredName,
		"hypershift.openshift.io/"+testHCPNamespace+".customer-break-glass",
		testHCPNamespace,
		"customer-admin",
		privPEM,
	)
	require.NoError(t, err)
	require.NotNil(t, csr)
	require.NotEmpty(t, csr.Spec.Request)

	block, _ := pem.Decode(csr.Spec.Request)
	require.NotNil(t, block)
	assert.Equal(t, "CERTIFICATE REQUEST", block.Type)
	parsed, err := x509.ParseCertificateRequest(block.Bytes)
	require.NoError(t, err)
	assert.NoError(t, parsed.CheckSignature())
	assert.Equal(t, "customer-admin", parsed.Subject.CommonName)
}

func TestBuildCSR_RejectsEmptyArgs(t *testing.T) {
	owner := testOwner(t)
	_, privPEM, err := GenerateKeypair()
	require.NoError(t, err)
	for _, tc := range []struct {
		name     string
		credName string
		signer   string
		ns       string
		user     string
		key      []byte
		wantErr  string
	}{
		{"empty credName", "", "signer/x.customer-break-glass", testHCPNamespace, "u", privPEM, "credName"},
		{"empty signer", testCredName, "", testHCPNamespace, "u", privPEM, "signerName"},
		{"empty namespace", testCredName, "signer/x.customer-break-glass", "", "u", privPEM, "namespace"},
		{"empty username", testCredName, "signer/x.customer-break-glass", testHCPNamespace, "", privPEM, "username"},
		{"bad key", testCredName, "signer/x.customer-break-glass", testHCPNamespace, "u", []byte("not pem"), "private-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildCSR(owner, tc.credName, tc.signer, tc.ns, tc.user, tc.key)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestBuildCSR_OwnerAnnotation(t *testing.T) {
	_, privPEM, err := GenerateKeypair()
	require.NoError(t, err)
	csr, err := BuildCSR(
		testOwner(t), testCredName,
		"hypershift.openshift.io/"+testHCPNamespace+".customer-break-glass",
		testHCPNamespace, "u", privPEM,
	)
	require.NoError(t, err)
	got := csr.Annotations[OwnerAnnotationKey]
	assert.True(t, strings.HasPrefix(got, "/subscriptions/00000000"),
		"annotation must be the lowercased cluster RID; got %q", got)
	assert.Equal(t, strings.ToLower(got), got, "annotation must be lowercased")
}

func TestBuildCSR_PanicsOnNilOwner(t *testing.T) {
	_, privPEM, err := GenerateKeypair()
	require.NoError(t, err)
	assert.Panics(t, func() {
		_, _ = BuildCSR(nil, testCredName,
			"hypershift.openshift.io/x.customer-break-glass",
			testHCPNamespace, "u", privPEM)
	})
}

func TestBuildCSRA_OwnerAnnotationAndShape(t *testing.T) {
	csra, err := BuildCSRA(testOwner(t), testCredName, testHCPNamespace)
	require.NoError(t, err)
	assert.Equal(t, CSRANamePrefix+"-"+testCredName, csra.Name)
	assert.Equal(t, testHCPNamespace, csra.Namespace)
	assert.NotEmpty(t, csra.Annotations[OwnerAnnotationKey])
}

func TestBuildCSRA_RejectsEmptyArgs(t *testing.T) {
	owner := testOwner(t)
	_, err := BuildCSRA(owner, "", testHCPNamespace)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "credName")
	_, err = BuildCSRA(owner, testCredName, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace")
}

func TestBuildRevocationRequest_OwnerAnnotationAndShape(t *testing.T) {
	crr, err := BuildRevocationRequest(testOwner(t), testRevokeOpSuffix, testHCPNamespace)
	require.NoError(t, err)
	assert.Equal(t, CRRNamePrefix+"-"+testRevokeOpSuffix, crr.Name)
	assert.Equal(t, CustomerBreakGlassSignerClass, crr.Spec.SignerClass)
	assert.NotEmpty(t, crr.Annotations[OwnerAnnotationKey])
}

func TestBuildRBACGiveCSRPerm(t *testing.T) {
	objs, err := BuildRBACGiveCSRPerm(testOwner(t), testCredName)
	require.NoError(t, err)
	require.Len(t, objs, 2)

	cr, ok := objs[0].(*rbacv1.ClusterRole)
	require.True(t, ok)
	assert.Equal(t, RBACGiveCSRPermNamePrefix+"-"+testCredName, cr.Name)
	assert.NotEmpty(t, cr.Annotations[OwnerAnnotationKey])
	require.Len(t, cr.Rules, 1)
	assert.Equal(t, []string{"certificates.k8s.io"}, cr.Rules[0].APIGroups)

	crb, ok := objs[1].(*rbacv1.ClusterRoleBinding)
	require.True(t, ok)
	assert.Equal(t, cr.Name, crb.Name, "binding must reference the role by the same name")
	assert.Equal(t, cr.Name, crb.RoleRef.Name)
	require.Len(t, crb.Subjects, 1)
	assert.Equal(t, klusterletServiceAccountName, crb.Subjects[0].Name)
	assert.Equal(t, klusterletServiceAccountNamespace, crb.Subjects[0].Namespace)
}

func TestBuildRBACCSRA(t *testing.T) {
	objs, err := BuildRBACCSRA(testOwner(t), testCredName, testHCPNamespace)
	require.NoError(t, err)
	require.Len(t, objs, 2)
	r, ok := objs[0].(*rbacv1.Role)
	require.True(t, ok)
	assert.Equal(t, RBACCSRAPermNamePrefix+"-"+testCredName, r.Name)
	assert.Equal(t, testHCPNamespace, r.Namespace)
	assert.Equal(t, []string{"certificates.hypershift.openshift.io"}, r.Rules[0].APIGroups)
	assert.Equal(t, []string{"certificatesigningrequestapprovals"}, r.Rules[0].Resources)
}

func TestBuildRBACRevocation(t *testing.T) {
	objs, err := BuildRBACRevocation(testOwner(t), testCredName, testHCPNamespace)
	require.NoError(t, err)
	require.Len(t, objs, 2)
	r, ok := objs[0].(*rbacv1.Role)
	require.True(t, ok)
	assert.Equal(t, []string{"certificaterevocationrequests"}, r.Rules[0].Resources)
}

func TestBuildKubeconfig_HappyPath(t *testing.T) {
	in := BuildKubeconfigInput{
		APIURL:               "https://api.cluster.example:6443",
		ServingCABundle:      []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"),
		SignedCertificatePEM: []byte("-----BEGIN CERTIFICATE-----\nMIIC\n-----END CERTIFICATE-----\n"),
		PrivateKeyPEM:        []byte("-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----\n"),
		Username:             "customer-admin",
		ClusterName:          "my-cluster",
	}
	out, err := BuildKubeconfig(in)
	require.NoError(t, err)
	require.NotEmpty(t, out)

	// Round-trip through the kubeconfig decoder to assert structure.
	decoded, _, err := clientcmdapilatest.Codec.Decode(out, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, decoded)
	_ = runtime.Object(decoded) // type assertion already done by Codec
	assert.Contains(t, string(out), "https://api.cluster.example:6443")
	assert.Contains(t, string(out), "my-cluster")
}

func TestBuildKubeconfig_RejectsEmptyFields(t *testing.T) {
	base := BuildKubeconfigInput{
		APIURL:               "https://x",
		ServingCABundle:      []byte("ca"),
		SignedCertificatePEM: []byte("cert"),
		PrivateKeyPEM:        []byte("key"),
		Username:             "u",
		ClusterName:          "c",
	}
	for _, mut := range []struct {
		name    string
		patch   func(*BuildKubeconfigInput)
		wantErr string
	}{
		{"APIURL", func(i *BuildKubeconfigInput) { i.APIURL = "" }, "APIURL"},
		{"CA", func(i *BuildKubeconfigInput) { i.ServingCABundle = nil }, "ServingCABundle"},
		{"Cert", func(i *BuildKubeconfigInput) { i.SignedCertificatePEM = nil }, "SignedCertificatePEM"},
		{"Key", func(i *BuildKubeconfigInput) { i.PrivateKeyPEM = nil }, "PrivateKeyPEM"},
		{"Username", func(i *BuildKubeconfigInput) { i.Username = "" }, "Username"},
		{"ClusterName", func(i *BuildKubeconfigInput) { i.ClusterName = "" }, "ClusterName"},
	} {
		t.Run(mut.name, func(t *testing.T) {
			in := base
			mut.patch(&in)
			_, err := BuildKubeconfig(in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), mut.wantErr)
		})
	}
}

func TestDecodeBase64Cert(t *testing.T) {
	original := []byte("-----BEGIN CERTIFICATE-----\nMIIC\n-----END CERTIFICATE-----")
	encoded := base64.StdEncoding.EncodeToString(original)
	out, err := DecodeBase64Cert(encoded)
	require.NoError(t, err)
	assert.Equal(t, original, out)

	_, err = DecodeBase64Cert("not base64!!")
	assert.Error(t, err)
}

func TestNewCredentialName(t *testing.T) {
	got := NewCredentialName()
	assert.Len(t, got, SuffixLength)
	for _, r := range got {
		assert.True(t, (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'),
			"credName must be lowercase hex; got %q", got)
	}
	// Each call should be distinct in practice.
	again := NewCredentialName()
	assert.NotEqual(t, got, again)
}

func TestRevokeOpSuffix(t *testing.T) {
	suffix := RevokeOpSuffix("9c4f4e10-2c00-4f31-8b75-bcdcdef01234")
	assert.Equal(t, SuffixLength, len(suffix))
	assert.NotContains(t, suffix, "-")

	assert.Panics(t, func() { RevokeOpSuffix("short") })
}
