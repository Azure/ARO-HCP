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

// BackendIdentityAzureClients is a type that contains the Azure clients that
// are used to interact with the Azure platform as the backend identity. The
// backend identity is used to interact with Red Hat side Azure infrastructure.
type BackendIdentityAzureClients struct {
	// DataplaneIdentitiesOIDCConfigurationBlobStorageClient is the blob storage client
	// that is used to interact with the Azure Storage Account Blob Service that contains the
	// OIDC configuration associated to the ARO-HCP Clusters' Data Plane Operators
	// Azure Identities
	DataplaneIdentitiesOIDCConfigurationBlobStorageClient BlobStorageClient
}
