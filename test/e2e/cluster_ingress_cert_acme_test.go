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

package e2e

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

func TestUnstructuredClusterIssuerCarriesCABundle(t *testing.T) {
	caBundle := []byte("-----BEGIN CERTIFICATE-----\nZm9v\n-----END CERTIFICATE-----\n")
	issuer := unstructuredClusterIssuer(ingressCertificateIssuerName, "noreply@redhat.com", framework.PebbleDirectoryURL, caBundle,
		"aro.example.com", "rg", "sub", "client")
	server, _, err := unstructured.NestedString(issuer.Object, "spec", "acme", "server")
	require.NoError(t, err)
	require.Equal(t, framework.PebbleDirectoryURL, server)
	encoded, _, err := unstructured.NestedString(issuer.Object, "spec", "acme", "caBundle")
	require.NoError(t, err)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	require.Equal(t, caBundle, decoded)
}
