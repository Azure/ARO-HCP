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
	"context"
	"encoding/json"
	"fmt"

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// UpdateOpenShiftAzureNodeClassVersion changes only spec.version. The caller
// must wait for node replacement and check the version of the new nodes.
func UpdateOpenShiftAzureNodeClassVersion(ctx context.Context, adminRESTConfig *rest.Config, name, version string) error {
	if name == "" {
		return fmt.Errorf("the NodeClass name must not be empty")
	}
	if _, err := semver.Parse(version); err != nil || len(version) < 5 || len(version) > 64 {
		return fmt.Errorf("NodeClass version %q must be a full semantic version with 5 to 64 characters", version)
	}
	client, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"version": version}})
	if err != nil {
		return fmt.Errorf("failed to encode NodeClass version patch: %w", err)
	}
	if _, err := client.Resource(openShiftAzureNodeClassGVR).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("failed to set OpenShiftAzureNodeClass %q spec.version to %q: %w", name, version, err)
	}
	return nil
}
