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
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// RBAC bundle name prefixes — every bundle's k8s object names are
// `<prefix>-<credName>`. Per-credential naming means two credentials
// cannot collide on shared cluster-scoped resources (ClusterRole,
// ClusterRoleBinding), even within a single cluster. See PLAN.md.
const (
	RBACGiveCSRPermNamePrefix    = "system-admin-credential-give-csr-perm"
	RBACCSRAPermNamePrefix       = "system-admin-credential-csra-perm"
	RBACRevocationPermNamePrefix = "system-admin-credential-revocation-perm"
)

// klusterletServiceAccount* identify the klusterlet on the management
// cluster. The RBAC bundles bind these identities to the verbs they need
// against the per-credential CSR / CSRA / CRR objects. The namespace
// `open-cluster-management-agent` is the standard klusterlet install
// namespace; pin against it explicitly so a future klusterlet repackage
// does not silently break our RBAC.
const (
	klusterletServiceAccountName      = "klusterlet-work-sa"
	klusterletServiceAccountNamespace = "open-cluster-management-agent"
)

// BuildRBACGiveCSRPerm returns the ClusterRole + ClusterRoleBinding pair
// that lets the klusterlet manage cluster-scoped CertificateSigningRequest
// objects. CSRs are cluster-scoped in k8s, so the binding has to be
// cluster-scoped too.
//
// owner is required and is written to metadata.annotations on every
// object returned.
func BuildRBACGiveCSRPerm(owner *azcorearm.ResourceID, credName string) ([]runtime.Object, error) {
	if credName == "" {
		return nil, fmt.Errorf("credName must not be empty")
	}
	name := RBACGiveCSRPermNamePrefix + "-" + credName

	cr := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: rbacv1.SchemeGroupVersion.String(),
			Kind:       "ClusterRole",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"certificates.k8s.io"},
				Resources: []string{"certificatesigningrequests"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
		},
	}
	setOwnerAnnotation(&cr.ObjectMeta, owner)

	crb := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: rbacv1.SchemeGroupVersion.String(),
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     name,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      klusterletServiceAccountName,
			Namespace: klusterletServiceAccountNamespace,
		}},
	}
	setOwnerAnnotation(&crb.ObjectMeta, owner)

	return []runtime.Object{cr, crb}, nil
}

// BuildRBACCSRA returns the Role + RoleBinding pair that lets the
// klusterlet manage CertificateSigningRequestApproval objects (HyperShift
// CRD) in the cluster's HCP namespace.
func BuildRBACCSRA(owner *azcorearm.ResourceID, credName, namespace string) ([]runtime.Object, error) {
	if credName == "" {
		return nil, fmt.Errorf("credName must not be empty")
	}
	if namespace == "" {
		return nil, fmt.Errorf("namespace must not be empty")
	}
	name := RBACCSRAPermNamePrefix + "-" + credName

	r := &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			APIVersion: rbacv1.SchemeGroupVersion.String(),
			Kind:       "Role",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"certificates.hypershift.openshift.io"},
				Resources: []string{"certificatesigningrequestapprovals"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
		},
	}
	setOwnerAnnotation(&r.ObjectMeta, owner)

	rb := &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: rbacv1.SchemeGroupVersion.String(),
			Kind:       "RoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     name,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      klusterletServiceAccountName,
			Namespace: klusterletServiceAccountNamespace,
		}},
	}
	setOwnerAnnotation(&rb.ObjectMeta, owner)

	return []runtime.Object{r, rb}, nil
}

// BuildRBACRevocation returns the Role + RoleBinding pair that lets the
// klusterlet manage CertificateRevocationRequest objects (HyperShift
// CRD) in the cluster's HCP namespace.
func BuildRBACRevocation(owner *azcorearm.ResourceID, credName, namespace string) ([]runtime.Object, error) {
	if credName == "" {
		return nil, fmt.Errorf("credName must not be empty")
	}
	if namespace == "" {
		return nil, fmt.Errorf("namespace must not be empty")
	}
	name := RBACRevocationPermNamePrefix + "-" + credName

	r := &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			APIVersion: rbacv1.SchemeGroupVersion.String(),
			Kind:       "Role",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"certificates.hypershift.openshift.io"},
				Resources: []string{"certificaterevocationrequests"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
		},
	}
	setOwnerAnnotation(&r.ObjectMeta, owner)

	rb := &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: rbacv1.SchemeGroupVersion.String(),
			Kind:       "RoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     name,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      klusterletServiceAccountName,
			Namespace: klusterletServiceAccountNamespace,
		}},
	}
	setOwnerAnnotation(&rb.ObjectMeta, owner)

	return []runtime.Object{r, rb}, nil
}
