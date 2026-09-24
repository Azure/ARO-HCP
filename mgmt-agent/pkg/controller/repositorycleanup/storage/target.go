// Copyright 2026 Microsoft Corporation
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

// Package storage resolves and deletes precisely scoped Azure Kopia repositories.
package storage

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Target identifies one repository, never an entire container or account.
type Target struct {
	AccountURL string `json:"accountURL"`
	Container  string `json:"container"`
	Prefix     string `json:"prefix"`
}

var (
	accountEndpoint = regexp.MustCompile(`^https://[a-z0-9]{3,24}\.blob\.core\.windows\.net/?$`)
	accountName     = regexp.MustCompile(`^[a-z0-9]{3,24}$`)
	containerName   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	volumeNamespace = regexp.MustCompile(`^ocm-[a-z][a-z0-9]{0,9}-[a-z0-9]{32}(-[a-z0-9]([-a-z0-9]*[a-z0-9])?)?$`)
	pathSegment     = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

// Validate rejects ambiguous paths and endpoints before any credential is used.
func Validate(target Target) error {
	if !accountEndpoint.MatchString(target.AccountURL) {
		return fmt.Errorf("accountURL must be a public Azure HTTPS blob account endpoint without credentials, query, port, or path")
	}
	if !containerName.MatchString(target.Container) || strings.Contains(target.Container, "--") {
		return fmt.Errorf("container must be a 3-63 character Azure container DNS name")
	}
	if !strings.HasSuffix(target.Prefix, "/") || len(target.Prefix) > 1024 {
		return fmt.Errorf("repository prefix must end in / and fit an Azure blob name")
	}
	parts := strings.Split(strings.TrimSuffix(target.Prefix, "/"), "/")
	if len(parts) < 3 || parts[len(parts)-2] != "kopia" {
		return fmt.Errorf("repository prefix must be <base>/kopia/<ARO volume namespace>/")
	}
	for _, part := range parts {
		if !pathSegment.MatchString(part) || part == "." || part == ".." {
			return fmt.Errorf("repository prefix contains an unsafe or non-normalized path segment")
		}
	}
	namespace := parts[len(parts)-1]
	if !volumeNamespace.MatchString(namespace) || len(validation.IsDNS1123Label(namespace)) != 0 {
		return fmt.Errorf("repository prefix does not end in an ARO volume namespace DNS label")
	}
	return nil
}

// Resolve uses only the current Azure/Kopia BSL shape. It does not consult the
// cloud, infer missing fields from status, or fall back to a broader prefix.
func Resolve(repo, bsl *unstructured.Unstructured) (Target, error) {
	if repo == nil || bsl == nil {
		return Target{}, fmt.Errorf("repository and backup storage location are required")
	}
	fields := []struct {
		object *unstructured.Unstructured
		path   []string
	}{
		{repo, []string{"spec", "repositoryType"}},
		{repo, []string{"spec", "volumeNamespace"}},
		{repo, []string{"spec", "backupStorageLocation"}},
		{bsl, []string{"spec", "provider"}},
		{bsl, []string{"spec", "objectStorage", "bucket"}},
		{bsl, []string{"spec", "objectStorage", "prefix"}},
	}
	values := make([]string, len(fields))
	for i, field := range fields {
		value, found, err := unstructured.NestedString(field.object.Object, field.path...)
		if err != nil || !found || value == "" {
			return Target{}, fmt.Errorf("%s must be a nonempty string", strings.Join(field.path, "."))
		}
		values[i] = value
	}
	if values[0] != "kopia" || values[3] != "azure" {
		return Target{}, fmt.Errorf("only Azure kopia repositories are supported")
	}
	if values[2] != bsl.GetName() || repo.GetNamespace() != bsl.GetNamespace() {
		return Target{}, fmt.Errorf("backup storage location does not match the repository reference")
	}
	config, found, err := unstructured.NestedStringMap(bsl.Object, "spec", "config")
	if err != nil || !found || !accountName.MatchString(config["storageAccount"]) {
		return Target{}, fmt.Errorf("spec.config.storageAccount must be a public Azure storage account name")
	}
	for key, value := range config {
		switch key {
		case "storageAccount", "storageAccountURI", "resourceGroup", "subscriptionId", "useAAD":
		case "activeDirectoryAuthorityURI":
			if strings.TrimSuffix(value, "/") != "https://login.microsoftonline.com" {
				return Target{}, fmt.Errorf("unsupported Azure authority endpoint")
			}
		default:
			return Target{}, fmt.Errorf("unsupported Azure storage configuration key %q", key)
		}
	}
	accountURL := "https://" + config["storageAccount"] + ".blob.core.windows.net"
	if uri, present := config["storageAccountURI"]; present && strings.TrimSuffix(uri, "/") != accountURL {
		return Target{}, fmt.Errorf("storageAccountURI must match the public Azure storageAccount endpoint")
	}
	base := strings.Trim(values[5], "/")
	// Check before path.Join: cleaning a malformed base would silently change scope.
	for _, segment := range strings.Split(base, "/") {
		if !pathSegment.MatchString(segment) || segment == "." || segment == ".." {
			return Target{}, fmt.Errorf("objectStorage.prefix must contain safe nonempty path segments")
		}
	}
	if !volumeNamespace.MatchString(values[1]) || len(validation.IsDNS1123Label(values[1])) != 0 {
		return Target{}, fmt.Errorf("volumeNamespace must be an ARO volume namespace DNS label")
	}
	target := Target{AccountURL: accountURL, Container: values[4], Prefix: path.Join(base, "kopia", values[1]) + "/"}
	if err := Validate(target); err != nil {
		return Target{}, err
	}
	return target, nil
}
