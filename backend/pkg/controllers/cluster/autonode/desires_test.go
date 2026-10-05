// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package autonode

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
)

// TestBuildAutoNodePayload_GoldenJSON pins the exact wire shape this
// controller server-side-applies onto the HostedCluster. This is a
// golden-JSON shape assertion only, not schema validation against the
// fork's CRD: a static CRD-schema fixture sourced from a personal fork
// checkout would rot silently against whatever eventually ships upstream.
// Real structural validation, if wanted later, must be derived from the
// actual pinned hypershift image at test time - not built here. This test's
// job is narrower and cheaper: catch an accidental field-name/json-tag typo
// in the hand-marshaled types (see the doc comment on autoNodeSpec) before
// it reaches a real management cluster.
func TestBuildAutoNodePayload_GoldenJSON(t *testing.T) {
	target := kubeapplierapi.ResourceReference{
		Group:     "hypershift.openshift.io",
		Version:   "v1beta1",
		Resource:  "hostedclusters",
		Namespace: "ocm-test-env-clu123",
		Name:      "test-domain-prefix",
	}

	raw, err := buildAutoNodePayload(target, "11111111-2222-3333-4444-555555555555", ImageOverrides{})
	require.NoError(t, err)

	const golden = `{` +
		`"apiVersion":"hypershift.openshift.io/v1beta1",` +
		`"kind":"HostedCluster",` +
		`"metadata":{"name":"test-domain-prefix","namespace":"ocm-test-env-clu123"},` +
		`"spec":{"autoNode":{"provisionerConfig":{"name":"Karpenter","karpenter":{"platform":"Azure","azure":{"clientID":"11111111-2222-3333-4444-555555555555"}}}}}` +
		`}`

	assert.JSONEq(t, golden, string(raw))
}

func TestBuildAutoNodePayload_RejectsEmptyClientID(t *testing.T) {
	_, err := buildAutoNodePayload(kubeapplierapi.ResourceReference{}, "", ImageOverrides{})
	assert.Error(t, err)
}

// TestBuildAutoNodePayload_SetsImageOverrideAnnotations pins that the
// personal-dev-only image overrides (see the karpenterOperatorImageAnnotation
// doc comment) only ever appear on the wire when explicitly configured, and
// never leak an annotations key at all when both overrides are empty (the
// golden-JSON test above already covers that zero-value case).
func TestBuildAutoNodePayload_SetsImageOverrideAnnotations(t *testing.T) {
	target := kubeapplierapi.ResourceReference{
		Namespace: "ocm-test-env-clu123",
		Name:      "test-domain-prefix",
	}

	raw, err := buildAutoNodePayload(target, "11111111-2222-3333-4444-555555555555", ImageOverrides{
		KarpenterOperatorImage:      "example.azurecr.io/karpenter-operator:demo",
		KarpenterProviderAzureImage: "example.azurecr.io/karpenter-azure:demo",
	})
	require.NoError(t, err)

	const golden = `{` +
		`"apiVersion":"hypershift.openshift.io/v1beta1",` +
		`"kind":"HostedCluster",` +
		`"metadata":{` +
		`"name":"test-domain-prefix",` +
		`"namespace":"ocm-test-env-clu123",` +
		`"annotations":{` +
		`"hypershift.openshift.io/karpenter-operator-image":"example.azurecr.io/karpenter-operator:demo",` +
		`"hypershift.openshift.io/karpenter-provider-azure-image":"example.azurecr.io/karpenter-azure:demo"` +
		`}` +
		`},` +
		`"spec":{"autoNode":{"provisionerConfig":{"name":"Karpenter","karpenter":{"platform":"Azure","azure":{"clientID":"11111111-2222-3333-4444-555555555555"}}}}}` +
		`}`

	assert.JSONEq(t, golden, string(raw))
}
