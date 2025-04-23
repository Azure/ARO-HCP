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

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	operandImageEnvVarPrefix = "OPERAND_IMAGE_"
	imageRegistryParamName   = "imageRegistry"
)

type Customizer func(unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error)

var customizerFuncs = []Customizer{
	parameterizeNamespace,
	parameterizeRoleBindingSubjectsNamespace,
	parameterizeClusterRoleBindingSubjectsNamespace,
	parameterizeOperandsImageRegistries,
	parameterizeDeployment,
	annotationCleaner,
}

func CustomizeManifests(objects []unstructured.Unstructured) ([]unstructured.Unstructured, map[string]interface{}, error) {
	parameters := make(map[string]string)
	customizedManifests := make([]unstructured.Unstructured, len(objects))
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

func parameterizeOperandsImageRegistries(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
	if isOperatorDeployment(obj) {
		deployment := &appsv1.Deployment{}
		err := convertFromUnstructured(obj, deployment)
		if err != nil {
			return unstructured.Unstructured{}, nil, fmt.Errorf("failed to convert unstructured object to Deployment: %v", err)
		}
		for c, container := range deployment.Spec.Template.Spec.Containers {
			for e, env := range container.Env {
				if isOperandImageEnvVar(env.Name) {
					deployment.Spec.Template.Spec.Containers[c].Env[e].Value = parameterizeImageRegistry(env.Value, imageRegistryParamName)
				}
			}
		}
		modifiedObj, err := convertToUnstructured(deployment)
		return modifiedObj, map[string]string{imageRegistryParamName: ""}, err
	}
	return obj, nil, nil
}

func isOperandImageEnvVar(envVarName string) bool {
	return strings.HasPrefix(envVarName, operandImageEnvVarPrefix)
}

func parameterizeDeployment(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
	if isDeployment(obj) {
		deployment := &appsv1.Deployment{}
		err := convertFromUnstructured(obj, deployment)
		if err != nil {
			return unstructured.Unstructured{}, nil, fmt.Errorf("failed to convert unstructured object to Deployment: %v", err)
		}
		// image registry
		for c, container := range deployment.Spec.Template.Spec.Containers {
			deployment.Spec.Template.Spec.Containers[c].Image = parameterizeImageRegistry(container.Image, imageRegistryParamName)
		}
		modifiedObj, err := convertToUnstructured(deployment)
		return modifiedObj, map[string]string{imageRegistryParamName: ""}, err
	}
	return obj, nil, nil
}

func annotationCleaner(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
	annotationToScrape := []string{"openshift.io", "operatorframework.io", "olm", "alm-examples", "createdAt"}
	annotations := obj.GetAnnotations()
	for k := range annotations {
		for _, prefix := range annotationToScrape {
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
