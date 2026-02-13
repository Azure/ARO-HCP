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

package api

const (
	// ServiceProviderNodePoolResourceName is the name of the ServiceProviderNodePool resource.
	// ServiceProviderNodePool is a singleton resource and ARM convention is to
	// use the name "default" for singleton resources.
	ServiceProviderNodePoolResourceName = "default"
)

// ServiceProviderNodePool is used internally by controllers to track and pass information between them.
type ServiceProviderNodePool struct {
	// CosmosMetadata ResourceID is nested under the cluster so that association and cleanup work as expected
	// it will be the ServiceProviderNodePool type and the name default
	CosmosMetadata `json:"cosmosMetadata"`
}
