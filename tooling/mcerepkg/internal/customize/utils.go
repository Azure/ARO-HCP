package customize

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func convertUnstructured(from unstructured.Unstructured, to interface{}) error {
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

var deployGVK = schema.GroupVersionKind{
	Group:   "apps",
	Version: "v1",
	Kind:    "Deployment",
}

func isOperatorDeployment(obj unstructured.Unstructured) bool {
	return obj.GroupVersionKind() == deployGVK && obj.GetName() == "multicluster-engine-operator"
}
