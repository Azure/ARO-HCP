// Copyright 2025 Microsoft Corporation
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

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	// klusterletAgentSA is the service account that the ACM klusterlet
	// agent uses on the management cluster.
	klusterletAgentSA = "klusterlet-work-sa"
	// klusterletAgentNamespace is the namespace of the klusterlet agent.
	klusterletAgentNamespace = "open-cluster-management-agent"
)

// BuildRBACGiveCSRPerm returns a ClusterRole + ClusterRoleBinding
// allowing the klusterlet agent to create/get/list/watch
// CertificateSigningRequests on the management cluster.
// Named: system-admin-credential-give-csr-perm-<credName>
func BuildRBACGiveCSRPerm(owner *azcorearm.ResourceID, credName string) []client.Object {
	requireOwner(owner)

	name := fmt.Sprintf("system-admin-credential-give-csr-perm-%s", credName)
	annotations := ownerAnnotation(owner)

	cr := &rbacv1.ClusterRole{
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
				APIGroups: []string{"certificates.k8s.io"},
				Resources: []string{"certificatesigningrequests"},
				Verbs:     []string{"create", "get", "list", "watch"},
			},
			{
				APIGroups: []string{"certificates.k8s.io"},
				Resources: []string{"certificatesigningrequests/status"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}

	crb := &rbacv1.ClusterRoleBinding{
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
				Kind:      "ServiceAccount",
				Name:      klusterletAgentSA,
				Namespace: klusterletAgentNamespace,
			},
		},
	}

	return []client.Object{cr, crb}
}

// BuildRBACCSRA returns a Role + RoleBinding allowing the klusterlet
// agent to create/get/list/watch CertificateSigningRequestApprovals
// in the HCP namespace.
// Named: system-admin-credential-csra-perm-<credName>
func BuildRBACCSRA(owner *azcorearm.ResourceID, credName, hcpNamespace string) []client.Object {
	requireOwner(owner)

	name := fmt.Sprintf("system-admin-credential-csra-perm-%s", credName)
	annotations := ownerAnnotation(owner)

	role := &rbacv1.Role{
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
				Resources: []string{"certificatesigningrequestapprovals"},
				Verbs:     []string{"create", "get", "list", "watch"},
			},
		},
	}

	rb := &rbacv1.RoleBinding{
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
				Kind:      "ServiceAccount",
				Name:      klusterletAgentSA,
				Namespace: klusterletAgentNamespace,
			},
		},
	}

	return []client.Object{role, rb}
}

// BuildRBACRevocation returns a Role + RoleBinding allowing the
// klusterlet agent to create/get/list/watch
// CertificateRevocationRequests in the HCP namespace.
// Named: system-admin-credential-revocation-perm-<credName>
func BuildRBACRevocation(owner *azcorearm.ResourceID, credName, hcpNamespace string) []client.Object {
	requireOwner(owner)

	name := fmt.Sprintf("system-admin-credential-revocation-perm-%s", credName)
	annotations := ownerAnnotation(owner)

	role := &rbacv1.Role{
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
				Verbs:     []string{"create", "get", "list", "watch"},
			},
			{
				APIGroups: []string{"certificates.hypershift.openshift.io"},
				Resources: []string{"certificaterevocationrequests/status"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}

	rb := &rbacv1.RoleBinding{
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
				Kind:      "ServiceAccount",
				Name:      klusterletAgentSA,
				Namespace: klusterletAgentNamespace,
			},
		},
	}

	return []client.Object{role, rb}
}
