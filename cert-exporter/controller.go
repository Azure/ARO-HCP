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
	"fmt"
	"log/slog"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	exporterClusterRoleName = "cert-exporter"
	exporterRoleBindingName = "cert-exporter"
	managedByLabel          = "app.kubernetes.io/managed-by"
	rbacControllerName      = "cert-exporter-rbac-controller"
)

type rbacController struct {
	client                 kubernetes.Interface
	exporterNamespace      string
	exporterServiceAccount string
	targetNamespacePrefix  string
	targetNamespaceExact   string
	logger                 *slog.Logger
	status                 *controllerStatus
}

func newRBACController(client kubernetes.Interface, exporterNamespace, exporterServiceAccount, targetNamespacePrefix, targetNamespaceExact string) *rbacController {
	return &rbacController{
		client:                 client,
		exporterNamespace:      exporterNamespace,
		exporterServiceAccount: exporterServiceAccount,
		targetNamespacePrefix:  targetNamespacePrefix,
		targetNamespaceExact:   targetNamespaceExact,
		logger:                 slog.Default().With("controller", rbacControllerName),
		status:                 &controllerStatus{},
	}
}

func (c *rbacController) reconcile(ctx context.Context) error {
	namespaces, err := listNamespaces(ctx, c.client)
	if err != nil {
		return err
	}

	var reconciliationErrors []error
	var targetNamespaces int64
	exactNamespaceFound := false
	for _, namespace := range namespaces {
		if namespace == c.targetNamespaceExact {
			exactNamespaceFound = true
		}
		if !shouldGrantExporterAccess(namespace, c.targetNamespacePrefix, c.targetNamespaceExact) {
			if err := c.deleteRoleBindingIfOwned(ctx, namespace); err != nil {
				reconciliationErrors = append(reconciliationErrors, err)
			}
			continue
		}
		targetNamespaces++
		if err := c.reconcileRoleBinding(ctx, namespace); err != nil {
			reconciliationErrors = append(reconciliationErrors, err)
		}
	}
	c.status.targetNamespaces.Store(targetNamespaces)
	if !exactNamespaceFound {
		c.logger.WarnContext(ctx, "expected certificate namespace not found", "namespace", c.targetNamespaceExact)
	}
	return errors.Join(reconciliationErrors...)
}

func shouldGrantExporterAccess(namespace, prefix, exact string) bool {
	return strings.HasPrefix(namespace, prefix) || namespace == exact
}

func (c *rbacController) reconcileRoleBinding(ctx context.Context, namespace string) error {
	desired := c.desiredRoleBinding(namespace)
	existing, err := c.client.RbacV1().RoleBindings(namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = c.client.RbacV1().RoleBindings(namespace).Create(ctx, desired, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create RoleBinding in namespace %q: %w", namespace, err)
		}
		c.status.bindingsCreated.Add(1)
		c.logger.InfoContext(ctx, "created exporter RoleBinding", "namespace", namespace)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get RoleBinding in namespace %q: %w", namespace, err)
	}

	if !isOwnedRoleBinding(existing) {
		return fmt.Errorf("refusing to modify unmanaged RoleBinding %s/%s", namespace, desired.Name)
	}
	if existing.RoleRef != desired.RoleRef {
		if err := c.client.RbacV1().RoleBindings(namespace).Delete(ctx, desired.Name, metav1.DeleteOptions{}); err != nil {
			return fmt.Errorf("failed to delete RoleBinding with unexpected roleRef in namespace %q: %w", namespace, err)
		}
		if _, err := c.client.RbacV1().RoleBindings(namespace).Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("failed to recreate RoleBinding in namespace %q: %w", namespace, err)
		}
		c.status.bindingsDeleted.Add(1)
		c.status.bindingsCreated.Add(1)
		c.logger.InfoContext(ctx, "recreated exporter RoleBinding", "namespace", namespace)
		return nil
	}
	if apiequality.Semantic.DeepEqual(existing.Labels, desired.Labels) && apiequality.Semantic.DeepEqual(existing.Subjects, desired.Subjects) {
		return nil
	}
	existing.Labels = desired.Labels
	existing.Subjects = desired.Subjects
	if _, err := c.client.RbacV1().RoleBindings(namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update RoleBinding in namespace %q: %w", namespace, err)
	}
	c.status.bindingsUpdated.Add(1)
	c.logger.InfoContext(ctx, "updated exporter RoleBinding", "namespace", namespace)
	return nil
}

func (c *rbacController) deleteRoleBindingIfOwned(ctx context.Context, namespace string) error {
	existing, err := c.client.RbacV1().RoleBindings(namespace).Get(ctx, exporterRoleBindingName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get RoleBinding in namespace %q for cleanup: %w", namespace, err)
	}
	if !isOwnedRoleBinding(existing) {
		return nil
	}
	if err := c.client.RbacV1().RoleBindings(namespace).Delete(ctx, existing.Name, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("failed to delete RoleBinding in namespace %q: %w", namespace, err)
	}
	c.status.bindingsDeleted.Add(1)
	c.logger.InfoContext(ctx, "deleted exporter RoleBinding outside target namespace", "namespace", namespace)
	return nil
}

func isOwnedRoleBinding(binding *rbacv1.RoleBinding) bool {
	return binding.Labels[managedByLabel] == rbacControllerName
}

func (c *rbacController) desiredRoleBinding(namespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      exporterRoleBindingName,
			Namespace: namespace,
			Labels: map[string]string{
				managedByLabel: rbacControllerName,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     exporterClusterRoleName,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      c.exporterServiceAccount,
			Namespace: c.exporterNamespace,
		}},
	}
}
