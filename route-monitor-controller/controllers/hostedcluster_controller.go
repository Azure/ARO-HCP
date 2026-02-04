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
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

type HostedClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *HostedClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling HostedCluster", "name", req.Name, "namespace", req.Namespace)
	var hostedCluster hypershiftv1beta1.HostedCluster
	if err := r.Get(ctx, req.NamespacedName, &hostedCluster); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("HostedCluster not found, was deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get HostedCluster")
		return ctrl.Result{}, err
	}

	if !hostedCluster.DeletionTimestamp.IsZero() {
		logger.Info("HostedCluster is being deleted, cleaning up ServiceMonitor")
		return r.handleDeletion(ctx, &hostedCluster)
	}

	availableCondition := meta.FindStatusCondition(hostedCluster.Status.Conditions, string(hypershiftv1beta1.HostedClusterAvailable))
	if availableCondition == nil {
		logger.Info("HostedCluster is still being Provisioned")
		return ctrl.Result{}, nil
	}
	if availableCondition.Status != metav1.ConditionTrue {
		logger.Info("HostedCluster is not available yet",
			"cluster", hostedCluster.Name,
			"status", availableCondition.Status,
			"reason", availableCondition.Reason)
		return ctrl.Result{}, nil
	}

	return r.reconcileServiceMonitor(ctx, &hostedCluster)
}

func (r *HostedClusterReconciler) reconcileServiceMonitor(ctx context.Context, hostedCluster *hypershiftv1beta1.HostedCluster) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	desired, err := r.buildServiceMonitor(hostedCluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := controllerutil.SetControllerReference(hostedCluster, desired, r.Scheme); err != nil {
		logger.Error(err, "Failed to set OwnerReference")
		return ctrl.Result{}, err
	}

	var existing monitoringv1.ServiceMonitor
	err = r.Get(ctx, types.NamespacedName{
		Name:      desired.Name,
		Namespace: desired.Namespace,
	}, &existing)

	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating ServiceMonitor with OwnerReference",
				"name", desired.Name,
				"namespace", desired.Namespace,
				"owner", hostedCluster.Name)
			if err := r.Create(ctx, desired); err != nil {
				logger.Error(err, "Failed to create ServiceMonitor")
				return ctrl.Result{}, err
			}
			logger.Info("Successfully created ServiceMonitor")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get ServiceMonitor")
		return ctrl.Result{}, err
	}

	needsUpdate := false

	if !equality.Semantic.DeepEqual(existing.Spec, desired.Spec) {
		existing.Spec = desired.Spec
		needsUpdate = true
	}
	if !equality.Semantic.DeepEqual(existing.Labels, desired.Labels) {
		existing.Labels = desired.Labels
		needsUpdate = true
	}
	if !equality.Semantic.DeepEqual(existing.OwnerReferences, desired.OwnerReferences) {
		existing.OwnerReferences = desired.OwnerReferences
		needsUpdate = true
	}
	if needsUpdate {
		return ctrl.Result{}, r.Update(ctx, &existing)
	}

	logger.Info("ServiceMonitor is up to date")
	return ctrl.Result{}, nil
}

func (r *HostedClusterReconciler) buildServiceMonitor(hostedCluster *hypershiftv1beta1.HostedCluster) (*monitoringv1.ServiceMonitor, error) {
	serviceMonitorName := "hcp-kas-monitor"
	namespace := hostedCluster.Namespace

	/*module := "http_2xx"
	if useInsecure {
		module = "insecure_http_2xx"
	}*/
	module := "insecure_http_2xx"
	routeURL := getRouteURL(hostedCluster)
	if routeURL == "" {
		return nil, fmt.Errorf("Route URL empty for the HostedCluster %s", hostedCluster.Name)
	}

	params := map[string][]string{
		"module": {module},
		"target": {routeURL},
	}

	return &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceMonitorName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       hostedCluster.Name,
				"app.kubernetes.io/managed-by": "route-monitor-controller",
			},
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name": "route-monitor-controller",
				},
			},
			Endpoints: []monitoringv1.Endpoint{
				{
					Port:          "blackbox", //assuming we will set this in the independent deployment config
					Interval:      "30s",
					ScrapeTimeout: "15s",
					Path:          "/probe",
					Scheme:        "http",
					Params:        params,
					MetricRelabelConfigs: []monitoringv1.RelabelConfig{
						{
							Replacement: &routeURL,
							TargetLabel: "probe_url",
						},
						{
							Replacement: &hostedCluster.Spec.ClusterID,
							TargetLabel: "_id",
						},
						{
							Replacement: &hostedCluster.Namespace,
							TargetLabel: "namespace",
						},
					},
				},
			},
		},
	}, nil
}

func (r *HostedClusterReconciler) handleDeletion(ctx context.Context, hostedCluster *hypershiftv1beta1.HostedCluster) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	serviceMonitorName := "hcp-kas-monitor"
	namespace := hostedCluster.Namespace

	var serviceMonitor monitoringv1.ServiceMonitor
	err := r.Get(ctx, client.ObjectKey{
		Name:      serviceMonitorName,
		Namespace: namespace,
	}, &serviceMonitor)

	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("ServiceMonitor not found, may already be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get ServiceMonitor for deletion")
		return ctrl.Result{}, err
	}

	logger.Info("Deleting ServiceMonitor", "name", serviceMonitorName, "namespace", namespace)
	if err := r.Delete(ctx, &serviceMonitor); err != nil {
		logger.Error(err, "Failed to delete ServiceMonitor")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *HostedClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hypershiftv1beta1.HostedCluster{}).
		Owns(&monitoringv1.ServiceMonitor{}).
		Complete(r)
}

func getRouteURL(hostedCluster *hypershiftv1beta1.HostedCluster) string {

	for _, service := range hostedCluster.Spec.Services {
		if service.Service == hypershiftv1beta1.APIServer {
			if service.ServicePublishingStrategy.Route != nil {
				return service.ServicePublishingStrategy.Route.Hostname
			}
		}
	}
	return ""
}
