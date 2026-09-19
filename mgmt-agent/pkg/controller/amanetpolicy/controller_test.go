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

package amanetpolicy

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-Tools/testutil"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	hsfake "github.com/openshift/hypershift/client/clientset/clientset/fake"
	hcplisters "github.com/openshift/hypershift/client/listers/hypershift/v1beta1"
)

func TestBuildNetworkPolicy(t *testing.T) {
	np := BuildNetworkPolicy(
		"ocm-arohcppers-abc123-xyz",
		"test-hcp",
		types.UID("uid-123"),
	)

	testutil.CompareWithFixture(t, np)
}

func newTestController(t *testing.T, kubeClient kubernetes.Interface, hsClient hypershiftclient.Interface, hcps ...*hypershiftv1beta1.HostedControlPlane) *AMANetworkPolicyController {
	t.Helper()

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	for _, hcp := range hcps {
		if err := indexer.Add(hcp); err != nil {
			t.Fatalf("failed to add HCP to indexer: %v", err)
		}
	}

	return &AMANetworkPolicyController{
		kubeClientset: kubeClient,
		hsClient:      hsClient,
		hcpLister:     hcplisters.NewHostedControlPlaneLister(indexer),
		workqueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: "test"},
		),
	}
}

func TestSyncHandler(t *testing.T) {
	tests := []struct {
		name            string
		key             string
		hcp             *hypershiftv1beta1.HostedControlPlane
		existingNP      bool
		applyError      bool
		wantErr         bool
		wantPolicy      bool
		wantFinalizer   bool
		wantNoFinalizer bool
	}{
		{
			name: "HCP exists, no finalizer — adds finalizer",
			key:  "ocm-test-ns/test-hcp",
			hcp: &hypershiftv1beta1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "ocm-test-ns",
					UID:       "uid-456",
				},
			},
			wantFinalizer: true,
		},
		{
			name: "HCP exists, has finalizer — creates NetworkPolicy",
			key:  "ocm-test-ns/test-hcp",
			hcp: &hypershiftv1beta1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  "ocm-test-ns",
					UID:        "uid-456",
					Finalizers: []string{finalizerName},
				},
			},
			wantPolicy: true,
		},
		{
			name: "HCP deleted, no-op",
			key:  "ocm-test-ns/deleted-hcp",
		},
		{
			name: "HCP deleting, NetworkPolicy exists — cleanup",
			key:  "ocm-test-ns/deleting-hcp",
			hcp: &hypershiftv1beta1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deleting-hcp",
					Namespace:         "ocm-test-ns",
					UID:               "uid-789",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
					Finalizers:        []string{finalizerName},
				},
			},
			existingNP:      true,
			wantNoFinalizer: true,
		},
		{
			name: "HCP deleting, NetworkPolicy already gone — cleanup",
			key:  "ocm-test-ns/deleting-hcp",
			hcp: &hypershiftv1beta1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deleting-hcp",
					Namespace:         "ocm-test-ns",
					UID:               "uid-789",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
					Finalizers:        []string{finalizerName},
				},
			},
			wantNoFinalizer: true,
		},
		{
			name: "apply error propagated",
			key:  "ocm-test-ns/error-hcp",
			hcp: &hypershiftv1beta1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "error-hcp",
					Namespace:  "ocm-test-ns",
					UID:        "uid-err",
					Finalizers: []string{finalizerName},
				},
			},
			applyError: true,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := kubefake.NewClientset()
			if tt.applyError {
				kubeClient.PrependReactor("patch", "networkpolicies", func(action k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, fmt.Errorf("simulated apply error")
				})
			}

			ctx := context.Background()

			if tt.existingNP && tt.hcp != nil {
				np := &networkingv1.NetworkPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      networkPolicyName,
						Namespace: tt.hcp.Namespace,
					},
				}
				if _, err := kubeClient.NetworkingV1().NetworkPolicies(tt.hcp.Namespace).Create(ctx, np, metav1.CreateOptions{}); err != nil {
					t.Fatalf("failed to pre-create NetworkPolicy: %v", err)
				}
			}

			var hcps []*hypershiftv1beta1.HostedControlPlane
			var hsObjs []runtime.Object
			if tt.hcp != nil {
				hcps = append(hcps, tt.hcp)
				hsObjs = append(hsObjs, tt.hcp)
			}
			hsClient := hsfake.NewSimpleClientset(hsObjs...)

			c := newTestController(t, kubeClient, hsClient, hcps...)

			err := c.syncHandler(ctx, tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("syncHandler() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantPolicy {
				policies, err := kubeClient.NetworkingV1().NetworkPolicies(tt.hcp.Namespace).List(ctx, metav1.ListOptions{})
				if err != nil {
					t.Fatalf("failed to list NetworkPolicies: %v", err)
				}
				if len(policies.Items) != 1 {
					t.Fatalf("expected 1 NetworkPolicy, got %d", len(policies.Items))
				}
				np := policies.Items[0]
				if np.Name != networkPolicyName {
					t.Errorf("NetworkPolicy name = %q, want %q", np.Name, networkPolicyName)
				}
				if np.Labels[managedByLabel] != managedByValue {
					t.Errorf("NetworkPolicy managed-by label = %q, want %q", np.Labels[managedByLabel], managedByValue)
				}
			}

			if tt.wantFinalizer && tt.hcp != nil {
				updated, err := hsClient.HypershiftV1beta1().HostedControlPlanes(tt.hcp.Namespace).Get(ctx, tt.hcp.Name, metav1.GetOptions{})
				if err != nil {
					t.Fatalf("failed to get HCP: %v", err)
				}
				if !slices.Contains(updated.Finalizers, finalizerName) {
					t.Errorf("expected finalizer %q to be present, got %v", finalizerName, updated.Finalizers)
				}
			}

			if tt.wantNoFinalizer && tt.hcp != nil {
				updated, err := hsClient.HypershiftV1beta1().HostedControlPlanes(tt.hcp.Namespace).Get(ctx, tt.hcp.Name, metav1.GetOptions{})
				if err != nil {
					t.Fatalf("failed to get HCP: %v", err)
				}
				if slices.Contains(updated.Finalizers, finalizerName) {
					t.Errorf("expected finalizer %q to be removed, got %v", finalizerName, updated.Finalizers)
				}
			}

			if tt.existingNP && tt.wantNoFinalizer && tt.hcp != nil {
				policies, err := kubeClient.NetworkingV1().NetworkPolicies(tt.hcp.Namespace).List(ctx, metav1.ListOptions{})
				if err != nil {
					t.Fatalf("failed to list NetworkPolicies: %v", err)
				}
				if len(policies.Items) != 0 {
					t.Errorf("expected NetworkPolicy to be deleted, got %d", len(policies.Items))
				}
			}
		})
	}
}
