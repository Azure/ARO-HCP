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

package istio

import (
	"bytes"
	"context"
	"fmt"

	"github.com/go-logr/logr"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

func revisionTagWebhookName(tagName string) string {
	return fmt.Sprintf("istio-revision-tag-%s-%s", tagName, istioSystemNamespace)
}

func EnsureRevisionTag(ctx context.Context, client kubernetes.Interface, tagName, newRevision string) error {
	logger := logr.FromContextOrDiscard(ctx).WithName("revision-tag")
	webhookName := revisionTagWebhookName(tagName)
	newServiceName := istiodServiceName(newRevision)

	wh, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, webhookName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get webhook %s: %w", webhookName, err)
		}
		wh, err = buildTagWebhook(ctx, client, tagName, newRevision)
		if err != nil {
			return fmt.Errorf("failed to build tag webhook: %w", err)
		}
		if _, err = client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, wh, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("failed to create tag webhook %s: %w", webhookName, err)
		}
		logger.Info("Created revision tag webhook", "webhook", webhookName, "revision", newRevision)
		return nil
	}

	revWHName := revisionWebhookName(newRevision)
	revWH, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, revWHName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get revision webhook %s for caBundle: %w", revWHName, err)
	}
	if len(revWH.Webhooks) == 0 {
		return fmt.Errorf("revision webhook %s has no webhook entries", revWHName)
	}
	newCABundle := revWH.Webhooks[0].ClientConfig.CABundle
	if len(newCABundle) == 0 {
		return fmt.Errorf("revision webhook %s has empty caBundle", revWHName)
	}

	changed := false
	for i := range wh.Webhooks {
		if wh.Webhooks[i].ClientConfig.Service == nil {
			return fmt.Errorf("webhook %s entry %d has no service-based config", webhookName, i)
		}
		if wh.Webhooks[i].ClientConfig.Service.Name != newServiceName {
			wh.Webhooks[i].ClientConfig.Service.Name = newServiceName
			changed = true
		}
		if wh.Webhooks[i].ClientConfig.Service.Namespace != istioSystemNamespace {
			wh.Webhooks[i].ClientConfig.Service.Namespace = istioSystemNamespace
			changed = true
		}
		if !bytes.Equal(wh.Webhooks[i].ClientConfig.CABundle, newCABundle) {
			wh.Webhooks[i].ClientConfig.CABundle = newCABundle
			changed = true
		}
	}
	if !changed {
		return nil
	}

	if _, err = client.AdmissionregistrationV1().MutatingWebhookConfigurations().Update(ctx, wh, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update webhook %s: %w", webhookName, err)
	}
	logger.Info("Updated revision tag webhook", "webhook", webhookName, "revision", newRevision)
	return nil
}

func revisionWebhookName(revision string) string {
	return fmt.Sprintf("istio-sidecar-injector-%s-%s", revision, istioSystemNamespace)
}

// buildTagWebhook constructs a MutatingWebhookConfiguration that routes injection
// for namespaces labeled istio.io/rev=<tagName> to istiod-<revision>. The caBundle
// is copied from the revision's AKS-managed webhook — equivalent to istioctl tag set.
func buildTagWebhook(ctx context.Context, client kubernetes.Interface, tagName, revision string) (*admissionregistrationv1.MutatingWebhookConfiguration, error) {
	revWHName := revisionWebhookName(revision)
	revWH, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, revWHName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get revision webhook %s for caBundle: %w", revWHName, err)
	}

	if len(revWH.Webhooks) == 0 {
		return nil, fmt.Errorf("revision webhook %s has no webhook entries", revWHName)
	}
	caBundle := revWH.Webhooks[0].ClientConfig.CABundle
	if len(caBundle) == 0 {
		return nil, fmt.Errorf("revision webhook %s has empty caBundle", revWHName)
	}

	return &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: revisionTagWebhookName(tagName),
			Labels: map[string]string{
				"istio.io/rev": tagName,
				"istio.io/tag": tagName,
				"app":          "sidecar-injector",
			},
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.namespace.sidecar-injector.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: caBundle,
					Service: &admissionregistrationv1.ServiceReference{
						Name:      istiodServiceName(revision),
						Namespace: istioSystemNamespace,
						Path:      ptr.To("/inject"),
						Port:      ptr.To(int32(443)),
					},
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{""},
							APIVersions: []string{"v1"},
							Resources:   []string{"pods"},
							Scope:       ptr.To(admissionregistrationv1.AllScopes),
						},
					},
				},
				NamespaceSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{Key: "istio.io/rev", Operator: metav1.LabelSelectorOpIn, Values: []string{tagName}},
						{Key: "istio-injection", Operator: metav1.LabelSelectorOpDoesNotExist},
						{Key: "kubernetes.azure.com/managedby", Operator: metav1.LabelSelectorOpNotIn, Values: []string{"aks"}},
					},
				},
				ObjectSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{Key: "sidecar.istio.io/inject", Operator: metav1.LabelSelectorOpNotIn, Values: []string{"false"}},
					},
				},
				FailurePolicy:           ptr.To(admissionregistrationv1.Fail),
				SideEffects:             ptr.To(admissionregistrationv1.SideEffectClassNone),
				AdmissionReviewVersions: []string{"v1"},
				ReinvocationPolicy:      ptr.To(admissionregistrationv1.NeverReinvocationPolicy),
			},
		},
	}, nil
}
