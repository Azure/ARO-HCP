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

package clients

import (
	"testing"
)

func TestNewGenericRegistryClient(t *testing.T) {
	registryURL := "mcr.microsoft.com"
	client := NewGenericRegistryClient(registryURL)

	if client == nil {
		t.Fatal("NewGenericRegistryClient() returned nil")
	}

	if client.registryURL != registryURL {
		t.Errorf("NewGenericRegistryClient() registryURL = %v, want %v", client.registryURL, registryURL)
	}

	if client.httpClient == nil {
		t.Error("NewGenericRegistryClient() httpClient should not be nil")
	}
}

func TestGenericRegistryClient_getAllTags(t *testing.T) {
	t.Run("error on invalid repository", func(t *testing.T) {
		client := NewGenericRegistryClient("invalid-registry-that-does-not-exist.example.com")
		_, err := client.getAllTags("test/repo")
		if err == nil {
			t.Error("getAllTags() expected error for invalid registry, got nil")
		}
	})
}
