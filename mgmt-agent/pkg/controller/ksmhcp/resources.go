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

package ksmhcp

import (
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsac "k8s.io/client-go/applyconfigurations/apps/v1"
	coreac "k8s.io/client-go/applyconfigurations/core/v1"
	metaac "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/utils/ptr"
)

func buildDeployment(namespace, ksmImage, kubeconfigSecretName, kubeconfigKey string, ownerRef metav1.OwnerReference) *appsac.DeploymentApplyConfiguration {
	return appsac.Deployment(resourceName, namespace).
		WithLabels(map[string]string{labelApp: resourceName}).
		WithOwnerReferences(metaac.OwnerReference().
			WithAPIVersion(ownerRef.APIVersion).
			WithKind(ownerRef.Kind).
			WithName(ownerRef.Name).
			WithUID(ownerRef.UID)).
		WithSpec(appsac.DeploymentSpec().
			WithReplicas(1).
			WithSelector(metaac.LabelSelector().
				WithMatchLabels(map[string]string{labelApp: resourceName})).
			WithTemplate(coreac.PodTemplateSpec().
				WithLabels(map[string]string{labelApp: resourceName}).
				WithSpec(coreac.PodSpec().
					WithContainers(coreac.Container().
						WithName("kube-state-metrics").
						WithImage(ksmImage).
						WithArgs(
							"--resources=nodes",
							"--kubeconfig=/opt/k8s/.kube/config",
							"--metric-allowlist=kube_node_status_condition,kube_node_info",
						).
						WithPorts(
							coreac.ContainerPort().
								WithName("http-metrics").
								WithContainerPort(8080).
								WithProtocol(corev1.ProtocolTCP),
							coreac.ContainerPort().
								WithName("telemetry").
								WithContainerPort(8081).
								WithProtocol(corev1.ProtocolTCP),
						).
						WithLivenessProbe(coreac.Probe().
							WithHTTPGet(coreac.HTTPGetAction().
								WithPath("/livez").
								WithPort(intstr.FromString("http-metrics"))).
							WithInitialDelaySeconds(5).
							WithTimeoutSeconds(5)).
						WithReadinessProbe(coreac.Probe().
							WithHTTPGet(coreac.HTTPGetAction().
								WithPath("/readyz").
								WithPort(intstr.FromString("telemetry"))).
							WithInitialDelaySeconds(5).
							WithTimeoutSeconds(5)).
						WithResources(coreac.ResourceRequirements().
							WithRequests(corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							}).
							WithLimits(corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							})).
						WithVolumeMounts(coreac.VolumeMount().
							WithName("kubeconfig").
							WithMountPath("/opt/k8s/.kube").
							WithReadOnly(true)).
						WithSecurityContext(coreac.SecurityContext().
							WithRunAsNonRoot(true).
							WithAllowPrivilegeEscalation(false).
							WithReadOnlyRootFilesystem(true).
							WithSeccompProfile(coreac.SeccompProfile().
								WithType(corev1.SeccompProfileTypeRuntimeDefault)).
							WithCapabilities(coreac.Capabilities().
								WithDrop(corev1.Capability("ALL"))))).
					WithAutomountServiceAccountToken(false).
					WithVolumes(coreac.Volume().
						WithName("kubeconfig").
						WithSecret(coreac.SecretVolumeSource().
							WithSecretName(kubeconfigSecretName).
							WithItems(coreac.KeyToPath().
								WithKey(kubeconfigKey).
								WithPath("config")))).
					WithSecurityContext(coreac.PodSecurityContext().
						WithRunAsNonRoot(true).
						WithRunAsUser(65534).
						WithRunAsGroup(65534).
						WithFSGroup(65534).
						WithSeccompProfile(coreac.SeccompProfile().
							WithType(corev1.SeccompProfileTypeRuntimeDefault))))))
}

func buildService(namespace string, ownerRef metav1.OwnerReference) *coreac.ServiceApplyConfiguration {
	return coreac.Service(resourceName, namespace).
		WithLabels(map[string]string{labelApp: resourceName}).
		WithOwnerReferences(metaac.OwnerReference().
			WithAPIVersion(ownerRef.APIVersion).
			WithKind(ownerRef.Kind).
			WithName(ownerRef.Name).
			WithUID(ownerRef.UID)).
		WithSpec(coreac.ServiceSpec().
			WithPorts(
				coreac.ServicePort().
					WithName("http-metrics").
					WithPort(8080).
					WithTargetPort(intstr.FromString("http-metrics")).
					WithProtocol(corev1.ProtocolTCP),
				coreac.ServicePort().
					WithName("telemetry").
					WithPort(8081).
					WithTargetPort(intstr.FromString("telemetry")).
					WithProtocol(corev1.ProtocolTCP),
			).
			WithSelector(map[string]string{labelApp: resourceName}))
}

// buildServiceMonitor returns an unstructured ServiceMonitor because
// monitoring.coreos.com is a CRD without typed apply configurations.
func buildServiceMonitor(namespace string, ownerRef metav1.OwnerReference) (*unstructured.Unstructured, error) {
	sm := &monitoringv1.ServiceMonitor{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "azmonitoring.coreos.com/v1",
			Kind:       "ServiceMonitor",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            resourceName,
			Namespace:       namespace,
			Labels:          map[string]string{labelApp: resourceName},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Endpoints: []monitoringv1.Endpoint{
				{
					Port:     "http-metrics",
					Interval: "30s",
					MetricRelabelConfigs: []monitoringv1.RelabelConfig{
						{
							TargetLabel: "namespace",
							Replacement: ptr.To(namespace),
							Action:      "replace",
						},
					},
				},
			},
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{labelApp: resourceName},
			},
			NamespaceSelector: monitoringv1.NamespaceSelector{
				MatchNames: []string{namespace},
			},
		},
	}

	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sm)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: data}, nil
}
