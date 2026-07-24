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
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"strings"

	certificatesv1 "k8s.io/api/certificates/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
)

// KubeObject is the subset of a typed Kubernetes object that our builders
// produce and our controllers serialize. It combines object metadata access
// (metav1.Object) with runtime.Object so callers can read name/namespace/GVK and
// JSON-marshal the object without depending on sigs.k8s.io/controller-runtime.
type KubeObject interface {
	metav1.Object
	runtime.Object
}

const (
	// ownerAnnotationKey is the annotation applied to every k8s object we land
	// on a management cluster via ApplyDesire.
	ownerAnnotationKey = "aro-hcp.openshift.io/owner"

	// defaultExpirationSeconds is the CSR expiration (approximately 24 hours).
	defaultExpirationSeconds = int32(86400)

	// customerBreakGlassSignerSuffix is the HyperShift signer name suffix.
	// This is HyperShift's contract, not ours to rename.
	customerBreakGlassSignerSuffix = "customer-break-glass"

	// customerBreakGlassRevocationSignerClass is the signer class for CRR.
	customerBreakGlassRevocationSignerClass = "customer-break-glass"
)

// requireOwner panics if owner is nil. All Build* helpers call this to guarantee
// the owner annotation is never omitted.
func requireOwner(owner *azcorearm.ResourceID) {
	if owner == nil {
		panic("systemadmincredential: owner resource ID must not be nil")
	}
}

// ownerAnnotation returns the standard owner annotation map.
func ownerAnnotation(owner *azcorearm.ResourceID) map[string]string {
	return map[string]string{
		ownerAnnotationKey: strings.ToLower(owner.String()),
	}
}

// BuildCSR builds a CertificateSigningRequest for a system admin credential.
// The hcpNamespace is the HyperShift HCP namespace on the management cluster
// (e.g. "ocm-<env>-<csClusterID>").
// privateKeyPEM is the private key used to sign the CSR request.
func BuildCSR(owner *azcorearm.ResourceID, credName, username, hcpNamespace string, privateKeyPEM []byte) (*certificatesv1.CertificateSigningRequest, error) {
	requireOwner(owner)

	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM private key")
	}
	privKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	csrTemplate := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: username,
		},
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	signerName := fmt.Sprintf("hypershift.openshift.io/%s.%s", hcpNamespace, customerBreakGlassSignerSuffix)

	return &certificatesv1.CertificateSigningRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "certificates.k8s.io/v1",
			Kind:       "CertificateSigningRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("system-admin-credential-%s", credName),
			Annotations: ownerAnnotation(owner),
		},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:           csrPEM,
			SignerName:        signerName,
			ExpirationSeconds: func() *int32 { v := defaultExpirationSeconds; return &v }(),
			Usages: []certificatesv1.KeyUsage{
				certificatesv1.UsageClientAuth,
				certificatesv1.UsageDigitalSignature,
			},
		},
	}, nil
}

// BuildCSRApproval builds a CertificateSigningRequestApproval for a system admin credential.
func BuildCSRApproval(owner *azcorearm.ResourceID, credName, hcpNamespace string) *certificatesv1alpha1.CertificateSigningRequestApproval {
	requireOwner(owner)
	return &certificatesv1alpha1.CertificateSigningRequestApproval{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "certificates.hypershift.openshift.io/v1alpha1",
			Kind:       "CertificateSigningRequestApproval",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("system-admin-credential-%s", credName),
			Namespace:   hcpNamespace,
			Annotations: ownerAnnotation(owner),
		},
	}
}

// BuildRevocationRequest builds a CertificateRevocationRequest that revokes all
// customer-break-glass certificates for the cluster.
func BuildRevocationRequest(owner *azcorearm.ResourceID, revokeOpSuffix, hcpNamespace string) *certificatesv1alpha1.CertificateRevocationRequest {
	requireOwner(owner)
	return &certificatesv1alpha1.CertificateRevocationRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "certificates.hypershift.openshift.io/v1alpha1",
			Kind:       "CertificateRevocationRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("system-admin-credential-revocation-%s", revokeOpSuffix),
			Namespace:   hcpNamespace,
			Annotations: ownerAnnotation(owner),
		},
		Spec: certificatesv1alpha1.CertificateRevocationRequestSpec{
			SignerClass: customerBreakGlassRevocationSignerClass,
		},
	}
}

// BuildRBACGiveCSRPerm returns a ClusterRole + ClusterRoleBinding pair that
// grants the klusterlet permission to manage CertificateSigningRequests for
// this credential.
func BuildRBACGiveCSRPerm(owner *azcorearm.ResourceID, credName string) []KubeObject {
	requireOwner(owner)
	name := fmt.Sprintf("system-admin-credential-give-csr-perm-%s", credName)
	annotations := ownerAnnotation(owner)
	return []KubeObject{
		&rbacv1.ClusterRole{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.authorization.k8s.io/v1",
				Kind:       "ClusterRole",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Annotations: annotations,
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups:     []string{"certificates.k8s.io"},
					Resources:     []string{"certificatesigningrequests"},
					ResourceNames: []string{fmt.Sprintf("system-admin-credential-%s", credName)},
					Verbs:         []string{"get", "list", "watch", "create", "update", "patch", "delete"},
				},
			},
		},
		&rbacv1.ClusterRoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.authorization.k8s.io/v1",
				Kind:       "ClusterRoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Annotations: annotations,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     name,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:     "Group",
					APIGroup: "rbac.authorization.k8s.io",
					Name:     "system:open-cluster-management:cluster:klusterlet:addon:managed-serviceaccount",
				},
			},
		},
	}
}

// BuildRBACCSRApproval returns a Role + RoleBinding pair that grants the klusterlet
// permission to manage CertificateSigningRequestApprovals for this credential.
func BuildRBACCSRApproval(owner *azcorearm.ResourceID, credName, hcpNamespace string) []KubeObject {
	requireOwner(owner)
	name := fmt.Sprintf("system-admin-credential-csrapproval-perm-%s", credName)
	annotations := ownerAnnotation(owner)
	return []KubeObject{
		&rbacv1.Role{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.authorization.k8s.io/v1",
				Kind:       "Role",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   hcpNamespace,
				Annotations: annotations,
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups:     []string{"certificates.hypershift.openshift.io"},
					Resources:     []string{"certificatesigningrequestapprovals"},
					ResourceNames: []string{fmt.Sprintf("system-admin-credential-%s", credName)},
					Verbs:         []string{"get", "list", "watch", "create", "update", "patch", "delete"},
				},
			},
		},
		&rbacv1.RoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.authorization.k8s.io/v1",
				Kind:       "RoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   hcpNamespace,
				Annotations: annotations,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     name,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:     "Group",
					APIGroup: "rbac.authorization.k8s.io",
					Name:     "system:open-cluster-management:cluster:klusterlet:addon:managed-serviceaccount",
				},
			},
		},
	}
}

// BuildRBACRevocation returns a Role + RoleBinding pair that grants the klusterlet
// permission to manage CertificateRevocationRequests for this credential.
func BuildRBACRevocation(owner *azcorearm.ResourceID, credName, hcpNamespace string) []KubeObject {
	requireOwner(owner)
	name := fmt.Sprintf("system-admin-credential-revocation-perm-%s", credName)
	annotations := ownerAnnotation(owner)
	return []KubeObject{
		&rbacv1.Role{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.authorization.k8s.io/v1",
				Kind:       "Role",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   hcpNamespace,
				Annotations: annotations,
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{"certificates.hypershift.openshift.io"},
					Resources: []string{"certificaterevocationrequests"},
					Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
				},
			},
		},
		&rbacv1.RoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.authorization.k8s.io/v1",
				Kind:       "RoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   hcpNamespace,
				Annotations: annotations,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     name,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:     "Group",
					APIGroup: "rbac.authorization.k8s.io",
					Name:     "system:open-cluster-management:cluster:klusterlet:addon:managed-serviceaccount",
				},
			},
		},
	}
}
