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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEnsureRevisionTag(t *testing.T) {
	svcName := "istiod-asm-1-28"
	webhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "istio-revision-tag-default-aks-istio-system"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.validation.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
					Service:  &admissionregistrationv1.ServiceReference{Name: svcName},
				},
			},
		},
	}
	revisionWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-29-aks-istio-system"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.namespace.sidecar-injector.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("new-ca-bundle"),
					Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
				},
			},
		},
	}
	client := fake.NewSimpleClientset(webhook, revisionWebhook)

	err := EnsureRevisionTag(context.Background(), client, "default", "asm-1-29")
	require.NoError(t, err)

	updated, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.Background(), "istio-revision-tag-default-aks-istio-system", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "istiod-asm-1-29", updated.Webhooks[0].ClientConfig.Service.Name)
}

func TestEnsureRevisionTag_UpdatesCABundleFromTargetRevision(t *testing.T) {
	webhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "istio-revision-tag-default-aks-istio-system"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.validation.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
					Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-28"},
				},
			},
		},
	}
	revisionWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-29-aks-istio-system"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.namespace.sidecar-injector.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("new-ca-bundle"),
					Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
				},
			},
		},
	}
	client := fake.NewSimpleClientset(webhook, revisionWebhook)

	err := EnsureRevisionTag(context.Background(), client, "default", "asm-1-29")
	require.NoError(t, err)

	updated, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.Background(), "istio-revision-tag-default-aks-istio-system", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "istiod-asm-1-29", updated.Webhooks[0].ClientConfig.Service.Name)
	assert.Equal(t, "aks-istio-system", updated.Webhooks[0].ClientConfig.Service.Namespace)
	assert.Equal(t, []byte("new-ca-bundle"), updated.Webhooks[0].ClientConfig.CABundle)
}

func TestEnsureRevisionTag_NoOpWhenAlreadyCorrect(t *testing.T) {
	webhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "istio-revision-tag-default-aks-istio-system"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.validation.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("current-ca-bundle"),
					Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
				},
			},
		},
	}
	revisionWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-29-aks-istio-system"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.namespace.sidecar-injector.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("current-ca-bundle"),
					Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
				},
			},
		},
	}
	client := fake.NewSimpleClientset(webhook, revisionWebhook)

	err := EnsureRevisionTag(context.Background(), client, "default", "asm-1-29")
	require.NoError(t, err)
}

func TestEnsureRevisionTag_CreatesWhenNotFound(t *testing.T) {
	revisionWebhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-29-aks-istio-system"},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "rev.namespace.sidecar-injector.istio.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("test-ca-bundle"),
					Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
				},
				AdmissionReviewVersions: []string{"v1"},
				SideEffects: func() *admissionregistrationv1.SideEffectClass {
					s := admissionregistrationv1.SideEffectClassNone
					return &s
				}(),
			},
		},
	}
	client := fake.NewSimpleClientset(revisionWebhook)

	err := EnsureRevisionTag(context.Background(), client, "prod-stable", "asm-1-29")
	require.NoError(t, err)

	created, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(
		context.Background(), "istio-revision-tag-prod-stable-aks-istio-system", metav1.GetOptions{})
	require.NoError(t, err, "tag webhook should have been created")

	assert.Equal(t, "prod-stable", created.Labels["istio.io/rev"])
	assert.Equal(t, "prod-stable", created.Labels["istio.io/tag"])
	assert.Equal(t, "sidecar-injector", created.Labels["app"])

	require.Len(t, created.Webhooks, 1)
	wh := created.Webhooks[0]
	assert.Equal(t, "rev.namespace.sidecar-injector.istio.io", wh.Name)
	assert.Equal(t, "istiod-asm-1-29", wh.ClientConfig.Service.Name)
	assert.Equal(t, "aks-istio-system", wh.ClientConfig.Service.Namespace)
	assert.Equal(t, []byte("test-ca-bundle"), wh.ClientConfig.CABundle)

	require.NotNil(t, wh.NamespaceSelector)
	assert.Len(t, wh.NamespaceSelector.MatchExpressions, 3)
	assert.Equal(t, "istio.io/rev", wh.NamespaceSelector.MatchExpressions[0].Key)
	assert.Equal(t, []string{"prod-stable"}, wh.NamespaceSelector.MatchExpressions[0].Values)
}

func TestEnsureRevisionTag_FailsWhenRevisionWebhookEmpty(t *testing.T) {
	tests := []struct {
		name            string
		revisionWebhook *admissionregistrationv1.MutatingWebhookConfiguration
		errContains     string
	}{
		{
			name: "no webhook entries",
			revisionWebhook: &admissionregistrationv1.MutatingWebhookConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-29-aks-istio-system"},
				Webhooks:   []admissionregistrationv1.MutatingWebhook{},
			},
			errContains: "has no webhook entries",
		},
		{
			name: "empty caBundle",
			revisionWebhook: &admissionregistrationv1.MutatingWebhookConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "istio-sidecar-injector-asm-1-29-aks-istio-system"},
				Webhooks: []admissionregistrationv1.MutatingWebhook{
					{
						Name: "rev.namespace.sidecar-injector.istio.io",
						ClientConfig: admissionregistrationv1.WebhookClientConfig{
							CABundle: []byte{},
							Service:  &admissionregistrationv1.ServiceReference{Name: "istiod-asm-1-29", Namespace: "aks-istio-system"},
						},
						AdmissionReviewVersions: []string{"v1"},
						SideEffects: func() *admissionregistrationv1.SideEffectClass {
							s := admissionregistrationv1.SideEffectClassNone
							return &s
						}(),
					},
				},
			},
			errContains: "has empty caBundle",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(tc.revisionWebhook)
			err := EnsureRevisionTag(context.Background(), client, "prod-stable", "asm-1-29")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errContains)
		})
	}
}
