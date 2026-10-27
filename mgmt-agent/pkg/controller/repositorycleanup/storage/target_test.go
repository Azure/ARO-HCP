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

package storage

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const testNamespace = "ocm-arohcpint-0123456789abcdefghijklmnopqrstuv"

func testTarget() Target {
	return Target{"https://account123.blob.core.windows.net", "backups", "velero/kopia/" + testNamespace + "/"}
}

func testObjects() (*unstructured.Unstructured, *unstructured.Unstructured) {
	repo := &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"name": "repository", "namespace": "velero"},
		"spec":     map[string]interface{}{"repositoryType": "kopia", "volumeNamespace": testNamespace, "backupStorageLocation": "default"},
	}}
	bsl := &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"name": "default", "namespace": "velero"},
		"spec": map[string]interface{}{
			"provider":      "azure",
			"objectStorage": map[string]interface{}{"bucket": "backups", "prefix": "velero"},
			"config":        map[string]interface{}{"storageAccount": "account123", "useAAD": "true", "resourceGroup": "rg", "subscriptionId": "subscription"},
		},
	}}
	return repo, bsl
}

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name, field, value string
		valid              bool
	}{
		{"current", "prefix", testTarget().Prefix, true},
		{"control plane", "prefix", "velero/kopia/" + testNamespace + "-cluster-1/", true},
		{"multi base", "prefix", "team/backups.v1/velero/kopia/" + testNamespace + "/", true},
		{"empty prefix", "prefix", "", false},
		{"root", "prefix", "/", false},
		{"no base", "prefix", "kopia/" + testNamespace + "/", false},
		{"partial namespace", "prefix", "velero/kopia/ocm-arohcpint-012/", false},
		{"no boundary", "prefix", strings.TrimSuffix(testTarget().Prefix, "/"), false},
		{"namespace only", "prefix", "velero/" + testNamespace + "/", false},
		{"child path", "prefix", testTarget().Prefix + "data/", false},
		{"absolute", "prefix", "/" + testTarget().Prefix, false},
		{"empty interior", "prefix", "team//" + testTarget().Prefix, false},
		{"dot", "prefix", "./" + testTarget().Prefix, false},
		{"dotdot", "prefix", "team/../" + testTarget().Prefix, false},
		{"encoded traversal", "prefix", "%2e%2e/" + testTarget().Prefix, false},
		{"backslash", "prefix", "team\\" + testTarget().Prefix, false},
		{"space", "prefix", "team /" + testTarget().Prefix, false},
		{"env numeric", "prefix", "velero/kopia/ocm-1int-0123456789abcdefghijklmnopqrstuv/", false},
		{"long env", "prefix", "velero/kopia/ocm-arohcpintxx-0123456789abcdefghijklmnopqrstuv/", false},
		{"dot namespace", "prefix", "velero/kopia/" + testNamespace + "-name.dot/", false},
		{"long namespace", "prefix", "velero/kopia/" + testNamespace + "-" + strings.Repeat("a", 30) + "/", false},
		{"dangling hyphen", "prefix", "velero/kopia/" + testNamespace + "-/", false},
		{"huge base", "prefix", strings.Repeat("a", 1024) + "/" + testTarget().Prefix, false},
		{"endpoint slash", "url", testTarget().AccountURL + "/", true},
		{"minimum account", "url", "https://abc.blob.core.windows.net", true},
		{"maximum account", "url", "https://" + strings.Repeat("a", 24) + ".blob.core.windows.net", true},
		{"short account", "url", "https://ab.blob.core.windows.net", false},
		{"long account", "url", "https://" + strings.Repeat("a", 25) + ".blob.core.windows.net", false},
		{"uppercase", "url", "https://Account.blob.core.windows.net", false},
		{"http", "url", "http://account.blob.core.windows.net", false},
		{"foreign host", "url", "https://attacker.example", false},
		{"suffix attack", "url", "https://account.blob.core.windows.net.evil.example", false},
		{"sovereign cloud", "url", "https://account.blob.core.usgovcloudapi.net", false},
		{"dfs", "url", "https://account.dfs.core.windows.net", false},
		{"port", "url", testTarget().AccountURL + ":443", false},
		{"credentials", "url", "https://user:password@account.blob.core.windows.net", false},
		{"SAS", "url", testTarget().AccountURL + "?sig=secret", false},
		{"empty query", "url", testTarget().AccountURL + "?", false},
		{"fragment", "url", testTarget().AccountURL + "#", false},
		{"path", "url", testTarget().AccountURL + "/backups", false},
		{"encoded slash", "url", testTarget().AccountURL + "/%2f", false},
		{"valid container", "container", "abc-123", true},
		{"max container", "container", strings.Repeat("a", 63), true},
		{"empty container", "container", "", false},
		{"short container", "container", "ab", false},
		{"long container", "container", strings.Repeat("a", 64), false},
		{"double hyphen", "container", "abc--def", false},
		{"leading hyphen", "container", "-abc", false},
		{"trailing hyphen", "container", "abc-", false},
		{"uppercase container", "container", "Abc", false},
		{"container dot", "container", "abc.def", false},
		{"root container", "container", "$root", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := testTarget()
			switch tc.field {
			case "prefix":
				target.Prefix = tc.value
			case "url":
				target.AccountURL = tc.value
			case "container":
				target.Container = tc.value
			}
			if err := Validate(target); (err == nil) != tc.valid {
				t.Fatalf("Validate(%+v) = %v, valid=%t", target, err, tc.valid)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name, object, field string
		value               interface{}
		valid               bool
		prefix              string
	}{
		{name: "current", valid: true},
		{"trim slashes", "bsl", "spec.objectStorage.prefix", "/velero/", true, testTarget().Prefix},
		{"multi base", "bsl", "spec.objectStorage.prefix", "team/velero", true, "team/" + testTarget().Prefix},
		{"control plane", "repo", "spec.volumeNamespace", testNamespace + "-cluster", true, "velero/kopia/" + testNamespace + "-cluster/"},
		{"explicit URI", "bsl", "spec.config.storageAccountURI", testTarget().AccountURL + "/", true, ""},
		{"authority", "bsl", "spec.config.activeDirectoryAuthorityURI", "https://login.microsoftonline.com/", true, ""},
		{"restic", "repo", "spec.repositoryType", "restic", false, ""},
		{"missing type", "repo", "spec.repositoryType", nil, false, ""},
		{"provider", "bsl", "spec.provider", "aws", false, ""},
		{"missing provider", "bsl", "spec.provider", nil, false, ""},
		{"namespace mismatch", "bsl", "metadata.namespace", "other", false, ""},
		{"reference mismatch", "repo", "spec.backupStorageLocation", "other", false, ""},
		{"missing reference", "repo", "spec.backupStorageLocation", nil, false, ""},
		{"invalid namespace", "repo", "spec.volumeNamespace", "default", false, ""},
		{"traversing namespace", "repo", "spec.volumeNamespace", testNamespace + "/../other", false, ""},
		{"missing bucket", "bsl", "spec.objectStorage.bucket", nil, false, ""},
		{"missing prefix", "bsl", "spec.objectStorage.prefix", nil, false, ""},
		{"empty prefix", "bsl", "spec.objectStorage.prefix", "", false, ""},
		{"root prefix", "bsl", "spec.objectStorage.prefix", "///", false, ""},
		{"dotdot prefix", "bsl", "spec.objectStorage.prefix", "velero/../other", false, ""},
		{"empty segment", "bsl", "spec.objectStorage.prefix", "velero//other", false, ""},
		{"wrong prefix type", "bsl", "spec.objectStorage.prefix", int64(1), false, ""},
		{"unknown shape", "bsl", "spec.objectStorage", nil, false, ""},
		{"missing account", "bsl", "spec.config.storageAccount", nil, false, ""},
		{"wrong config type", "bsl", "spec.config", "account123", false, ""},
		{"URI mismatch", "bsl", "spec.config.storageAccountURI", "https://another.blob.core.windows.net", false, ""},
		{"URI SAS", "bsl", "spec.config.storageAccountURI", testTarget().AccountURL + "?sig=secret", false, ""},
		{"foreign authority", "bsl", "spec.config.activeDirectoryAuthorityURI", "https://attacker.example", false, ""},
		{"endpoint override", "bsl", "spec.config.blobEndpoint", "https://attacker.example", false, ""},
		{"cloud override", "bsl", "spec.config.cloudName", "AzureUSGovernmentCloud", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, bsl := testObjects()
			if tc.field != "" {
				object := bsl
				if tc.object == "repo" {
					object = repo
				}
				fields := strings.Split(tc.field, ".")
				if tc.value == nil {
					unstructured.RemoveNestedField(object.Object, fields...)
				} else if err := unstructured.SetNestedField(object.Object, tc.value, fields...); err != nil {
					t.Fatal(err)
				}
			}
			beforeRepo, beforeBSL := repo.DeepCopy(), bsl.DeepCopy()
			got, err := Resolve(repo, bsl)
			if (err == nil) != tc.valid {
				t.Fatalf("Resolve() = %+v, %v, valid=%t", got, err, tc.valid)
			}
			if !reflect.DeepEqual(repo, beforeRepo) || !reflect.DeepEqual(bsl, beforeBSL) {
				t.Fatal("Resolve mutated its inputs")
			}
			if !tc.valid {
				if got != (Target{}) {
					t.Fatalf("invalid resolution returned a target: %+v", got)
				}
				return
			}
			want := testTarget()
			if tc.prefix != "" {
				want.Prefix = tc.prefix
			}
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
	if _, err := Resolve(nil, nil); err == nil {
		t.Fatal("nil inputs accepted")
	}
}

func TestTargetJSON(t *testing.T) {
	data, err := json.Marshal(testTarget())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fields, map[string]string{"accountURL": testTarget().AccountURL, "container": "backups", "prefix": testTarget().Prefix}) {
		t.Fatalf("unexpected JSON contract: %s", data)
	}
}
