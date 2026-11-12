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

package coreapi_test

import (
	"encoding/json"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
)

func TestTLSCertificateURLRoundTripAndDeepCopy(test *testing.T) {
	vaultURL := "https://certificates.vault.azure.net/"
	for _, confirmed := range []bool{false, true} {
		reference := &coreapi.AzureTLSCertificateReference{KeyVaultURL: vaultURL, CertificateName: "certificate"}
		serializedReference, err := json.Marshal(reference)
		require.NoError(test, err)
		require.JSONEq(test, `{"keyVaultURL":"https://certificates.vault.azure.net/","certificateName":"certificate"}`, string(serializedReference))
		certificate := &coreapi.TLSCertificate{PendingReference: reference}
		if confirmed {
			certificate = &coreapi.TLSCertificate{AzureReference: reference}
		}
		original := &coreapi.ServiceProviderCluster{Status: coreapi.ServiceProviderClusterStatus{AzureResources: coreapi.AzureResources{
			KubeAPIServerCertificate: certificate,
			IngressCertificate:       certificate.DeepCopy(),
		}}}
		serialized, err := json.Marshal(original)
		require.NoError(test, err)
		var decoded coreapi.ServiceProviderCluster
		require.NoError(test, json.Unmarshal(serialized, &decoded))
		require.Equal(test, original, &decoded)
		copied := original.DeepCopy()
		require.Equal(test, original, copied)
		for _, copiedCertificate := range []*coreapi.TLSCertificate{copied.Status.AzureResources.KubeAPIServerCertificate, copied.Status.AzureResources.IngressCertificate} {
			copiedReference := copiedCertificate.PendingReference
			if confirmed {
				copiedReference = copiedCertificate.AzureReference
			}
			copiedReference.CertificateName = "changed"
			copiedReference.KeyVaultURL = "https://changed.vault.azure.net/"
		}
		require.Equal(test, "certificate", reference.CertificateName)
		require.Equal(test, vaultURL, reference.KeyVaultURL)
	}
	require.Equal(test, &coreapi.AzureTLSCertificateReference{}, (&coreapi.AzureTLSCertificateReference{}).DeepCopy())
}

func TestTLSCertificateUnsetReferences(test *testing.T) {
	for _, input := range []string{"{}", `{"pendingReference":null,"azureReference":null}`} {
		var certificate coreapi.TLSCertificate
		require.NoError(test, json.Unmarshal([]byte(input), &certificate))
		require.Nil(test, certificate.PendingReference)
		require.Nil(test, certificate.AzureReference)
		copied := certificate.DeepCopy()
		require.Nil(test, copied.PendingReference)
		require.Nil(test, copied.AzureReference)
		serialized, err := json.Marshal(copied)
		require.NoError(test, err)
		require.JSONEq(test, "{}", string(serialized))
	}
}

func TestDeepCopyCluster(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := coreapitesting.DeepCopyFuzzerFor(rand.NewSource(seed))

	for i := 0; i < 200; i++ {
		original := &coreapi.Cluster{}
		fuzzer.Fill(original)
		coreapitesting.DoDeepCopyTest(t, original, fuzzer)
	}
}

func TestServiceProviderClusterImageContentSources(test *testing.T) {
	test.Parallel()
	empty := []hsv1beta1.ImageContentSource{}
	populated := []hsv1beta1.ImageContentSource{{Source: "source.example.com", Mirrors: []string{"mirror.example.com"}}}
	tests := []struct {
		name    string
		sources *[]hsv1beta1.ImageContentSource
	}{
		{name: "unobserved"},
		{name: "observed empty", sources: &empty},
		{name: "observed populated", sources: &populated},
	}
	for _, testcase := range tests {
		test.Run(testcase.name, func(test *testing.T) {
			test.Parallel()
			original := &coreapi.ServiceProviderClusterStatus{ImageContentSources: testcase.sources}
			copied := original.DeepCopy()
			assert.Equal(test, original, copied)
			serialized, err := json.Marshal(original)
			require.NoError(test, err)
			var restored coreapi.ServiceProviderClusterStatus
			require.NoError(test, json.Unmarshal(serialized, &restored))
			assert.Equal(test, original.ImageContentSources, restored.ImageContentSources)
			if testcase.sources == nil {
				assert.NotContains(test, string(serialized), `"imageContentSources"`)
				return
			}
			require.NotNil(test, copied.ImageContentSources)
			originalLength := len(*testcase.sources)
			if len(*testcase.sources) > 0 {
				(*copied.ImageContentSources)[0].Source = "changed.example.com"
				(*copied.ImageContentSources)[0].Mirrors[0] = "changed-mirror.example.com"
				assert.Equal(test, "source.example.com", (*original.ImageContentSources)[0].Source)
				assert.Equal(test, []string{"mirror.example.com"}, (*original.ImageContentSources)[0].Mirrors)
			}
			*copied.ImageContentSources = append(*copied.ImageContentSources, hsv1beta1.ImageContentSource{Source: "extra.example.com"})
			assert.Len(test, *original.ImageContentSources, originalLength)
			assert.Len(test, *copied.ImageContentSources, originalLength+1)
		})
	}
}

func TestDeepCopyNodePool(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := coreapitesting.DeepCopyFuzzerFor(rand.NewSource(seed))

	for i := 0; i < 200; i++ {
		original := &coreapi.NodePool{}
		fuzzer.Fill(original)
		coreapitesting.DoDeepCopyTest(t, original, fuzzer)
	}
}

func TestDeepCopyOperation(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := coreapitesting.DeepCopyFuzzerFor(rand.NewSource(seed))

	for i := 0; i < 200; i++ {
		original := &coreapi.Operation{}
		fuzzer.Fill(original)
		coreapitesting.DoDeepCopyTest(t, original, fuzzer)
	}
}
