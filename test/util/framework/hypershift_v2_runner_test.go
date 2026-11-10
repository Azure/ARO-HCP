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

package framework

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

const v2TestResourceID = "/subscriptions/customer-sub/resourceGroups/customer-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/customer-cluster"

func TestHyperShiftV2ConfigValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*HyperShiftV2Config)
		want   string
	}{
		{"When the binary is missing, it should fail before provisioning", func(c *HyperShiftV2Config) { c.Binary += "-missing" }, "executable"},
		{"When management access is missing, it should fail before provisioning", func(c *HyperShiftV2Config) { c.ManagementKubeconfig = "" }, "HYPERSHIFT_V2_MANAGEMENT_KUBECONFIG"},
		{"When the management kubeconfig is invalid, it should fail before provisioning", func(c *HyperShiftV2Config) { c.ManagementKubeconfig += "-missing" }, "management kubeconfig"},
		{"When the artifact destination is missing, it should fail before provisioning", func(c *HyperShiftV2Config) { c.ArtifactDir = "" }, "artifact directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			config := newHyperShiftV2Fixture(t)
			g.Expect(os.WriteFile(config.Binary, []byte("#!/bin/sh\nexit 0\n"), 0700)).To(Succeed(), "create a local executable")
			tt.change(&config)
			g.Expect(config.Validate()).To(MatchError(ContainSubstring(tt.want)), "invalid prerequisites must fail clearly")
		})
	}
}

func TestResolveHyperShiftV2Target(t *testing.T) {
	t.Parallel()
	cluster := func(name, namespace, resourceID string) runtime.Object {
		return &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "hypershift.openshift.io/v1beta1", "kind": "HostedCluster",
			"metadata": map[string]interface{}{
				"name": name, "namespace": namespace,
				"annotations": map[string]interface{}{hypershiftv1beta1.ManagedAzureResourceIDAnnotation: resourceID},
			},
		}}
	}
	tests := []struct {
		name    string
		objects []runtime.Object
		want    types.NamespacedName
		wantErr string
	}{
		{
			name: "When an exact resource annotation matches, it should use the actual HC name and namespace",
			objects: []runtime.Object{cluster("internal-name", "actual-hc-namespace", strings.ToUpper(v2TestResourceID)),
				cluster("customer-cluster", "unrelated-namespace", v2TestResourceID+"-other")},
			want: types.NamespacedName{Name: "internal-name", Namespace: "actual-hc-namespace"},
		},
		{
			name:    "When no HC belongs to the resource, it should fail instead of choosing another cluster",
			objects: []runtime.Object{cluster("customer-cluster", "wrong-customer", strings.Replace(v2TestResourceID, "customer-sub", "other-sub", 1))},
			wantErr: "found 0",
		},
		{
			name:    "When multiple HCs match, it should reject the ambiguous target",
			objects: []runtime.Object{cluster("a", "ns-a", v2TestResourceID), cluster("b", "ns-b", v2TestResourceID)},
			wantErr: "found 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			gvr := schema.GroupVersionResource{Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedclusters"}
			client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "HostedClusterList"}, tt.objects...)
			target, err := resolveHyperShiftV2Target(t.Context(), client, v2TestResourceID)
			if tt.wantErr != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tt.wantErr)), "target discovery should explain why it cannot select a cluster")
				return
			}
			g.Expect(err).NotTo(HaveOccurred(), "target discovery should succeed")
			g.Expect(target).To(BeComparableTo(tt.want), "the adapter should use the HC namespace rather than the control-plane namespace")
		})
	}
}

func TestValidateHyperShiftV2JUnit(t *testing.T) {
	t.Parallel()
	passing := "<testcase name=\"[Feature:Health] Hosted Cluster Health " + hyperShiftV2ConditionsName + "\"/>"
	tests := []struct {
		name   string
		report string
		fail   bool
	}{
		{"When the intended check passes, it should accept its native JUnit", "<testsuites><testsuite>" + passing + "</testsuite></testsuites>", false},
		{"When selection is empty, it should reject a false success", "<testsuites><testsuite/></testsuites>", true},
		{"When the intended check is skipped, it should reject a false success", "<testsuite><testcase name=\"" + hyperShiftV2ConditionsName + "\"><skipped/></testcase></testsuite>", true},
		{"When an informing failure exits successfully, it should still reject the failure", "<testsuite><testcase name=\"" + hyperShiftV2ConditionsName + "\"><failure>assertion failed</failure></testcase></testsuite>", true},
		{"When the testcase reports an error, it should fail", "<testsuite><testcase name=\"" + hyperShiftV2ConditionsName + "\"><error/></testcase></testsuite>", true},
		{"When a different check ran, it should reject the wrong selection", "<testsuite><testcase name=\"another check\"/></testsuite>", true},
		{"When the check ran twice, it should reject the unexpected selection", "<testsuite>" + passing + passing + "</testsuite>", true},
		{"When the report is malformed, it should fail", "<testsuite><testcase", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			path := filepath.Join(t.TempDir(), "junit.xml")
			g.Expect(os.WriteFile(path, []byte(tt.report), 0600)).To(Succeed(), "write the native JUnit fixture")
			err := validateHyperShiftV2JUnit(path)
			if tt.fail {
				g.Expect(err).To(HaveOccurred(), "an incomplete or failing check must not pass")
			} else {
				g.Expect(err).NotTo(HaveOccurred(), "the intended passing check should be accepted")
			}
		})
	}
}

func TestRunHyperShiftV2Conditions(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		wantErr string
		timeout time.Duration
	}{
		{"When the child verifies the requested target, it should preserve the result", "exit 0", "", 0},
		{"When the child fails despite a passing report, it should propagate the exit status", "exit 9", "exit status 9", 0},
		{"When the child succeeds without a report, it should fail", "rm \"$report\"\nexit 0", "JUnit report", 0},
		{"When cancellation interrupts the child, it should return promptly", "exec sleep 60", "context deadline exceeded", 500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			config := newHyperShiftV2Fixture(t)
			// Ambient settings from another invocation must not redirect the child.
			t.Setenv("KUBECONFIG", "/wrong-service-kubeconfig")
			t.Setenv("KUBECONFIG_SOURCE", "/wrong-source-kubeconfig")
			t.Setenv("E2E_HOSTED_CLUSTER_NAME", "wrong-cluster")
			t.Setenv("E2E_SHOW_ENV_HELP", "1")
			script := "#!/bin/sh\nset -eu\n" +
				"[ \"$E2E_HOSTED_CLUSTER_NAME\" = internal-name ]\n" +
				"[ \"$E2E_HOSTED_CLUSTER_NAMESPACE\" = actual-hc-namespace ]\n" +
				"[ -z \"$KUBECONFIG_SOURCE\" ]\n[ -z \"$E2E_SHOW_ENV_HELP\" ]\n" +
				"[ \"$KUBECONFIG\" = " + config.ManagementKubeconfig + " ]\n" +
				"report=\nfor arg in \"$@\"; do\ncase \"$arg\" in --e2e.junit-report=*) report=${arg#*=};; esac\ndone\n" +
				"printf '%s' '<testsuites><testsuite><testcase name=\"" + hyperShiftV2ConditionsName + "\"/></testsuite></testsuites>' > \"$report\"\n" + tt.script + "\n"
			g.Expect(os.WriteFile(config.Binary, []byte(script), 0700)).To(Succeed(), "create a local child executable")
			ctx := t.Context()
			if tt.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.timeout)
				defer cancel()
			}
			start := time.Now()
			err := RunHyperShiftV2Conditions(ctx, config, v2TestResourceID, io.Discard)
			if tt.wantErr != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tt.wantErr)), "child failure should remain visible to the scenario")
			} else {
				g.Expect(err).NotTo(HaveOccurred(), "the child should verify the requested cluster")
				reports, globErr := filepath.Glob(filepath.Join(config.ArtifactDir, "conditions-*", "junit_hypershift_v2.xml"))
				g.Expect(globErr).NotTo(HaveOccurred(), "find the preserved JUnit result")
				g.Expect(reports).To(HaveLen(1), "preserve one native JUnit report")
				// A second invocation cannot pass by reading this first result.
				g.Expect(os.WriteFile(config.Binary, []byte("#!/bin/sh\nexit 0\n"), 0700)).To(Succeed(), "replace the executable with one that produces no report")
				g.Expect(RunHyperShiftV2Conditions(ctx, config, v2TestResourceID, io.Discard)).To(MatchError(ContainSubstring("JUnit report")), "a stale report must not satisfy a subsequent invocation")
			}
			if tt.timeout > 0 {
				g.Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(), "preserve the cancellation cause")
				g.Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second), "cancellation must stop the child without waiting for its sleep")
			}
		})
	}
}

func newHyperShiftV2Fixture(t *testing.T) HyperShiftV2Config {
	t.Helper()
	g := NewWithT(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/hypershift.openshift.io/v1beta1/hostedclusters" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(map[string]interface{}{
			"apiVersion": "hypershift.openshift.io/v1beta1", "kind": "HostedClusterList",
			"metadata": metav1.ListMeta{},
			"items": []interface{}{map[string]interface{}{
				"apiVersion": "hypershift.openshift.io/v1beta1", "kind": "HostedCluster",
				"metadata": map[string]interface{}{
					"name": "internal-name", "namespace": "actual-hc-namespace",
					"annotations": map[string]string{hypershiftv1beta1.ManagedAzureResourceIDAnnotation: v2TestResourceID},
				},
			}},
		})
		if err != nil {
			t.Errorf("encode management API response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	kubeconfig := clientcmdapi.Config{
		Clusters:       map[string]*clientcmdapi.Cluster{"management": {Server: server.URL}},
		AuthInfos:      map[string]*clientcmdapi.AuthInfo{"management": {}},
		Contexts:       map[string]*clientcmdapi.Context{"management": {Cluster: "management", AuthInfo: "management"}},
		CurrentContext: "management",
	}
	path := filepath.Join(dir, "management.kubeconfig")
	g.Expect(clientcmd.WriteToFile(kubeconfig, path)).To(Succeed(), "create local management access")
	return HyperShiftV2Config{Binary: filepath.Join(dir, "test-e2e-v2"), ManagementKubeconfig: path, ArtifactDir: filepath.Join(dir, "artifacts")}
}
