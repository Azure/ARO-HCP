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

package main

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestShouldGrantExporterAccess(t *testing.T) {
	const targetNamespacePrefix = "ocm-"
	const targetNamespaceExact = "open-cluster-management-policies"
	tests := map[string]bool{
		"ocm-arohcp-cluster":               true,
		"open-cluster-management-policies": true,
		"cert-exporter":                    false,
		"default":                          false,
		"ocm":                              false,
	}

	for namespace, expected := range tests {
		t.Run(namespace, func(t *testing.T) {
			if actual := shouldGrantExporterAccess(namespace, targetNamespacePrefix, targetNamespaceExact); actual != expected {
				t.Fatalf("shouldGrantExporterAccess(%q) = %t, want %t", namespace, actual, expected)
			}
		})
	}
}

func TestReconcileCreatesBindingsOnlyInTargetNamespaces(t *testing.T) {
	const policyCertificateNS = "open-cluster-management-policies"
	client := fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ocm-arohcp-cluster"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: policyCertificateNS}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
	)
	controller := newTestRBACController(client)

	if err := controller.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile() returned an error: %v", err)
	}

	bindings, err := client.RbacV1().RoleBindings("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("failed to list RoleBindings: %v", err)
	}
	if len(bindings.Items) != 2 {
		t.Fatalf("created %d RoleBindings, want 2", len(bindings.Items))
	}
	for _, binding := range bindings.Items {
		assertDesiredBinding(t, binding)
	}
}

func TestReconcileRepairsSubjects(t *testing.T) {
	const policyCertificateNS = "open-cluster-management-policies"
	client := fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: policyCertificateNS}},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      exporterRoleBindingName,
				Namespace: policyCertificateNS,
				Labels:    map[string]string{managedByLabel: rbacControllerName},
			},
			RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: exporterClusterRoleName},
			Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: "wrong", Namespace: "wrong"}},
		},
	)
	controller := newTestRBACController(client)

	if err := controller.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile() returned an error: %v", err)
	}
	binding, err := client.RbacV1().RoleBindings(policyCertificateNS).Get(context.Background(), exporterRoleBindingName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get RoleBinding: %v", err)
	}
	assertDesiredBinding(t, *binding)
}

func TestReconcileDeletesOwnedBindingOutsideTargetNamespace(t *testing.T) {
	client := fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      exporterRoleBindingName,
				Namespace: "default",
				Labels:    map[string]string{managedByLabel: rbacControllerName},
			},
		},
	)
	controller := newTestRBACController(client)

	if err := controller.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile() returned an error: %v", err)
	}
	if _, err := client.RbacV1().RoleBindings("default").Get(context.Background(), exporterRoleBindingName, metav1.GetOptions{}); err == nil {
		t.Fatal("owned RoleBinding still exists outside a target namespace")
	}
}

func TestReconcilePreservesUnmanagedBinding(t *testing.T) {
	client := fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: exporterRoleBindingName, Namespace: "default"}},
	)
	controller := newTestRBACController(client)

	if err := controller.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile() returned an error: %v", err)
	}
	if _, err := client.RbacV1().RoleBindings("default").Get(context.Background(), exporterRoleBindingName, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged RoleBinding was removed: %v", err)
	}
}

func TestReconcileRefusesUnmanagedBindingInTargetNamespace(t *testing.T) {
	const targetNamespace = "open-cluster-management-policies"
	client := fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: targetNamespace}},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: exporterRoleBindingName, Namespace: targetNamespace},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: exporterClusterRoleName},
			Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "other", Namespace: "other"}},
		},
	)
	controller := newTestRBACController(client)

	if err := controller.reconcile(context.Background()); err == nil {
		t.Fatal("reconcile() succeeded for an unmanaged RoleBinding collision")
	}
	binding, err := client.RbacV1().RoleBindings(targetNamespace).Get(context.Background(), exporterRoleBindingName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get unmanaged RoleBinding: %v", err)
	}
	if binding.Subjects[0].Name != "other" {
		t.Errorf("unmanaged RoleBinding subject was changed to %q", binding.Subjects[0].Name)
	}
}

func TestReconcileReturnsNamespaceListError(t *testing.T) {
	client := fake.NewClientset()
	client.PrependReactor("list", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("API unavailable")
	})
	controller := newTestRBACController(client)

	if err := controller.reconcile(context.Background()); err == nil {
		t.Fatal("reconcile() succeeded when namespace listing failed")
	}
}

func TestReconcileReplacesOwnedBindingWithWrongRoleRef(t *testing.T) {
	const policyCertificateNS = "open-cluster-management-policies"
	client := fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: policyCertificateNS}},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      exporterRoleBindingName,
				Namespace: policyCertificateNS,
				Labels:    map[string]string{managedByLabel: rbacControllerName},
			},
			RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "wrong"},
		},
	)
	controller := newTestRBACController(client)

	if err := controller.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile() returned an error: %v", err)
	}
	binding, err := client.RbacV1().RoleBindings(policyCertificateNS).Get(context.Background(), exporterRoleBindingName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get replacement RoleBinding: %v", err)
	}
	assertDesiredBinding(t, *binding)
}

func newTestRBACController(client kubernetes.Interface) *rbacController {
	return newRBACController(client, "cert-exporter", "cert-exporter", "ocm-", "open-cluster-management-policies")
}

func assertDesiredBinding(t *testing.T, binding rbacv1.RoleBinding) {
	t.Helper()
	if binding.RoleRef.Name != exporterClusterRoleName {
		t.Errorf("RoleRef.Name = %q, want %q", binding.RoleRef.Name, exporterClusterRoleName)
	}
	if len(binding.Subjects) != 1 || binding.Subjects[0].Name != "cert-exporter" || binding.Subjects[0].Namespace != "cert-exporter" {
		t.Errorf("Subjects = %#v, want cert-exporter service account", binding.Subjects)
	}
	if binding.Labels[managedByLabel] != rbacControllerName {
		t.Errorf("managed-by label = %q, want %q", binding.Labels[managedByLabel], rbacControllerName)
	}
}
