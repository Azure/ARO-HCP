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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/errors"
)

func SanityCheck(objects []unstructured.Unstructured, config *BundleConfig) error {
	var errs []error

	// Validate required resources are present
	errs = append(errs, validateRequiredResources(objects, config)...)

	// Validate operator deployment(s)
	errs = append(errs, validateOperatorDeployments(objects, config)...)

	return errors.NewAggregate(errs)
}

func validateRequiredResources(objects []unstructured.Unstructured, config *BundleConfig) []error {
	var errs []error

	// Track found resources
	foundResources := make(map[string]bool)
	for _, obj := range objects {
		foundResources[obj.GetKind()] = true
	}

	// Check for required resources
	for _, requiredResource := range config.RequiredResources {
		if !foundResources[requiredResource] {
			errs = append(errs, fmt.Errorf("required resource type %s not found in bundle", requiredResource))
		}
	}

	return errs
}

func validateOperatorDeployments(objects []unstructured.Unstructured, config *BundleConfig) []error {
	var errs []error
	operatorDeploymentFound := false

	for _, obj := range objects {
		if isOperatorDeployment(obj, config) {
			deployment, err := deploymentFromUnstructured(obj)
			if err != nil {
				errs = append(errs, fmt.Errorf("deployment is invalid: %v", err))
				continue
			}
			operatorDeploymentFound = true

			// Validate required environment variables in operator deployment
			if len(config.RequiredEnvVarPrefixes) > 0 {
				errs = append(errs, validateRequiredEnvVars(deployment, config)...)
			}
		}
	}

	if !operatorDeploymentFound {
		errs = append(errs, fmt.Errorf("no operator deployment found in the bundle"))
	}

	return errs
}

func validateRequiredEnvVars(deployment *appsv1.Deployment, config *BundleConfig) []error {
	var errs []error

	// Track found env var prefixes
	foundPrefixes := make(map[string]bool)
	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, envVar := range container.Env {
			for _, prefix := range config.RequiredEnvVarPrefixes {
				if strings.HasPrefix(envVar.Name, prefix) {
					foundPrefixes[prefix] = true
				}
			}
		}
	}

	// Check for required prefixes
	for _, requiredPrefix := range config.RequiredEnvVarPrefixes {
		if !foundPrefixes[requiredPrefix] {
			errs = append(errs, fmt.Errorf("no environment variables with prefix %s found in operator deployment", requiredPrefix))
		}
	}

	return errs
}
