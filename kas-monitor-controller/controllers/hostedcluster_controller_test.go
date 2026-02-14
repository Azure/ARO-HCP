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

package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func TestHostedClusterReconciler_Reconcile(t *testing.T) {
	tests := []struct {
		name           string
		hostedCluster  *hypershiftv1beta1.HostedCluster
		serviceMonitor *monitoringv1.ServiceMonitor
		expectedResult ctrl.Result
		expectedError  bool
	}{
		{
			name: "HostedCluster not found",
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
			},
			expectedResult: ctrl.Result{},
			expectedError:  false,
		},
		{
			name: "HostedCluster being deleted",
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-cluster",
					Namespace:         "test-namespace",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
					Finalizers:        []string{"test-finalizer"},
				},
			},
			expectedResult: ctrl.Result{},
			expectedError:  false,
		},
		{
			name: "HostedCluster without available condition",
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{},
				},
			},
			expectedResult: ctrl.Result{},
			expectedError:  false,
		},
		{
			name: "HostedCluster not available",
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hypershiftv1beta1.HostedClusterAvailable),
							Status: metav1.ConditionFalse,
							Reason: "NotReady",
						},
					},
				},
			},
			expectedResult: ctrl.Result{},
			expectedError:  false,
		},
		{
			name: "HostedCluster available with Route API server",
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hypershiftv1beta1.HostedClusterSpec{
					ClusterID: "test-cluster-id",
					Services: []hypershiftv1beta1.ServicePublishingStrategyMapping{
						{
							Service: hypershiftv1beta1.APIServer,
							ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{
								Type: hypershiftv1beta1.Route,
								Route: &hypershiftv1beta1.RoutePublishingStrategy{
									Hostname: "api.test-cluster.example.com",
								},
							},
						},
					},
				},
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hypershiftv1beta1.HostedClusterAvailable),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expectedResult: ctrl.Result{},
			expectedError:  false,
		},
		{
			name: "HostedCluster available without Route API server",
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hypershiftv1beta1.HostedClusterSpec{
					ClusterID: "test-cluster-id",
					Services: []hypershiftv1beta1.ServicePublishingStrategyMapping{
						{
							Service: hypershiftv1beta1.APIServer,
							ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{
								Type: hypershiftv1beta1.LoadBalancer,
							},
						},
					},
				},
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hypershiftv1beta1.HostedClusterAvailable),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expectedResult: ctrl.Result{},
			expectedError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()

			s := scheme.Scheme
			require.NoError(t, hypershiftv1beta1.AddToScheme(s))
			require.NoError(t, monitoringv1.AddToScheme(s))

			var objs []runtime.Object
			if tt.name != "HostedCluster not found" {
				objs = append(objs, tt.hostedCluster)
			}
			if tt.serviceMonitor != nil {
				objs = append(objs, tt.serviceMonitor)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithRuntimeObjects(objs...).
				Build()

			reconciler := &HostedClusterReconciler{
				Client: fakeClient,
				Scheme: s,
			}

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
			}

			result, err := reconciler.Reconcile(ctx, req)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestHostedClusterReconciler_reconcileServiceMonitor(t *testing.T) {
	tests := []struct {
		name            string
		hostedCluster   *hypershiftv1beta1.HostedCluster
		existingMonitor *monitoringv1.ServiceMonitor
		expectedResult  ctrl.Result
		expectedError   bool
		shouldCreateNew bool
		shouldUpdate    bool
	}{
		{
			name: "Create new ServiceMonitor",
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hypershiftv1beta1.HostedClusterSpec{
					ClusterID: "test-cluster-id",
					Services: []hypershiftv1beta1.ServicePublishingStrategyMapping{
						{
							Service: hypershiftv1beta1.APIServer,
							ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{
								Type: hypershiftv1beta1.Route,
								Route: &hypershiftv1beta1.RoutePublishingStrategy{
									Hostname: "api.test-cluster.example.com",
								},
							},
						},
					},
				},
			},
			shouldCreateNew: true,
			expectedResult:  ctrl.Result{},
			expectedError:   false,
		},
		{
			name: "ServiceMonitor up to date",
			hostedCluster: &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hypershiftv1beta1.HostedClusterSpec{
					ClusterID: "test-cluster-id",
					Services: []hypershiftv1beta1.ServicePublishingStrategyMapping{
						{
							Service: hypershiftv1beta1.APIServer,
							ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{
								Type: hypershiftv1beta1.Route,
								Route: &hypershiftv1beta1.RoutePublishingStrategy{
									Hostname: "api.test-cluster.example.com",
								},
							},
						},
					},
				},
			},
			existingMonitor: &monitoringv1.ServiceMonitor{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp-kas-monitor",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"app.kubernetes.io/name":       "test-cluster",
						"app.kubernetes.io/managed-by": "kas-monitor-controller",
					},
				},
				Spec: monitoringv1.ServiceMonitorSpec{
					Selector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app.kubernetes.io/name": "kas-monitor-controller",
						},
					},
					Endpoints: []monitoringv1.Endpoint{
						{
							Port:          "blackbox",
							Interval:      "30s",
							ScrapeTimeout: "15s",
							Path:          "/probe",
							Scheme:        "http",
							Params: map[string][]string{
								"module": {"insecure_http_2xx"},
								"target": {"api.test-cluster.example.com"},
							},
							MetricRelabelConfigs: []monitoringv1.RelabelConfig{
								{
									Replacement: stringPtr("api.test-cluster.example.com"),
									TargetLabel: "probe_url",
								},
								{
									Replacement: stringPtr("test-cluster-id"),
									TargetLabel: "_id",
								},
								{
									Replacement: stringPtr("test-namespace"),
									TargetLabel: "namespace",
								},
							},
						},
					},
				},
			},
			expectedResult: ctrl.Result{},
			expectedError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()

			s := scheme.Scheme
			require.NoError(t, hypershiftv1beta1.AddToScheme(s))
			require.NoError(t, monitoringv1.AddToScheme(s))

			var objs []runtime.Object
			objs = append(objs, tt.hostedCluster)
			if tt.existingMonitor != nil {
				objs = append(objs, tt.existingMonitor)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithRuntimeObjects(objs...).
				Build()

			reconciler := &HostedClusterReconciler{
				Client: fakeClient,
				Scheme: s,
			}

			result, err := reconciler.reconcileServiceMonitor(ctx, tt.hostedCluster)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func stringPtr(s string) *string {
	return &s
}
