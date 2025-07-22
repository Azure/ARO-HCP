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

package customize

import (
	"fmt"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Customizer func(unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error)

func CustomizeManifests(objects []unstructured.Unstructured, config *BundleConfig) ([]unstructured.Unstructured, map[string]interface{}, error) {
	parameters := make(map[string]string)
	customizedManifests := make([]unstructured.Unstructured, len(objects))

	// Create config-driven customizers
	customizerFuncs := []Customizer{
		parameterizeNamespace,
		parameterizeRoleBindingSubjectsNamespace,
		parameterizeClusterRoleBindingSubjectsNamespace,
		createParameterizeOperandsImageRegistries(config),
		createParameterizeDeployment(config),
		createAnnotationCleaner(config),
	}

	for i, obj := range objects {
		var err error
		var newParams map[string]string
		for _, customerFunc := range customizerFuncs {
			obj, newParams, err = customerFunc(obj)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to apply customer function: %v", err)
			}
			for k, v := range newParams {
				parameters[k] = v
			}
		}
		customizedManifests[i] = obj
	}
	return customizedManifests, makeNestedMap(parameters), nil
}

func parameterizeNamespace(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
	// check if the resource is a namespaced resource
	if obj.GetNamespace() != "" {
		obj.SetNamespace("{{ .Release.Namespace }}")
	}
	return obj, nil, nil
}

func parameterizeRoleBindingSubjectsNamespace(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
	if isRoleBinding(obj) {
		roleBinding := &rbacv1.RoleBinding{}
		err := convertFromUnstructured(obj, roleBinding)
		if err != nil {
			return unstructured.Unstructured{}, map[string]string{}, fmt.Errorf("failed to convert unstructured object to RoleBinding: %v", err)
		}
		for s, subject := range roleBinding.Subjects {
			if subject.Kind == "ServiceAccount" {
				roleBinding.Subjects[s].Namespace = "{{ .Release.Namespace }}"
			}
		}
		modifiedObj, err := convertToUnstructured(roleBinding)
		return modifiedObj, nil, err
	}
	return obj, nil, nil
}

func parameterizeClusterRoleBindingSubjectsNamespace(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
	if isClusterRoleBinding(obj) {
		clusterRoleBinding := &rbacv1.ClusterRoleBinding{}
		err := convertFromUnstructured(obj, clusterRoleBinding)
		if err != nil {
			return unstructured.Unstructured{}, nil, fmt.Errorf("failed to convert unstructured object to ClusterRoleBinding: %v", err)
		}
		for s, subject := range clusterRoleBinding.Subjects {
			if subject.Kind == "ServiceAccount" {
				clusterRoleBinding.Subjects[s].Namespace = "{{ .Release.Namespace }}"
			}
		}
		modifiedObj, err := convertToUnstructured(clusterRoleBinding)
		return modifiedObj, nil, err
	}
	return obj, nil, nil
}

func createParameterizeOperandsImageRegistries(config *BundleConfig) Customizer {
	return func(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
		allParams := make(map[string]string)

		if isOperatorDeployment(obj, config) {
			deployment := &appsv1.Deployment{}
			err := convertFromUnstructured(obj, deployment)
			if err != nil {
				return unstructured.Unstructured{}, nil, fmt.Errorf("failed to convert unstructured object to Deployment: %v", err)
			}
			for c, container := range deployment.Spec.Template.Spec.Containers {
				for e, env := range container.Env {
					if isOperandImageEnvVar(env.Name, config) {
						var parameterizedImage string
						var params map[string]string

						if config.PerImageCustomization {
							// Env-var-specific parameterization - use env var name (without prefix) as suffix
							suffix := extractEnvVarSuffix(env.Name, config)
							parameterizedImage, params = parameterizeImageComponentsWithSuffix(env.Value, config, suffix)
						} else {
							// Global parameterization - use original function (default behavior)
							parameterizedImage, params = parameterizeImageComponents(env.Value, config)
						}

						deployment.Spec.Template.Spec.Containers[c].Env[e].Value = parameterizedImage
						for k, v := range params {
							allParams[k] = v
						}
					}
				}
			}
			modifiedObj, err := convertToUnstructured(deployment)
			return modifiedObj, allParams, err
		}
		return obj, nil, nil
	}
}

func isOperandImageEnvVar(envVarName string, config *BundleConfig) bool {
	for _, prefix := range config.OperandImageEnvPrefixes {
		if strings.HasPrefix(envVarName, prefix) {
			return true
		}
	}
	return false
}

// extractEnvVarSuffix extracts the suffix from an environment variable name by removing the matching prefix
func extractEnvVarSuffix(envVarName string, config *BundleConfig) string {
	caser := cases.Title(language.English)
	for _, prefix := range config.OperandImageEnvPrefixes {
		if strings.HasPrefix(envVarName, prefix) {
			suffix := strings.TrimPrefix(envVarName, prefix)
			// Convert to title case for consistency
			return caser.String(strings.ToLower(suffix))
		}
	}
	return caser.String(strings.ToLower(envVarName))
}

func createParameterizeDeployment(config *BundleConfig) Customizer {
	return func(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
		allParams := make(map[string]string)

		if isDeployment(obj) {
			deployment := &appsv1.Deployment{}
			err := convertFromUnstructured(obj, deployment)
			if err != nil {
				return unstructured.Unstructured{}, nil, fmt.Errorf("failed to convert unstructured object to Deployment: %v", err)
			}
			// parameterize container images
			for c, container := range deployment.Spec.Template.Spec.Containers {
				var parameterizedImage string
				var params map[string]string

				if config.PerImageCustomization {
					// Container-specific parameterization - use container name as suffix
					caser := cases.Title(language.English)
					suffix := caser.String(container.Name)
					parameterizedImage, params = parameterizeImageComponentsWithSuffix(container.Image, config, suffix)
				} else {
					// Global parameterization - use original function (default behavior)
					parameterizedImage, params = parameterizeImageComponents(container.Image, config)
				}

				deployment.Spec.Template.Spec.Containers[c].Image = parameterizedImage
				for k, v := range params {
					allParams[k] = v
				}
			}
			modifiedObj, err := convertToUnstructured(deployment)
			return modifiedObj, allParams, err
		}
		return obj, nil, nil
	}
}

func createAnnotationCleaner(config *BundleConfig) Customizer {
	return func(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
		annotations := obj.GetAnnotations()
		for k := range annotations {
			for _, prefix := range config.AnnotationPrefixesToRemove {
				if strings.Contains(k, prefix) {
					delete(annotations, k)
					break
				}
			}
		}
		if len(annotations) == 0 {
			obj.SetAnnotations(nil)
		} else {
			obj.SetAnnotations(annotations)
		}
		return obj, nil, nil
	}
}
