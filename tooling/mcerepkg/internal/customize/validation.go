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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/errors"
)

func SanityCheck(objects []unstructured.Unstructured) error {
	var errs []error
	operatorDeploymentFound := false
	for _, obj := range objects {
		if isOperatorDeployment(obj) {
			deployment, err := deploymentFromUnstructured(obj)
			if err != nil {
				errs = append(errs, fmt.Errorf("deployment is invalid: %v", err))
			}
			operatorDeploymentFound = true
			operandImageEnvVarsFound := false
			for _, container := range deployment.Spec.Template.Spec.Containers {
				for _, envVar := range container.Env {
					if isOperandImageEnvVar(envVar.Name) {
						operandImageEnvVarsFound = true
						break
					}
				}
			}
			if !operandImageEnvVarsFound {
				errs = append(errs, fmt.Errorf("no operand image env vars found in the operator deployment"))
			}
		}
	}
	if !operatorDeploymentFound {
		errs = append(errs, fmt.Errorf("no operator deployment found in the bundle"))
	}

	return errors.NewAggregate(errs)
}
