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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func labels() map[string]string {
	return map[string]string{
		labelApp: resourceName,
	}
}

func buildDeployment(namespace, ksmImage string, kubeconfigRef *hypershiftv1beta1.KubeconfigSecretRef, ownerRef metav1.OwnerReference) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            resourceName,
			Namespace:       namespace,
			Labels:          labels(),
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels(),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels(),
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "kube-state-metrics",
							Image: ksmImage,
							Args: []string{
								"--resources=nodes",
								"--kubeconfig=/opt/k8s/.kube/config",
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "http-metrics",
									ContainerPort: 8080,
									Protocol:      corev1.ProtocolTCP,
								},
								{
									Name:          "telemetry",
									ContainerPort: 8081,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("32Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "kubeconfig",
									MountPath: "/opt/k8s/.kube",
									ReadOnly:  true,
								},
							},
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot:             ptr.To(true),
								AllowPrivilegeEscalation: ptr.To(false),
								ReadOnlyRootFilesystem:   ptr.To(true),
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "kubeconfig",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: kubeconfigRef.Name,
									Items: []corev1.KeyToPath{
										{
											Key:  kubeconfigRef.Key,
											Path: "config",
										},
									},
								},
							},
						},
					},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
					},
				},
			},
		},
	}
}

func buildService(namespace string, ownerRef metav1.OwnerReference) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            resourceName,
			Namespace:       namespace,
			Labels:          labels(),
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "http-metrics",
					Port:       8080,
					TargetPort: intstr.FromString("http-metrics"),
					Protocol:   corev1.ProtocolTCP,
				},
				{
					Name:       "telemetry",
					Port:       8081,
					TargetPort: intstr.FromString("telemetry"),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Selector: labels(),
		},
	}
}

// buildServiceMonitor returns an unstructured ServiceMonitor because the
// monitoring.coreos.com types are CRDs without a typed client in this module.
// The metricRelabelings inject the HCP namespace so node metrics (which are
// cluster-scoped with namespace="") get routed to the correct HCP Azure
// Monitor Workspace via the existing remote write namespace filter.
func buildServiceMonitor(namespace string, ownerRef metav1.OwnerReference) *unstructured.Unstructured {
	sm := &monitoringv1.ServiceMonitor{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "monitoring.coreos.com/v1",
			Kind:       "ServiceMonitor",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            resourceName,
			Namespace:       namespace,
			Labels:          labels(),
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
				MatchLabels: labels(),
			},
			NamespaceSelector: monitoringv1.NamespaceSelector{
				MatchNames: []string{namespace},
			},
		},
	}

	u, _ := toUnstructured(sm)
	return u
}
