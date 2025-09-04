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
	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func convertFromUnstructured(from unstructured.Unstructured, to interface{}) error {
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

var (
	deploymentGVK         = appsv1.SchemeGroupVersion.WithKind("Deployment")
	roleBindingGVK        = rbacv1.SchemeGroupVersion.WithKind("RoleBinding")
	clusterRoleBindingGVK = rbacv1.SchemeGroupVersion.WithKind("ClusterRoleBinding")
)

func isDeployment(obj unstructured.Unstructured) bool {
	return obj.GroupVersionKind() == deploymentGVK
}

func isOperatorDeployment(obj unstructured.Unstructured, config *BundleConfig) bool {
	if !isDeployment(obj) {
		return false
	}

	// Check by explicit names
	for _, name := range config.OperatorDeploymentNames {
		if strings.Contains(obj.GetName(), name) {
			return true
		}
	}

	// Check by label selectors
	labels := obj.GetLabels()
	if labels != nil {
		for key, value := range config.OperatorDeploymentSelector {
			if labels[key] == value {
				return true
			}
		}
	}

	return false
}

func isRoleBinding(obj unstructured.Unstructured) bool {
	return obj.GroupVersionKind() == roleBindingGVK
}

func isClusterRoleBinding(obj unstructured.Unstructured) bool {
	return obj.GroupVersionKind() == clusterRoleBindingGVK
}

func deploymentFromUnstructured(obj unstructured.Unstructured) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{}
	err := convertFromUnstructured(obj, deployment)
	return deployment, err
}

// imageComponents represents the parsed components of a container image reference
type imageComponents struct {
	registry   string
	repository string // includes the full repository path (previously repository + name)
	tag        string // mutually exclusive with digest
	digest     string // mutually exclusive with tag
}

// imageRefRegex parses container image references
// Pattern: [registry/]repository[:tag|@sha256:digest]
// Examples:
//   - registry.io/repo/image:tag -> registry="registry.io", repository="repo/image", tag="tag", digest=""
//   - registry.io/org/team/image:tag -> registry="registry.io", repository="org/team/image", tag="tag", digest=""
//   - registry.io/image:tag -> registry="registry.io", repository="image", tag="tag", digest=""
//   - localhost:5000/repo/image:tag -> registry="localhost:5000", repository="repo/image", tag="tag", digest=""
//   - myimage:tag -> registry="", repository="myimage", tag="tag", digest=""
//   - registry.io/repo/image@sha256:abc123 -> registry="registry.io", repository="repo/image", tag="", digest="abc123"
var imageRefRegex = regexp.MustCompile(`^(?:([^/]+)/)?([^:@]+)(?::(.+)|@sha256:([a-f0-9]+))?$`)

// parseImageReference parses a container image reference into its components
func parseImageReference(imageRef string) (*imageComponents, error) {
	matches := imageRefRegex.FindStringSubmatch(imageRef)
	if matches == nil {
		return nil, fmt.Errorf("invalid image reference format: %s", imageRef)
	}

	return &imageComponents{
		registry:   matches[1], // can be empty
		repository: matches[2], // required, includes full repository path
		tag:        matches[3], // can be empty, mutually exclusive with digest
		digest:     matches[4], // can be empty, mutually exclusive with tag
	}, nil
}

// buildImageReference reconstructs an image reference from components
func (ic *imageComponents) buildImageReference() string {
	var result string

	if ic.registry != "" {
		result = ic.registry + "/" + ic.repository
	} else {
		// No registry, just the repository
		result = ic.repository
	}

	if ic.tag != "" {
		result += ":" + ic.tag
	} else if ic.digest != "" {
		result += "@sha256:" + ic.digest
	}

	return result
}

// parameterizeImageComponents applies the appropriate parameterization based on config with optional suffix
func parameterizeImageComponents(imageRef string, config *BundleConfig, suffix string) (string, map[string]string) {
	params := make(map[string]string)

	components, err := parseImageReference(imageRef)
	if err != nil {
		return imageRef, params // return original if parsing fails
	}

	if config.ImageRegistryParam != "" && components.registry != "" {
		paramName := config.ImageRegistryParam
		if suffix != "" {
			paramName = paramName + suffix
		}
		params[paramName] = components.registry
		components.registry = fmt.Sprintf("{{ .Values.%s }}", paramName)
	}

	if config.ImageRootRepositoryParam != "" {
		// Only root repository param is set - replace only the root part
		paramName := config.ImageRootRepositoryParam
		if suffix != "" {
			paramName = paramName + suffix
		}
		// Split repository into root and remaining parts
		repositoryParts := strings.SplitN(components.repository, "/", 2)
		params[paramName] = repositoryParts[0]
		if len(repositoryParts) > 1 {
			// Has a root part and remaining parts: rootRepo/remaining/path
			components.repository = fmt.Sprintf("{{ .Values.%s }}/%s", paramName, repositoryParts[1])
		} else {
			// No slash in repository - replace the entire repository
			components.repository = fmt.Sprintf("{{ .Values.%s }}", paramName)
		}
	} else if config.ImageRepositoryParam != "" {
		// Only repository param is set - replace entire repository
		paramName := config.ImageRepositoryParam
		if suffix != "" {
			paramName = paramName + suffix
		}
		params[paramName] = components.repository
		components.repository = fmt.Sprintf("{{ .Values.%s }}", paramName)
	}

	if config.ImageTagParam != "" {
		paramName := config.ImageTagParam
		if suffix != "" {
			paramName = paramName + suffix
		}
		// Force tag format - clear digest and set tag
		components.digest = ""
		params[paramName] = components.tag
		components.tag = fmt.Sprintf("{{ .Values.%s }}", paramName)
	} else if config.ImageDigestParam != "" {
		paramName := config.ImageDigestParam
		if suffix != "" {
			paramName = paramName + suffix
		}
		// Force digest format - clear tag and set digest
		components.tag = ""
		params[paramName] = components.digest
		components.digest = fmt.Sprintf("{{ .Values.%s }}", paramName)
	}

	return components.buildImageReference(), params
}
