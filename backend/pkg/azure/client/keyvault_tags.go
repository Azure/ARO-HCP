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

package client

import (
	"strings"
)

const (
	KeyVaultTagSubscription  = "subscription"
	KeyVaultTagResourceGroup = "resource_group"
	KeyVaultTagCluster       = "cluster"
	KeyVaultTagBinarySource  = "binary_source"

	KeyVaultBinarySourceBackend = "backend"
)

func KeyVaultSecretTags(binarySource, subscriptionID, resourceGroupName, clusterName string) map[string]*string {
	subscription := strings.ToLower(subscriptionID)
	resourceGroup := strings.ToLower(resourceGroupName)
	cluster := strings.ToLower(clusterName)
	source := strings.ToLower(binarySource)
	return map[string]*string{
		KeyVaultTagSubscription:  &subscription,
		KeyVaultTagResourceGroup: &resourceGroup,
		KeyVaultTagCluster:       &cluster,
		KeyVaultTagBinarySource:  &source,
	}
}

func KeyVaultSecretTagsMatch(tags map[string]*string, binarySource, subscriptionID, resourceGroupName, clusterName string) bool {
	expected := KeyVaultSecretTags(binarySource, subscriptionID, resourceGroupName, clusterName)
	for k, ev := range expected {
		av, ok := tags[k]
		if !ok || av == nil || *av != *ev {
			return false
		}
	}
	return true
}
