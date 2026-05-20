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

// this is a temporary solution for partition key flexibility in CRUDs until
// https://github.com/Azure/ARO-HCP/pull/5094 lands

package fleet

func (s *Stamp) GetStampIdentifier() string {
	if s.CosmosMetadata.ResourceID == nil {
		return ""
	}
	return s.CosmosMetadata.ResourceID.Name
}

func (mc *ManagementCluster) GetStampIdentifier() string {
	if mc.CosmosMetadata.ResourceID == nil || mc.CosmosMetadata.ResourceID.Parent == nil {
		return ""
	}
	return mc.CosmosMetadata.ResourceID.Parent.Name
}
