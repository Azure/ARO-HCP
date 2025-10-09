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
	"testing"

	"github.com/stretchr/testify/assert"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestSanityCheckWithConfigRequiredResources(t *testing.T) {
	config := &BundleConfig{
		ChartName:                  "test-operator",
		ChartDescription:           "Test operator",
		OperatorDeploymentNames:    []string{"test-operator"},
		OperandImageEnvPrefixes:    []string{"TEST_IMAGE_"},
		ImageRegistryParam:         "registry",
		RequiredEnvVarPrefixes:     []string{"TEST_IMAGE_"},
		RequiredResources:          []string{"Deployment", "ServiceAccount", "ConfigMap"},
		AnnotationPrefixesToRemove: []string{"test.annotation"},
	}

	// Only provide Deployment
	deployment := buildDeployment("test-operator", "registry.io/test-image:abcdef", []v1.EnvVar{
		{Name: "TEST_IMAGE_OPERAND", Value: "operand:latest"},
	})
	obj, _ := convertToUnstructured(deployment)
	objects := []unstructured.Unstructured{obj}

	err := SanityCheck(objects, config)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "required resource type ServiceAccount not found")
	assert.Contains(t, err.Error(), "required resource type ConfigMap not found")
}

func TestSanityCheckWithConfigAllResourcesPresent(t *testing.T) {
	config := &BundleConfig{
		ChartName:                  "test-operator",
		ChartDescription:           "Test operator",
		OperatorDeploymentNames:    []string{"test-operator"},
		OperandImageEnvPrefixes:    []string{"TEST_IMAGE_"},
		ImageRegistryParam:         "registry",
		RequiredEnvVarPrefixes:     []string{"TEST_IMAGE_"},
		RequiredResources:          []string{"Deployment", "ServiceAccount"},
		AnnotationPrefixesToRemove: []string{"test.annotation"},
	}

	// Provide all required resources
	deployment := buildDeployment("test-operator", "registry.io/test-image:abcdef", []v1.EnvVar{
		{Name: "TEST_IMAGE_OPERAND", Value: "operand:latest"},
	})
	deploymentObj, _ := convertToUnstructured(deployment)

	serviceAccount := &v1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: v1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sa",
		},
	}
	saObj, _ := convertToUnstructured(serviceAccount)

	objects := []unstructured.Unstructured{deploymentObj, saObj}

	err := SanityCheck(objects, config)
	assert.Nil(t, err)
}

func TestSanityCheckWithConfigMultipleEnvPrefixes(t *testing.T) {
	config := &BundleConfig{
		ChartName:                  "test-operator",
		ChartDescription:           "Test operator",
		OperatorDeploymentNames:    []string{"test-operator"},
		OperandImageEnvPrefixes:    []string{"TEST_IMAGE_", "RELATED_IMAGE_"},
		ImageRegistryParam:         "registry",
		RequiredEnvVarPrefixes:     []string{"TEST_IMAGE_", "RELATED_IMAGE_"},
		RequiredResources:          []string{"Deployment"},
		AnnotationPrefixesToRemove: []string{"test.annotation"},
	}

	// Provide only one prefix
	deployment := buildDeployment("test-operator", "registry.io/test-image:abcdef", []v1.EnvVar{
		{Name: "TEST_IMAGE_OPERAND", Value: "operand:latest"},
	})
	obj, _ := convertToUnstructured(deployment)
	objects := []unstructured.Unstructured{obj}

	err := SanityCheck(objects, config)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "no environment variables with prefix RELATED_IMAGE_")
}

func TestSanityCheckWithConfigLabelSelector(t *testing.T) {
	config := &BundleConfig{
		ChartName:                  "test-operator",
		ChartDescription:           "Test operator",
		OperatorDeploymentNames:    []string{},
		OperatorDeploymentSelector: map[string]string{"app": "test-operator"},
		OperandImageEnvPrefixes:    []string{"TEST_IMAGE_"},
		ImageRegistryParam:         "registry",
		RequiredEnvVarPrefixes:     []string{"TEST_IMAGE_"},
		RequiredResources:          []string{"Deployment"},
		AnnotationPrefixesToRemove: []string{"test.annotation"},
	}

	// Create deployment with matching label
	deployment := buildDeployment("some-controller", "registry.io/test-image:abcdef", []v1.EnvVar{
		{Name: "TEST_IMAGE_OPERAND", Value: "operand:latest"},
	})
	deployment.Labels = map[string]string{"app": "test-operator"}
	obj, _ := convertToUnstructured(deployment)
	objects := []unstructured.Unstructured{obj}

	err := SanityCheck(objects, config)
	assert.Nil(t, err)
}

func TestSanityCheckWithConfigNoValidation(t *testing.T) {
	config := &BundleConfig{
		ChartName:                  "test-operator",
		ChartDescription:           "Test operator",
		OperatorDeploymentNames:    []string{"test-operator"},
		OperandImageEnvPrefixes:    []string{"TEST_IMAGE_"},
		ImageRegistryParam:         "registry",
		RequiredEnvVarPrefixes:     []string{}, // No validation
		RequiredResources:          []string{}, // No validation
		AnnotationPrefixesToRemove: []string{"test.annotation"},
	}

	// Provide deployment without any env vars
	deployment := buildDeployment("test-operator", "registry.io/test-image:abcdef", []v1.EnvVar{})
	obj, _ := convertToUnstructured(deployment)
	objects := []unstructured.Unstructured{obj}

	err := SanityCheck(objects, config)
	assert.Nil(t, err) // Should pass because no validation rules
}
