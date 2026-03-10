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

package fpa

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/internal/certificate"
)

func TestCredentialRetrieverLoadsInitialCertificate(t *testing.T) {
	certPEM, keyPEM, err := certificate.GenerateTestCertificate(t, 20)
	require.NoError(t, err)
	certData := append(keyPEM, certPEM...)

	certs, key, err := azidentity.ParseCertificates(certData, nil)
	require.NoError(t, err)

	mockReader := &certificate.MockReader{
		Certs: certs,
		Key:   key,
	}

	retriever, err := NewFirstPartyApplicationTokenCredentialRetriever(
		"11111111-1111-1111-1111-111111111111",
		mockReader,
		azcore.ClientOptions{},
	)
	require.NoError(t, err)

	cred, err := retriever.RetrieveCredential("tenant-id")
	require.NoError(t, err)
	assert.NotNil(t, cred)
}
