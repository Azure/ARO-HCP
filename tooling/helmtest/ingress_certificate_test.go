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

package main

import (
	"bytes"
	"os"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/common/util"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/engine"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func TestIngressCertificatePolicyOwnership(t *testing.T) {
	source, err := os.ReadFile("../../acm/deploy/helm/policies/templates/default-ingress-cert.policy.yaml")
	require.NoError(t, err)
	chart := &chartv2.Chart{
		Metadata:  &chartv2.Metadata{Name: "policy", APIVersion: "v2"},
		Templates: []*common.File{{Name: "templates/default-ingress-cert.policy.yaml", Data: source}},
	}
	values, err := util.ToRenderValues(chart, nil, common.ReleaseOptions{Name: "policy", Namespace: "policies", IsInstall: true}, nil)
	require.NoError(t, err)
	manifests, err := engine.Render(chart, values)
	require.NoError(t, err)
	var policy unstructured.Unstructured
	require.NoError(t, yaml.Unmarshal([]byte(manifests["policy/templates/default-ingress-cert.policy.yaml"]), &policy.Object))
	policies, _, err := unstructured.NestedSlice(policy.Object, "spec", "policy-templates")
	require.NoError(t, err)
	raw, _, err := unstructured.NestedString(policies[1].(map[string]any), "objectDefinition", "spec", "object-templates-raw")
	require.NoError(t, err)

	for _, scenario := range []struct {
		name      string
		selection string
		current   *string
		disabled  bool
		want      string
	}{
		{name: "initialize legacy delivery", selection: "cluster-ingress-cert", want: "cluster-ingress-cert"},
		{name: "repair internal certificate reference", selection: "cluster-ingress-cert", current: new("default-ingress-cert"), want: "cluster-ingress-cert"},
		{name: "respect customer certificate", selection: "cluster-ingress-cert", current: new("customer-certificate")},
		{name: "initialize native delivery", want: "default-ingress-cert"},
		{name: "initialize existing controller without a certificate", selection: "cluster-ingress-cert", current: new(""), want: "cluster-ingress-cert"},
		{name: "disable managed certificates", disabled: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			config := map[string]string{"default-certificate-name": scenario.selection, "endpoint-publishing-strategy": "external"}
			if scenario.disabled {
				config["disable-certificates"] = "true"
			}
			hub, err := template.New("hub").Delims("{{hub", "hub}}").Funcs(template.FuncMap{
				"fromConfigMap": func(_, _, key string) string { return config[key] },
			}).Parse(raw)
			require.NoError(t, err)
			var hubOutput bytes.Buffer
			require.NoError(t, hub.Execute(&hubOutput, map[string]any{"ManagedClusterName": "cluster", "PolicyMetadata": map[string]string{"namespace": "policies"}}))

			guest := map[string]any{}
			if scenario.current != nil {
				guest["spec"] = map[string]any{}
				if *scenario.current != "" {
					guest["spec"].(map[string]any)["defaultCertificate"] = map[string]any{"name": *scenario.current}
				}
			}
			managed, err := template.New("managed").Funcs(template.FuncMap{
				"lookup": func(_, _, _, _ string) map[string]any { return guest },
			}).Parse(hubOutput.String())
			require.NoError(t, err)
			var managedOutput bytes.Buffer
			require.NoError(t, managed.Execute(&managedOutput, nil))
			var objects []map[string]any
			require.NoError(t, yaml.Unmarshal(managedOutput.Bytes(), &objects))
			reference, found, err := unstructured.NestedString(objects[0], "objectDefinition", "spec", "defaultCertificate", "name")
			require.NoError(t, err)
			if scenario.want == "" {
				require.False(t, found, "the policy must not manage the customer's certificate reference")
			} else {
				require.Equal(t, scenario.want, reference)
			}
		})
	}
}
