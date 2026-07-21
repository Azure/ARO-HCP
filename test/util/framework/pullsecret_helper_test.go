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

package framework

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
)

func TestCreateTestDockerConfigSecret(t *testing.T) {
	t.Parallel()

	secret, registryAuth, err := CreateTestDockerConfigSecret(
		"registry.example.com",
		"user",
		"pass",
		"user@example.com",
		"my-secret",
		"my-namespace",
	)
	require.NoError(t, err)

	assert.Equal(t, "my-secret", secret.Name)
	assert.Equal(t, "my-namespace", secret.Namespace)
	assert.Equal(t, corev1.SecretTypeDockerConfigJson, secret.Type)

	expectedAuth := base64.StdEncoding.EncodeToString([]byte("user:pass"))
	assert.Equal(t, expectedAuth, registryAuth.Auth)
	assert.Equal(t, "user@example.com", registryAuth.Email)

	var config DockerConfigJSON
	require.NoError(t, json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &config))

	hostAuth, exists := config.Auths["registry.example.com"]
	assert.True(t, exists, "expected registry.example.com in auths")
	assert.Equal(t, registryAuth, hostAuth, "returned RegistryAuth must match what is in the Secret")
}

func TestAddRegistryAuthToSecret(t *testing.T) {
	t.Parallel()

	secret, originalAuth, err := CreateTestDockerConfigSecret(
		"original.example.com",
		"user1",
		"pass1",
		"user1@example.com",
		"test-secret",
		"default",
	)
	require.NoError(t, err)

	newAuth := RegistryAuth{
		Auth:  base64.StdEncoding.EncodeToString([]byte("user2:pass2")),
		Email: "user2@example.com",
	}
	err = AddRegistryAuthToSecret(secret, "new.example.com", newAuth)
	require.NoError(t, err)

	var config DockerConfigJSON
	require.NoError(t, json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &config))

	assert.Contains(t, config.Auths, "original.example.com", "original entry must be preserved")
	assert.Equal(t, originalAuth, config.Auths["original.example.com"])
	assert.Contains(t, config.Auths, "new.example.com", "new entry must be present")
	assert.Equal(t, newAuth, config.Auths["new.example.com"])
}

func TestAddRegistryAuthToSecret_OverwritesExisting(t *testing.T) {
	t.Parallel()

	secret, _, err := CreateTestDockerConfigSecret(
		"registry.example.com",
		"user",
		"pass",
		"old@example.com",
		"test-secret",
		"default",
	)
	require.NoError(t, err)

	updatedAuth := RegistryAuth{
		Auth:  base64.StdEncoding.EncodeToString([]byte("newuser:newpass")),
		Email: "new@example.com",
	}
	err = AddRegistryAuthToSecret(secret, "registry.example.com", updatedAuth)
	require.NoError(t, err)

	var config DockerConfigJSON
	require.NoError(t, json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &config))

	assert.Len(t, config.Auths, 1)
	assert.Equal(t, updatedAuth, config.Auths["registry.example.com"])
}
