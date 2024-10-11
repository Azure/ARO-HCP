package customize

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	operandImageEnvVarPrefix = "OPERAND_IMAGE_"
	imageRegistryParamName   = "imageRegistry"
)

type CustomerFunc func(unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error)

var customizerFuncs = []CustomerFunc{
	parameterizeNamespace,
	parameterizeRoleBindingSubjectsNamespace,
	parameterizeClusterRoleBindingSubjectsNamespace,
	parameterizeOperandsImageRegistries,
	parameterizeDeployment,
	annotationCleaner,
}

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

func CustomizeManifests(objects []unstructured.Unstructured) ([]unstructured.Unstructured, map[string]interface{}, error) {
	parameters := make(map[string]string)
	customizedManifests := make([]unstructured.Unstructured, len(objects))
	for i, obj := range objects {
		var err error
		var newParams map[string]string
		for _, customerFunc := range customizerFuncs {
			obj, newParams, err = customerFunc(obj)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to apply customer function: %v", err)
			}
			for k, v := range newParams {
				parameters[k] = v
			}
		}
		customizedManifests[i] = obj
	}
	return customizedManifests, makeNestedMap(parameters), nil
}

func parameterizeNamespace(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
	// check if the resource is a namespaced resource
	if obj.GetNamespace() != "" {
		obj.SetNamespace("{{ .Release.Namespace }}")
	}
	return obj, nil, nil
}

func parameterizeRoleBindingSubjectsNamespace(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
	if obj.GetKind() == "RoleBinding" {
		roleBinding := &rbacv1.RoleBinding{}
		err := convertUnstructured(obj, roleBinding)
		if err != nil {
			return unstructured.Unstructured{}, map[string]string{}, fmt.Errorf("failed to convert unstructured object to RoleBinding: %v", err)
		}
		for s, subject := range roleBinding.Subjects {
			if subject.Kind == "ServiceAccount" {
				roleBinding.Subjects[s].Namespace = "{{ .Release.Namespace }}"
			}
		}
		modifiedObj, err := convertToUnstructured(roleBinding)
		return modifiedObj, nil, err
	}
	return obj, nil, nil
}

func parameterizeClusterRoleBindingSubjectsNamespace(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
	if obj.GetKind() == "ClusterRoleBinding" {
		clusterRoleBinding := &rbacv1.ClusterRoleBinding{}
		err := convertUnstructured(obj, clusterRoleBinding)
		if err != nil {
			return unstructured.Unstructured{}, nil, fmt.Errorf("failed to convert unstructured object to ClusterRoleBinding: %v", err)
		}
		for s, subject := range clusterRoleBinding.Subjects {
			if subject.Kind == "ServiceAccount" {
				clusterRoleBinding.Subjects[s].Namespace = "{{ .Release.Namespace }}"
			}
		}
		modifiedObj, err := convertToUnstructured(clusterRoleBinding)
		return modifiedObj, nil, err
	}
	return obj, nil, nil
}

func parameterizeOperandsImageRegistries(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
	if isOperatorDeployment(obj) {
		deployment := &appsv1.Deployment{}
		err := convertUnstructured(obj, deployment)
		if err != nil {
			return unstructured.Unstructured{}, nil, fmt.Errorf("failed to convert unstructured object to Deployment: %v", err)
		}
		for c, container := range deployment.Spec.Template.Spec.Containers {
			for e, env := range container.Env {
				if strings.HasPrefix(env.Name, operandImageEnvVarPrefix) {
					deployment.Spec.Template.Spec.Containers[c].Env[e].Value = parameterizeImageRegistry(container.Image, imageRegistryParamName)
				}
			}
		}
		modifiedObj, err := convertToUnstructured(deployment)
		return modifiedObj, map[string]string{imageRegistryParamName: ""}, err
	}
	return obj, nil, nil
}

func parameterizeDeployment(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
	if obj.GroupVersionKind() == deployGVK {
		deployment := &appsv1.Deployment{}
		err := convertUnstructured(obj, deployment)
		if err != nil {
			return unstructured.Unstructured{}, nil, fmt.Errorf("failed to convert unstructured object to Deployment: %v", err)
		}
		// image registry
		for c, container := range deployment.Spec.Template.Spec.Containers {
			deployment.Spec.Template.Spec.Containers[c].Image = parameterizeImageRegistry(container.Image, imageRegistryParamName)
		}
		modifiedObj, err := convertToUnstructured(deployment)
		return modifiedObj, map[string]string{imageRegistryParamName: ""}, err
	}
	return obj, nil, nil
}

func annotationCleaner(obj unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error) {
	obj.SetAnnotations(nil)
	return obj, nil, nil
}

func isOperatorDeployment(obj unstructured.Unstructured) bool {
	return obj.GroupVersionKind() == deployGVK && obj.GetName() == "multicluster-engine-operator"
}
