package customize

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func SanityCheck(objects []unstructured.Unstructured) error {
	deploymentFound := false
	for _, obj := range objects {
		if isOperatorDeployment(obj) {
			deploymentFound = true
		}
	}
	if !deploymentFound {
		return fmt.Errorf("no operator deployment found in the bundle")
	}
	return nil
}
