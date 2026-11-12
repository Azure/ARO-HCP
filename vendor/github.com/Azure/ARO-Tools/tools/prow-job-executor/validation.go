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

package prowjobexecutor

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/api/validation"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var (
	envVarKeyRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// validateEnvVarKey validates environment variable key format using regexp
func validateEnvVarKey(key string) error {
	if key == "" {
		return fmt.Errorf("environment variable key cannot be empty")
	}
	if !envVarKeyRegex.MatchString(key) {
		return fmt.Errorf("environment variable key %q is invalid: must start with letter or underscore and contain only letters, digits, and underscores", key)
	}
	return nil
}

// validateKubernetesLabel validates a Kubernetes label key-value pair using official Kubernetes validation
func validateKubernetesLabel(key, value string) error {
	// Validate label key using IsQualifiedName - this is the correct function for Kubernetes label keys
	if errs := k8svalidation.IsQualifiedName(key); len(errs) > 0 {
		return fmt.Errorf("label key %q is invalid: %s", key, strings.Join(errs, "; "))
	}

	// Validate value using official Kubernetes validation
	if errs := k8svalidation.IsValidLabelValue(value); len(errs) > 0 {
		return fmt.Errorf("label value %q is invalid: %s", value, strings.Join(errs, "; "))
	}

	return nil
}

// validateAnnotationsMap validates a complete set of annotations using official Kubernetes validation
func validateAnnotationsMap(annotations map[string]string) error {
	// Use official Kubernetes validation that checks both individual annotation format and total size
	if errs := validation.ValidateAnnotations(annotations, field.NewPath("annotations")); len(errs) > 0 {
		var errMessages []string
		for _, err := range errs {
			errMessages = append(errMessages, err.Error())
		}
		return fmt.Errorf("annotations validation failed: %s", strings.Join(errMessages, "; "))
	}

	return nil
}

// validateUUID validates that the given string is a valid UUID
func validateUUID(id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("execution ID must be a valid UUID format: %w", err)
	}
	return nil
}
