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

package framework

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RegistryAuth represents authentication credentials for a single container
// image registry. It models one entry inside the "auths" map of a
// kubernetes.io/dockerconfigjson Secret. The Auth field is a base64 encoding
// of "username:password"; Username and Email are optional metadata.
//
// See https://kubernetes.io/docs/concepts/configuration/secret/#docker-config-secrets
type RegistryAuth struct {
	Username string `json:"username,omitempty"`
	Email    string `json:"email,omitempty"`
	Auth     string `json:"auth"`
}

// DockerConfigJSON is the root structure stored under the .dockerconfigjson
// key of a kubernetes.io/dockerconfigjson Secret. Auths maps registry
// hostnames (e.g. "quay.io", "registry.redhat.io") to their credentials.
//
// See https://kubernetes.io/docs/concepts/configuration/secret/#docker-config-secrets
type DockerConfigJSON struct {
	Auths map[string]RegistryAuth `json:"auths"`
}

// CreateTestDockerConfigSecret builds a corev1.Secret of type
// kubernetes.io/dockerconfigjson containing credentials for a single registry.
// It returns both the Secret and the RegistryAuth it constructed, so callers
// can pass the auth data directly to verifiers without recomputing it.
// The returned Secret is suitable for use as the HCCO "additional-pull-secret"
// in kube-system, which HCCO merges into the cluster's global pull secret.
//
// See https://hypershift.pages.dev/how-to/aws/global-pull-secret/
func CreateTestDockerConfigSecret(host, username, password, email, secretName, namespace string) (*corev1.Secret, RegistryAuth, error) {
	registryAuth := RegistryAuth{
		Email: email,
		Auth:  base64.StdEncoding.EncodeToString([]byte(username + ":" + password)),
	}

	dockerConfig := DockerConfigJSON{
		Auths: map[string]RegistryAuth{
			host: registryAuth,
		},
	}

	dockerConfigJSON, err := json.Marshal(dockerConfig)
	if err != nil {
		return nil, RegistryAuth{}, fmt.Errorf("failed to marshal docker config: %w", err)
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: dockerConfigJSON,
		},
	}, registryAuth, nil
}

// AddRegistryAuthToSecret adds or replaces a registry entry in an existing
// dockerconfigjson Secret. It unmarshals the Secret's current .dockerconfigjson
// data, inserts (or overwrites) the entry for host, and marshals the result
// back into the Secret's Data field. The caller is responsible for applying the
// updated Secret to the cluster (e.g. via a Kubernetes Update call).
func AddRegistryAuthToSecret(secret *corev1.Secret, host string, registryAuth RegistryAuth) error {
	var config DockerConfigJSON
	if err := json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &config); err != nil {
		return fmt.Errorf("failed to unmarshal pull secret: %w", err)
	}

	config.Auths[host] = registryAuth

	updated, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal updated docker config: %w", err)
	}

	secret.Data[corev1.DockerConfigJsonKey] = updated
	return nil
}
