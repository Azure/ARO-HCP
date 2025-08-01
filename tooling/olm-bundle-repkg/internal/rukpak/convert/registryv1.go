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

package convert

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing/fstest"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	apimachyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"

	registry "github.com/Azure/ARO-HCP/tooling/olm-bundle-repkg/internal/rukpak/operator-registry"

	"github.com/Azure/ARO-HCP/tooling/olm-bundle-repkg/internal/rukpak/util"
)

type RegistryV1 struct {
	PackageName string
	CSV         v1alpha1.ClusterServiceVersion
	CRDs        []apiextensionsv1.CustomResourceDefinition
	Others      []unstructured.Unstructured
}

type Plain struct {
	Objects []client.Object
}

func RegistryV1ToPlain(rv1 fs.FS, installNamespace string, watchNamespaces []string) (fs.FS, RegistryV1, error) {
	reg := RegistryV1{}
	fileData, err := fs.ReadFile(rv1, filepath.Join("metadata", "annotations.yaml"))
	if err != nil {
		return nil, reg, err
	}
	annotationsFile := registry.AnnotationsFile{}
	if err := yaml.Unmarshal(fileData, &annotationsFile); err != nil {
		return nil, reg, err
	}
	reg.PackageName = annotationsFile.Annotations.PackageName

	var objects []*unstructured.Unstructured
	const manifestsDir = "manifests"

	entries, err := fs.ReadDir(rv1, manifestsDir)
	if err != nil {
		return nil, reg, err
	}
	for _, e := range entries {
		if e.IsDir() {
			return nil, reg, fmt.Errorf("subdirectories are not allowed within the %q directory of the bundle image filesystem: found %q", manifestsDir, filepath.Join(manifestsDir, e.Name()))
		}
		fileData, err := fs.ReadFile(rv1, filepath.Join(manifestsDir, e.Name()))
		if err != nil {
			return nil, reg, err
		}

		dec := apimachyaml.NewYAMLOrJSONDecoder(bytes.NewReader(fileData), 1024)
		for {
			obj := unstructured.Unstructured{}
			err := dec.Decode(&obj)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, reg, fmt.Errorf("read %q: %v", e.Name(), err)
			}
			objects = append(objects, &obj)
		}
	}

	for _, obj := range objects {
		obj := obj
		switch obj.GetObjectKind().GroupVersionKind().Kind {
		case "ClusterServiceVersion":
			csv := v1alpha1.ClusterServiceVersion{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &csv); err != nil {
				return nil, reg, err
			}
			reg.CSV = csv
		case "CustomResourceDefinition":
			crd := apiextensionsv1.CustomResourceDefinition{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &crd); err != nil {
				return nil, reg, err
			}
			reg.CRDs = append(reg.CRDs, crd)
		default:
			reg.Others = append(reg.Others, *obj)
		}
	}

	plain, err := Convert(reg, installNamespace, watchNamespaces)
	if err != nil {
		return nil, reg, err
	}

	var manifest bytes.Buffer
	for _, obj := range plain.Objects {
		yamlData, err := yaml.Marshal(obj)
		if err != nil {
			return nil, reg, err
		}
		if _, err := fmt.Fprintf(&manifest, "---\n%s\n", string(yamlData)); err != nil {
			return nil, reg, err
		}
	}

	now := time.Now()
	plainFS := fstest.MapFS{
		".": &fstest.MapFile{
			Data:    nil,
			Mode:    fs.ModeDir | 0o755,
			ModTime: now,
		},
		"manifests": &fstest.MapFile{
			Data:    nil,
			Mode:    fs.ModeDir | 0o755,
			ModTime: now,
		},
		"manifests/manifest.yaml": &fstest.MapFile{
			Data:    manifest.Bytes(),
			Mode:    0o644,
			ModTime: now,
		},
	}

	return plainFS, reg, nil
}

func validateTargetNamespaces(supportedInstallModes sets.Set[string], installNamespace string, targetNamespaces []string) error {
	set := sets.New[string](targetNamespaces...)
	switch {
	case set.Len() == 0 || (set.Len() == 1 && set.Has("")):
		if supportedInstallModes.Has(string(v1alpha1.InstallModeTypeAllNamespaces)) {
			return nil
		}
		return fmt.Errorf("supported install modes %v do not support targeting all namespaces", sets.List(supportedInstallModes))
	case set.Len() == 1 && !set.Has(""):
		if supportedInstallModes.Has(string(v1alpha1.InstallModeTypeSingleNamespace)) {
			return nil
		}
		if supportedInstallModes.Has(string(v1alpha1.InstallModeTypeOwnNamespace)) && targetNamespaces[0] == installNamespace {
			return nil
		}
	default:
		if supportedInstallModes.Has(string(v1alpha1.InstallModeTypeMultiNamespace)) && !set.Has("") {
			return nil
		}
	}
	return fmt.Errorf("supported install modes %v do not support target namespaces %v", sets.List[string](supportedInstallModes), targetNamespaces)
}

func saNameOrDefault(saName string) string {
	if saName == "" {
		return "default"
	}
	return saName
}

func Convert(in RegistryV1, installNamespace string, targetNamespaces []string) (*Plain, error) {
	if installNamespace == "" {
		installNamespace = in.CSV.Annotations["operatorframework.io/suggested-namespace"]
	}
	if installNamespace == "" {
		installNamespace = fmt.Sprintf("%s-system", in.PackageName)
	}
	supportedInstallModes := sets.New[string]()
	for _, im := range in.CSV.Spec.InstallModes {
		if im.Supported {
			supportedInstallModes.Insert(string(im.Type))
		}
	}
	if len(targetNamespaces) == 0 {
		if supportedInstallModes.Has(string(v1alpha1.InstallModeTypeAllNamespaces)) {
			targetNamespaces = []string{""}
		} else if supportedInstallModes.Has(string(v1alpha1.InstallModeTypeOwnNamespace)) {
			targetNamespaces = []string{installNamespace}
		}
	}

	if err := validateTargetNamespaces(supportedInstallModes, installNamespace, targetNamespaces); err != nil {
		return nil, err
	}

	if len(in.CSV.Spec.APIServiceDefinitions.Owned) > 0 {
		return nil, fmt.Errorf("apiServiceDefintions are not supported")
	}

	if len(in.CSV.Spec.WebhookDefinitions) > 0 {
		return nil, fmt.Errorf("webhookDefinitions are not supported")
	}

	deployments := []appsv1.Deployment{}
	serviceAccounts := map[string]corev1.ServiceAccount{}
	for _, depSpec := range in.CSV.Spec.InstallStrategy.StrategySpec.DeploymentSpecs {
		annotations := util.MergeMaps(in.CSV.Annotations, depSpec.Spec.Template.Annotations)
		annotations["olm.targetNamespaces"] = strings.Join(targetNamespaces, ",")
		deployments = append(deployments, appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Deployment",
				APIVersion: appsv1.SchemeGroupVersion.String(),
			},

			ObjectMeta: metav1.ObjectMeta{
				Namespace:   installNamespace,
				Name:        depSpec.Name,
				Labels:      depSpec.Label,
				Annotations: annotations,
			},
			Spec: depSpec.Spec,
		})
		saName := saNameOrDefault(depSpec.Spec.Template.Spec.ServiceAccountName)
		serviceAccounts[saName] = newServiceAccount(installNamespace, saName)
	}

	// NOTES:
	//   1. There's an extra Role for OperatorConditions: get/update/patch; resourceName=csv.name
	//        - This is managed by the OperatorConditions controller here: https://github.com/operator-framework/operator-lifecycle-manager/blob/9ced412f3e263b8827680dc0ad3477327cd9a508/pkg/controller/operators/operatorcondition_controller.go#L106-L109
	//   2. There's an extra RoleBinding for the above mentioned role.
	//        - Every SA mentioned in the OperatorCondition.spec.serviceAccounts is a subject for this role binding: https://github.com/operator-framework/operator-lifecycle-manager/blob/9ced412f3e263b8827680dc0ad3477327cd9a508/pkg/controller/operators/operatorcondition_controller.go#L171-L177
	//   3. strategySpec.permissions are _also_ given a clusterrole/clusterrole binding.
	//  		- (for AllNamespaces mode only?)
	//			- (where does the extra namespaces get/list/watch rule come from?)

	roles := []rbacv1.Role{}
	roleBindings := []rbacv1.RoleBinding{}
	clusterRoles := []rbacv1.ClusterRole{}
	clusterRoleBindings := []rbacv1.ClusterRoleBinding{}

	permissions := in.CSV.Spec.InstallStrategy.StrategySpec.Permissions
	clusterPermissions := in.CSV.Spec.InstallStrategy.StrategySpec.ClusterPermissions
	allPermissions := append(permissions, clusterPermissions...)

	// Create all the service accounts
	for _, permission := range allPermissions {
		saName := saNameOrDefault(permission.ServiceAccountName)
		if _, ok := serviceAccounts[saName]; !ok {
			serviceAccounts[saName] = newServiceAccount(installNamespace, saName)
		}
	}

	// If we're in AllNamespaces mode, promote the permissions to clusterPermissions
	if len(targetNamespaces) == 1 && targetNamespaces[0] == "" {
		for _, p := range permissions {
			p.Rules = append(p.Rules, rbacv1.PolicyRule{
				Verbs:     []string{"get", "list", "watch"},
				APIGroups: []string{corev1.GroupName},
				Resources: []string{"namespaces"},
			})
		}
		clusterPermissions = append(clusterPermissions, permissions...)
		permissions = nil
	}

	for _, ns := range targetNamespaces {
		for _, permission := range permissions {
			saName := saNameOrDefault(permission.ServiceAccountName)
			name, err := generateName(fmt.Sprintf("%s-%s", in.CSV.Name, saName), permission)
			if err != nil {
				return nil, err
			}
			roles = append(roles, newRole(ns, name, permission.Rules))
			roleBindings = append(roleBindings, newRoleBinding(ns, name, name, installNamespace, saName))
		}
	}

	for _, permission := range clusterPermissions {
		saName := saNameOrDefault(permission.ServiceAccountName)
		name, err := generateName(fmt.Sprintf("%s-%s", in.CSV.Name, saName), permission)
		if err != nil {
			return nil, err
		}
		clusterRoles = append(clusterRoles, newClusterRole(name, permission.Rules))
		clusterRoleBindings = append(clusterRoleBindings, newClusterRoleBinding(name, name, installNamespace, saName))
	}

	objs := []client.Object{}
	for _, obj := range serviceAccounts {
		obj := obj
		if obj.GetName() != "default" {
			objs = append(objs, &obj)
		}
	}
	for _, obj := range roles {
		obj := obj
		objs = append(objs, &obj)
	}
	for _, obj := range roleBindings {
		obj := obj
		objs = append(objs, &obj)
	}
	for _, obj := range clusterRoles {
		obj := obj
		objs = append(objs, &obj)
	}
	for _, obj := range clusterRoleBindings {
		obj := obj
		objs = append(objs, &obj)
	}
	for _, obj := range in.CRDs {
		obj := obj
		objs = append(objs, &obj)
	}
	for _, obj := range in.Others {
		obj := obj
		obj.SetNamespace(installNamespace)
		objs = append(objs, &obj)
	}
	for _, obj := range deployments {
		obj := obj
		objs = append(objs, &obj)
	}
	return &Plain{Objects: objs}, nil
}

const maxNameLength = 63

func generateName(base string, o interface{}) (string, error) {
	hashStr, err := util.DeepHashObject(o)
	if err != nil {
		return "", err
	}
	if len(base)+len(hashStr) > maxNameLength {
		base = base[:maxNameLength-len(hashStr)-1]
	}

	return fmt.Sprintf("%s-%s", base, hashStr), nil
}

func newServiceAccount(namespace, name string) corev1.ServiceAccount {
	return corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func newRole(namespace, name string, rules []rbacv1.PolicyRule) rbacv1.Role {
	return rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Rules: rules,
	}
}

func newClusterRole(name string, rules []rbacv1.PolicyRule) rbacv1.ClusterRole {
	return rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRole",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Rules: rules,
	}
}

func newRoleBinding(namespace, name, roleName, saNamespace string, saNames ...string) rbacv1.RoleBinding {
	subjects := make([]rbacv1.Subject, 0, len(saNames))
	for _, saName := range saNames {
		subjects = append(subjects, rbacv1.Subject{
			Kind:      "ServiceAccount",
			Namespace: saNamespace,
			Name:      saName,
		})
	}
	return rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Subjects: subjects,
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     roleName,
		},
	}
}

func newClusterRoleBinding(name, roleName, saNamespace string, saNames ...string) rbacv1.ClusterRoleBinding {
	subjects := make([]rbacv1.Subject, 0, len(saNames))
	for _, saName := range saNames {
		subjects = append(subjects, rbacv1.Subject{
			Kind:      "ServiceAccount",
			Namespace: saNamespace,
			Name:      saName,
		})
	}
	return rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Subjects: subjects,
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     roleName,
		},
	}
}
