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

package systemadmincredential

import (
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateKeypair(t *testing.T) {
	pubPEM, privPEM, err := GenerateKeypair()
	require.NoError(t, err, "GenerateKeypair should succeed")

	// Verify public key decodes
	pubBlock, _ := pem.Decode(pubPEM)
	require.NotNil(t, pubBlock, "public key PEM should decode")
	require.Equal(t, "PUBLIC KEY", pubBlock.Type, "public key PEM should have correct type")
	_, err = x509.ParsePKIXPublicKey(pubBlock.Bytes)
	require.NoError(t, err, "public key should be parseable")

	// Verify private key decodes
	privBlock, _ := pem.Decode(privPEM)
	require.NotNil(t, privBlock, "private key PEM should decode")
	require.Equal(t, "PRIVATE KEY", privBlock.Type, "private key PEM should have correct type")
	_, err = x509.ParsePKCS8PrivateKey(privBlock.Bytes)
	require.NoError(t, err, "private key should be parseable")
}
