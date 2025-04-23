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
	"k8s.io/apimachinery/pkg/runtime"
)

func convertFromUnstructured(from unstructured.Unstructured, to interface{}) error {
	return runtime.DefaultUnstructuredConverter.FromUnstructured(from.Object, to)
}

func convertToUnstructured(from interface{}) (unstructured.Unstructured, error) {
	objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(from)
	if err != nil {
		return unstructured.Unstructured{}, err
	}
	return unstructured.Unstructured{Object: objMap}, nil
}

func convertMapToUnstructured(objMap map[string]interface{}) unstructured.Unstructured {
	return unstructured.Unstructured{Object: objMap}
}

func parameterizeImageRegistry(imageRef string, registryParamName string) string {
	registry := strings.Split(imageRef, "/")[0]
	return fmt.Sprintf("{{ .Values.%s }}%s", registryParamName, imageRef[len(registry):])
}

func makeNestedMap(flatMap map[string]string) map[string]interface{} {
	nestedMap := make(map[string]interface{})

	for key, value := range flatMap {
		parts := strings.Split(key, ".")
		currentMap := nestedMap

		for i, part := range parts {
			if i == len(parts)-1 {
				currentMap[part] = value
			} else {
				if _, exists := currentMap[part]; !exists {
					currentMap[part] = make(map[string]interface{})
				}
				currentMap = currentMap[part].(map[string]interface{})
			}
		}
	}

	return nestedMap
}

var (
	deploymentGVK             = appsv1.SchemeGroupVersion.WithKind("Deployment")
	roleBindingGVK            = rbacv1.SchemeGroupVersion.WithKind("RoleBinding")
	clusterRoleBindingGVK     = rbacv1.SchemeGroupVersion.WithKind("ClusterRoleBinding")
	mceOperatorDeploymentName = "multicluster-engine-operator"
)

func isDeployment(obj unstructured.Unstructured) bool {
	return obj.GroupVersionKind() == deploymentGVK
}

func isOperatorDeployment(obj unstructured.Unstructured) bool {
	return isDeployment(obj) && obj.GetName() == mceOperatorDeploymentName
}

func isRoleBinding(obj unstructured.Unstructured) bool {
	return obj.GroupVersionKind() == roleBindingGVK
}

func isClusterRoleBinding(obj unstructured.Unstructured) bool {
	return obj.GroupVersionKind() == clusterRoleBindingGVK
}

func deploymentFromUnstructured(obj unstructured.Unstructured) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{}
	err := convertFromUnstructured(obj, deployment)
	return deployment, err
}
