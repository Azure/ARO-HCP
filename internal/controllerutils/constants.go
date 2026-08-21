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

package controllerutils

const (
	// Fingerprint for direct comparison with HyperShift's SecretEncryption History fingerprints
	// and to avoid leaking sensitive information such as the KMS key version in logs or metrics.
	HcpClusterKmsKeyFingerprintAnnotation = "azure.microsoft.com/hcp-cluster-kms-key-fingerprint"
	HcpClusterAzureResourceIdAnnotation   = "azure.microsoft.com/hcp-cluster-azure-resource-id"
)
