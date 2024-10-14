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
