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
	"github.com/stretchr/testify/require"

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

func TestParameterizeOperandsImageRegistry(t *testing.T) {
	deployment := buildDeployment("controller-manager", "image", []v1.EnvVar{
		{Name: "OPERAND_IMAGE_1", Value: "registry.io/operand-image-1:abcdef"},
		{Name: "OPERAND_IMAGE_2", Value: "registry.io/operand-image-2:abcdef"},
		{Name: "some-other-env", Value: "value"},
	})
	obj, err := convertToUnstructured(deployment)
	assert.Nil(t, err)
	config := &BundleConfig{
		OperatorDeploymentNames: []string{"controller-manager"},
		OperandImageEnvPrefixes: []string{"OPERAND_IMAGE_"},
		ImageRegistryParam:      "imageRegistry",
	}

	modifiedObj, params, err := createParameterizeOperandsImageRegistries(config)(obj)
	assert.Nil(t, err)
	assert.NotNil(t, params)
	assert.True(t, func() bool { _, ok := params[config.ImageRegistryParam]; return ok }())

	modifiedDeployment := &appsv1.Deployment{}
	err = convertFromUnstructured(modifiedObj, modifiedDeployment)
	assert.Nil(t, err)

	// verify all operand env vars have been modified
	expectedEnvVars := []v1.EnvVar{
		{
			Name:  "OPERAND_IMAGE_1",
			Value: "{{ .Values.imageRegistry }}/operand-image-1:abcdef",
		},
		{
			Name:  "OPERAND_IMAGE_2",
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

func TestParameterizeDeployment(t *testing.T) {
	deployment := buildDeployment("test-deployment", "registry.io/test-image:abcdef", nil)
	obj, err := convertToUnstructured(deployment)
	assert.Nil(t, err)
	config := &BundleConfig{
		ImageRegistryParam: "imageRegistry",
	}

	modifiedObj, params, err := createParameterizeDeployment(config)(obj)
	assert.Nil(t, err)
	assert.NotNil(t, params)
	assert.True(t, func() bool { _, ok := params[config.ImageRegistryParam]; return ok }())

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
		config      *BundleConfig
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
			config: &BundleConfig{
				AnnotationPrefixesToRemove: []string{
					"openshift", "olm", "operatorframework", "alm-examples",
				},
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
			config: &BundleConfig{
				AnnotationPrefixesToRemove: []string{
					"openshift", "olm", "operatorframework", "alm-examples",
				},
			},
			expected: nil,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			obj := unstructured.Unstructured{}
			obj.SetAnnotations(testCase.annotations)
			modifiedObj, param, err := createAnnotationCleaner(testCase.config)(obj)
			assert.Nil(t, err)
			assert.Nil(t, param)
			assert.Equal(t, testCase.expected, modifiedObj.GetAnnotations())
		})
	}
}

func TestIsOperandImageEnvVar(t *testing.T) {
	config := &BundleConfig{
		OperandImageEnvPrefixes: []string{"OPERAND_IMAGE_", "RELATED_IMAGE_"},
		OperandImageEnvSuffixes: []string{"_IMAGE", "_CONTAINER"},
	}

	testCases := []struct {
		name     string
		envVar   string
		expected EnvvarMatch
	}{
		{
			name:     "matches operand image prefix",
			envVar:   "OPERAND_IMAGE_CONTROLLER",
			expected: PrefixMatch,
		},
		{
			name:     "matches related image prefix",
			envVar:   "RELATED_IMAGE_WEBHOOK",
			expected: PrefixMatch,
		},
		{
			name:     "matches _IMAGE suffix",
			envVar:   "CONTROLLER_IMAGE",
			expected: SuffixMatch,
		},
		{
			name:     "matches _CONTAINER suffix",
			envVar:   "WEBHOOK_CONTAINER",
			expected: SuffixMatch,
		},
		{
			name:     "no matching pattern",
			envVar:   "SOME_OTHER_VAR",
			expected: NoMatch,
		},
		{
			name:     "empty env var",
			envVar:   "",
			expected: NoMatch,
		},
		{
			name:     "prefix takes precedence over suffix",
			envVar:   "OPERAND_IMAGE_CONTROLLER_IMAGE",
			expected: PrefixMatch,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isOperandImageEnvVar(tc.envVar, config)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestExtractEnvVarAffix(t *testing.T) {
	config := &BundleConfig{
		OperandImageEnvPrefixes: []string{"OPERAND_IMAGE_", "RELATED_IMAGE_"},
		OperandImageEnvSuffixes: []string{"_IMAGE", "_CONTAINER"},
	}

	testCases := []struct {
		name     string
		envVar   string
		afixType EnvvarMatch
		expected string
	}{
		{
			name:     "extract suffix from prefix match",
			envVar:   "OPERAND_IMAGE_CONTROLLER",
			afixType: PrefixMatch,
			expected: "Controller",
		},
		{
			name:     "extract suffix from related image prefix",
			envVar:   "RELATED_IMAGE_WEBHOOK",
			afixType: PrefixMatch,
			expected: "Webhook",
		},
		{
			name:     "extract suffix with underscore from prefix",
			envVar:   "OPERAND_IMAGE_API_SERVER",
			afixType: PrefixMatch,
			expected: "Api_server",
		},
		{
			name:     "extract prefix from _IMAGE suffix",
			envVar:   "CONTROLLER_IMAGE",
			afixType: SuffixMatch,
			expected: "Controller",
		},
		{
			name:     "extract prefix from _CONTAINER suffix",
			envVar:   "WEBHOOK_CONTAINER",
			afixType: SuffixMatch,
			expected: "Webhook",
		},
		{
			name:     "extract prefix with underscore from suffix",
			envVar:   "API_SERVER_IMAGE",
			afixType: SuffixMatch,
			expected: "Api_server",
		},
		{
			name:     "empty suffix after prefix removal",
			envVar:   "OPERAND_IMAGE_",
			afixType: PrefixMatch,
			expected: "",
		},
		{
			name:     "empty prefix after suffix removal",
			envVar:   "_IMAGE",
			afixType: SuffixMatch,
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractEnvVarAffix(tc.envVar, config, tc.afixType)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestCustomizeManifests_WithOverrides(t *testing.T) {
	objects := []unstructured.Unstructured{
		{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ServiceAccount",
				"metadata": map[string]interface{}{
					"name":      "test-sa",
					"namespace": "default",
				},
			},
		},
		{
			Object: map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"metadata": map[string]interface{}{
					"name":      "test-deployment",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"metadata": map[string]interface{}{
							"labels": map[string]interface{}{
								"app": "test",
							},
						},
					},
				},
			},
		},
	}

	config := &BundleConfig{
		ChartName:               "test-chart",
		ChartDescription:        "Test Chart",
		OperatorDeploymentNames: []string{"test-deployment"},
		ManifestOverrides: []ManifestOverride{
			{
				Selector: Selector{
					Kind: "ServiceAccount",
					Name: "test-sa",
				},
				Operations: []Operation{
					{
						Op:   "add",
						Path: "metadata.annotations",
						Value: map[string]interface{}{
							"test-annotation": "test-value",
						},
					},
				},
			},
			{
				Selector: Selector{
					Kind: "Deployment",
					Name: "test-deployment",
				},
				Operations: []Operation{
					{
						Op:    "add",
						Path:  "spec.template.metadata.labels",
						Merge: true,
						Value: map[string]interface{}{
							"override-label": "override-value",
						},
					},
				},
			},
		},
	}

	result, _, err := CustomizeManifests(objects, config)
	require.NoError(t, err)
	require.Len(t, result, 2)

	var sa *unstructured.Unstructured
	var deploy *unstructured.Unstructured
	for i := range result {
		if result[i].GetKind() == "ServiceAccount" {
			sa = &result[i]
		} else if result[i].GetKind() == "Deployment" {
			deploy = &result[i]
		}
	}

	require.NotNil(t, sa)
	require.NotNil(t, deploy)

	assert.Equal(t, "{{ .Release.Namespace }}", sa.GetNamespace())
	assert.Equal(t, "{{ .Release.Namespace }}", deploy.GetNamespace())

	saAnnotations, err := GetNestedField(*sa, "metadata.annotations")
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"test-annotation": "test-value"}, saAnnotations)

	deployLabels, err := GetNestedField(*deploy, "spec.template.metadata.labels")
	require.NoError(t, err)
	expectedLabels := map[string]interface{}{
		"app":            "test",
		"override-label": "override-value",
	}
	assert.Equal(t, expectedLabels, deployLabels)
}

func TestCustomizeManifests_WithoutOverrides(t *testing.T) {
	objects := []unstructured.Unstructured{
		{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ServiceAccount",
				"metadata": map[string]interface{}{
					"name":      "test-sa",
					"namespace": "default",
				},
			},
		},
	}

	config := &BundleConfig{
		ChartName:               "test-chart",
		ChartDescription:        "Test Chart",
		OperatorDeploymentNames: []string{"test-deployment"},
		// No ManifestOverrides
	}

	result, _, err := CustomizeManifests(objects, config)
	require.NoError(t, err)
	require.Len(t, result, 1)

	// Verify namespace was still templated
	assert.Equal(t, "{{ .Release.Namespace }}", result[0].GetNamespace())
}
