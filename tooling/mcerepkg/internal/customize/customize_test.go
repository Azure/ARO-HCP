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
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestParameterizeNamespace(t *testing.T) {
	obj := unstructured.Unstructured{}
	obj.SetNamespace("test-namespace")
	modifiedObj, params, err := parameterizeNamespace(obj)
	assert.Nil(t, err)
	assert.Equal(t, "{{ .Release.Namespace }}", modifiedObj.GetNamespace())
	assert.Nil(t, params)
}

func TestParameterizeNamespaceNoNamespace(t *testing.T) {
	obj := unstructured.Unstructured{}
	modifiedObj, params, err := parameterizeNamespace(obj)
	assert.Nil(t, err)
	assert.Equal(t, "", modifiedObj.GetNamespace())
	assert.Nil(t, params)
}

func TestParameterizeRoleBindingSubjectsNamespace(t *testing.T) {
	rb := &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rolebinding",
			Namespace: "test-namespace",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "test-sa",
				Namespace: "test-namespace",
			},
		},
	}

	obj, err := convertToUnstructured(rb)
	assert.Nil(t, err)

	modifiedObj, params, err := parameterizeRoleBindingSubjectsNamespace(obj)
	assert.Nil(t, err)
	assert.Nil(t, params)

	modifiedRb := &rbacv1.RoleBinding{}
	err = convertFromUnstructured(modifiedObj, modifiedRb)
	assert.Nil(t, err)

	for _, subject := range modifiedRb.Subjects {
		assert.Equal(t, "{{ .Release.Namespace }}", subject.Namespace)
	}
}

func TestParameterizeClusterRoleBindingSubjectsNamespace(t *testing.T) {
	rb := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-crb",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "test-sa",
				Namespace: "test-namespace",
			},
		},
	}

	obj, err := convertToUnstructured(rb)
	assert.Nil(t, err)

	modifiedObj, params, err := parameterizeClusterRoleBindingSubjectsNamespace(obj)
	assert.Nil(t, err)
	assert.Nil(t, params)

	modifiedRb := &rbacv1.ClusterRoleBinding{}
	err = convertFromUnstructured(modifiedObj, modifiedRb)
	assert.Nil(t, err)

	for _, subject := range modifiedRb.Subjects {
		assert.Equal(t, "{{ .Release.Namespace }}", subject.Namespace)
	}
}

func buildDeployment(name, image string, envs []v1.EnvVar) *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: appsv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: appsv1.DeploymentSpec{
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "main",
							Image: image,
							Env:   envs,
						},
					},
				},
			},
		},
	}
}

func buildMulticlusterEngineDeployment() *appsv1.Deployment {
	return buildDeployment(
		mceOperatorDeploymentName, "registry.io/test-image:abcdef",
		[]v1.EnvVar{
			{
				Name:  fmt.Sprintf("%s1", operandImageEnvVarPrefix),
				Value: "registry.io/operand-image-1:abcdef",
			},
			{
				Name:  fmt.Sprintf("%s2", operandImageEnvVarPrefix),
				Value: "registry.io/operand-image-2:abcdef",
			},
			{
				Name:  "some-other-env",
				Value: "value",
			},
		},
	)
}

func TestParameterizeOperandsImageRegistry(t *testing.T) {
	deployment := buildMulticlusterEngineDeployment()
	obj, err := convertToUnstructured(deployment)
	assert.Nil(t, err)

	modifiedObj, params, err := parameterizeOperandsImageRegistries(obj)
	assert.Nil(t, err)
	assert.NotNil(t, params)
	_, imageRegistryParamExists := params[imageRegistryParamName]
	assert.True(t, imageRegistryParamExists)

	modifiedDeployment := &appsv1.Deployment{}
	err = convertFromUnstructured(modifiedObj, modifiedDeployment)
	assert.Nil(t, err)

	// verify all operand env vars have been modified
	expectedEnvVars := []v1.EnvVar{
		{
			Name:  fmt.Sprintf("%s1", operandImageEnvVarPrefix),
			Value: "{{ .Values.imageRegistry }}/operand-image-1:abcdef",
		},
		{
			Name:  fmt.Sprintf("%s2", operandImageEnvVarPrefix),
			Value: "{{ .Values.imageRegistry }}/operand-image-2:abcdef",
		},
		{
			Name:  "some-other-env",
			Value: "value",
		},
	}
	for _, expectedEnvVar := range expectedEnvVars {
		found := false
		for _, container := range modifiedDeployment.Spec.Template.Spec.Containers {
			for _, envVar := range container.Env {
				fmt.Printf("actual: %v", envVar)
				if envVar.Name == expectedEnvVar.Name {
					assert.Equal(t, expectedEnvVar.Value, envVar.Value)
					found = true
					break
				}
			}
		}
		assert.True(t, found)
	}
}

func TestParameterizeDeploymentImage(t *testing.T) {
	deployment := buildDeployment("test-deployment", "registry.io/test-image:abcdef", nil)
	obj, err := convertToUnstructured(deployment)
	assert.Nil(t, err)

	modifiedObj, params, err := parameterizeDeployment(obj)
	assert.Nil(t, err)
	assert.NotNil(t, params)
	_, imageRegistryParamExists := params[imageRegistryParamName]
	assert.True(t, imageRegistryParamExists)

	modifiedDeployment := &appsv1.Deployment{}
	err = convertFromUnstructured(modifiedObj, modifiedDeployment)
	assert.Nil(t, err)

	// verify all image references have been modified
	for _, container := range modifiedDeployment.Spec.Template.Spec.Containers {
		assert.Equal(t, "{{ .Values.imageRegistry }}/test-image:abcdef", container.Image)
	}
}

func TestAnnotationCleaner(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		annotations map[string]string
		expected    map[string]string
	}{
		{
			name: "only remove unwanted annotations",
			annotations: map[string]string{
				"openshift.io/some-annotation":         "value",
				"operatorframework.io/some-annotation": "value",
				"olm/some-annotation":                  "value",
				"alm-examples/some-annotation":         "value",
				"some-other-annotation":                "value",
			},
			expected: map[string]string{
				"some-other-annotation": "value",
			},
		},
		{
			name: "annotations are nil if none are left after cleaning",
			annotations: map[string]string{
				"openshift.io/some-annotation": "value",
			},
			expected: nil,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			obj := unstructured.Unstructured{}
			obj.SetAnnotations(testCase.annotations)
			modifiedObj, param, err := annotationCleaner(obj)
			assert.Nil(t, err)
			assert.Nil(t, param)
			assert.Equal(t, testCase.expected, modifiedObj.GetAnnotations())
		})
	}
}
