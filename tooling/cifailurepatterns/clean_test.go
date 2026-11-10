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

package cifailurepatterns

import (
	"strings"
	"testing"
)

func TestCleanCanonicalScrubsResourceGroupAndClusterIDs(t *testing.T) {
	t.Parallel()

	input := "timeout '10.000000' minutes exceeded during GetAdminRESTConfigForHCPCluster for cluster ea-cluster in resource group external-auth-cluster-w6qnck, error: context deadline exceeded"
	got := cleanCanonical(input)

	if strings.Contains(got, "external-auth-cluster-w6qnck") {
		t.Fatalf("expected resource-group ID to be scrubbed, got=%q", got)
	}
	if strings.Contains(got, "for cluster ea-cluster") {
		t.Fatalf("expected cluster ID to be scrubbed, got=%q", got)
	}
	if !strings.Contains(got, "resource group <resource-group>") {
		t.Fatalf("expected resource-group placeholder in canonical phrase, got=%q", got)
	}
	if !strings.Contains(got, "for cluster <cluster>") {
		t.Fatalf("expected cluster placeholder in canonical phrase, got=%q", got)
	}
}

func TestCleanCanonicalScrubsNodePoolIDs(t *testing.T) {
	t.Parallel()

	input := "timeout '45.000000' minutes exceeded during CreateNodePoolFromParam for node pool ea-np-1 in resource group external-auth-cluster-h9gmhm, error: failed waiting for nodepool=\"ea-np-1\" for cluster \"ea-cluster\" in resourcegroup=\"external-auth-cluster-h9gmhm\" to finish creating"
	got := cleanCanonical(input)

	if strings.Contains(got, "ea-np-1") {
		t.Fatalf("expected node-pool ID to be scrubbed, got=%q", got)
	}
	if strings.Contains(got, "external-auth-cluster-h9gmhm") {
		t.Fatalf("expected resource-group ID to be scrubbed, got=%q", got)
	}
	if !strings.Contains(got, "node pool <nodepool>") {
		t.Fatalf("expected node-pool placeholder in canonical phrase, got=%q", got)
	}
	if !strings.Contains(got, `nodepool="<nodepool>"`) {
		t.Fatalf("expected quoted node-pool placeholder in canonical phrase, got=%q", got)
	}
}

func TestCleanCanonicalScrubsExternalAuthResourceGroupSample(t *testing.T) {
	t.Parallel()

	input := "timeout '10.000000' minutes exceeded during GetAdminRESTConfigForHCPCluster for cluster ea-cluster in resource group external-auth-cluster-shhqbw, error: context deadline exceeded\", errs: [ {"
	got := cleanCanonical(input)

	if strings.Contains(got, "external-auth-cluster-shhqbw") {
		t.Fatalf("expected external-auth resource-group ID to be scrubbed, got=%q", got)
	}
	if strings.Contains(got, "for cluster ea-cluster") {
		t.Fatalf("expected cluster ID to be scrubbed, got=%q", got)
	}
	if !strings.Contains(got, "resource group <resource-group>") {
		t.Fatalf("expected resource-group placeholder in canonical phrase, got=%q", got)
	}
}

func TestCleanCanonicalScrubsExternalAuthAndInClusterIDs(t *testing.T) {
	t.Parallel()

	input := `failed waiting for external auth "ea-list" in resourcegroup="ea-list-rg-pxk72q", caused by: timeout '15.000000' minutes exceeded during CreateOrUpdateExternalAuthAndWait for external auth ea-list in cluster ea-listxfk7fg`
	got := cleanCanonical(input)

	if strings.Contains(got, "ea-listxfk7fg") {
		t.Fatalf("expected dynamic cluster name to be scrubbed, got=%q", got)
	}
	if strings.Contains(got, `external auth "ea-list"`) || strings.Contains(got, "external auth ea-list") {
		t.Fatalf("expected external auth name to be scrubbed, got=%q", got)
	}
	if !strings.Contains(got, `external auth "<external-auth>"`) {
		t.Fatalf("expected quoted external-auth placeholder in canonical phrase, got=%q", got)
	}
	if !strings.Contains(got, "for external auth <external-auth>") {
		t.Fatalf("expected external-auth placeholder in canonical phrase, got=%q", got)
	}
	if !strings.Contains(got, "in cluster <cluster>") {
		t.Fatalf("expected in-cluster placeholder in canonical phrase, got=%q", got)
	}
}

func TestCleanCanonicalScrubsBareNodePoolName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "update-nodes",
			input: "timeout '45.000000' minutes exceeded during UpdateNodePoolAndWait for nodepool np-update-nodes",
			want:  "nodepool <nodepool>",
		},
		{
			name:  "hyphen-name",
			input: "UpdateNodePoolAndWait for nodepool np-1 timed out",
			want:  "nodepool <nodepool>",
		},
		{
			name:  "one-node",
			input: "timeout exceeded during UpdateNodePoolAndWait for nodepool np-one-node",
			want:  "nodepool <nodepool>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cleanCanonical(tc.input)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("expected %q in cleaned canonical, got=%q", tc.want, got)
			}
		})
	}
}

func TestCleanCanonicalScrubsDialingIPPort(t *testing.T) {
	t.Parallel()

	input := `proxyconnect tcp: dial tcp 127.0.0.1:8888: connect: connection refused; also dialing 10.128.64.38:15017 failed`
	got := cleanCanonical(input)

	if strings.Contains(got, "127.0.0.1") || strings.Contains(got, "10.128.64.38") {
		t.Fatalf("expected IP addresses to be scrubbed, got=%q", got)
	}
	if !strings.Contains(got, "dial tcp <ip>:<port>") {
		t.Fatalf("expected dial tcp placeholder, got=%q", got)
	}
	if !strings.Contains(got, "dialing <ip>:<port>") {
		t.Fatalf("expected dialing placeholder, got=%q", got)
	}
}

func TestCleanCanonicalStripsK8sLogPrefix(t *testing.T) {
	t.Parallel()

	input := `E0407 23:10:13.008148    2565 controller.go:123] "Unhandled Error" err="something went wrong" controller="cluster"`
	got := cleanCanonical(input)

	if strings.Contains(got, "E0407") || strings.Contains(got, "23:10:13") || strings.Contains(got, "2565") {
		t.Fatalf("expected klog prefix to be stripped, got=%q", got)
	}
	if !strings.Contains(strings.ToLower(got), "unhandled error") {
		t.Fatalf("expected log message content to remain, got=%q", got)
	}
}

func TestCleanCanonicalStripsMakeDirectoryBanner(t *testing.T) {
	t.Parallel()

	input := "make[2]: Entering directory '/go/src/github.com/openshift-kni/numaresources-operator'\nerror: something actually failed"
	got := cleanCanonical(input)

	if strings.Contains(strings.ToLower(got), "entering directory") {
		t.Fatalf("expected make directory banner to be stripped, got=%q", got)
	}
	if !strings.Contains(strings.ToLower(got), "something actually failed") {
		t.Fatalf("expected actual error to remain, got=%q", got)
	}
}

func TestCleanCanonicalScrubsK8sNodeName(t *testing.T) {
	t.Parallel()

	input := `node pool upgrade verification failed: s9w6l8k8x0w1p6q-npupgrade-4-20-v8hsh-fvjvs (version 4.20 not in same minor as expected 4.21.5)`
	got := cleanCanonical(input)
	if strings.Contains(got, "s9w6l8k8x0w1p6q") {
		t.Fatalf("expected K8s node name to be scrubbed, got=%q", got)
	}
	if !strings.Contains(got, "<node>") {
		t.Fatalf("expected <node> placeholder, got=%q", got)
	}
}

func TestCleanCanonicalScrubsOCPChannel(t *testing.T) {
	t.Parallel()

	input := "no graph nodes found for stable-5.2"
	got := cleanCanonical(input)
	if strings.Contains(got, "stable-5.2") {
		t.Fatalf("expected OCP channel to be scrubbed, got=%q", got)
	}
	if !strings.Contains(got, "<ocp-channel>") {
		t.Fatalf("expected <ocp-channel> placeholder, got=%q", got)
	}

	input2 := "no graph nodes found for candidate-4.20"
	got2 := cleanCanonical(input2)
	if strings.Contains(got2, "candidate-4.20") {
		t.Fatalf("expected OCP channel to be scrubbed, got=%q", got2)
	}
}

func TestCleanCanonicalScrubsClusterCreationQuotedName(t *testing.T) {
	t.Parallel()

	input := `failed to create HCP cluster np-autoscale-cluster: failed starting cluster creation "np-autoscale-cluster" in resourcegroup="stg-autoscale-rg-abc123": PUT https://management.example.test/subscriptions/11111111-2222-3333-4444-555555555555/resourceGroups/stg-autoscale-rg-abc123/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/np-autoscale-cluster`
	got := cleanCanonical(input)

	if strings.Contains(got, "np-autoscale-cluster") {
		t.Fatalf("expected cluster creation name to be scrubbed, got=%q", got)
	}
	if !strings.Contains(got, `failed to create HCP cluster <cluster>`) {
		t.Fatalf("expected HCP cluster placeholder in canonical phrase, got=%q", got)
	}
	if !strings.Contains(got, `cluster creation "<cluster>"`) {
		t.Fatalf("expected cluster creation placeholder in canonical phrase, got=%q", got)
	}
	if !strings.Contains(got, `resourcegroup="<resource-group>"`) {
		t.Fatalf("expected resource-group placeholder in canonical phrase, got=%q", got)
	}
	if !strings.Contains(got, "PUT <url>") {
		t.Fatalf("expected URL placeholder in canonical phrase, got=%q", got)
	}
}

func TestCleanCanonicalScrubsQuotedAzureResourcePathAndResourceGroup(t *testing.T) {
	t.Parallel()

	input := "ERROR CODE: DeploymentFailed; detail code ResourceNotFound; detail message The Resource 'Microsoft.ContainerService/managedClusters/prow-j7955840-mgmt-1' under resource group 'hcp-underlay-prow-j7955840-mgmt-1' was not found.; provider Microsoft.ContainerService"
	got := cleanCanonical(input)

	if strings.Contains(got, "prow-j7955840-mgmt-1") {
		t.Fatalf("expected managed-cluster name to be scrubbed, got=%q", got)
	}
	if strings.Contains(got, "hcp-underlay-prow-j7955840-mgmt-1") {
		t.Fatalf("expected resource-group name to be scrubbed, got=%q", got)
	}
	if !strings.Contains(got, "'Microsoft.ContainerService/managedClusters/<resource>'") {
		t.Fatalf("expected managed-cluster resource path placeholder, got=%q", got)
	}
	if !strings.Contains(got, "resource group '<resource-group>'") {
		t.Fatalf("expected quoted resource-group placeholder, got=%q", got)
	}
}

func TestCleanCanonicalScrubsGeneratedCosmosDatabaseAccount(t *testing.T) {
	t.Parallel()

	input := "collection [Fleet] not found under /dbs/arohcpci01-rp-j5016704/colls/Fleet"
	got := cleanCanonical(input)

	if strings.Contains(got, "arohcpci01-rp-j5016704") {
		t.Fatalf("expected generated Cosmos account to be scrubbed, got=%q", got)
	}
	if !strings.Contains(got, "/dbs/<cosmos-account>/colls/Fleet") {
		t.Fatalf("expected Cosmos account placeholder while preserving collection, got=%q", got)
	}
}

func TestCleanCanonicalScrubsHumanTimestamp(t *testing.T) {
	t.Parallel()

	input := "deployment svc-kv-xehipxbjxawki was started at '9/3/2026 7:06:26 AM' and expires at '9/10/2026 7:06:26 AM'"
	got := cleanCanonical(input)

	if strings.Contains(got, "9/3/2026") || strings.Contains(got, "9/10/2026") {
		t.Fatalf("expected human-readable timestamps to be scrubbed, got=%q", got)
	}
	if !strings.Contains(got, "deployment svc-kv-xehipxbjxawki") {
		t.Fatalf("expected deployment component identity to remain, got=%q", got)
	}
	if strings.Count(got, "<timestamp>") != 2 {
		t.Fatalf("expected both timestamps to be normalized, got=%q", got)
	}
}

func TestDeploymentActiveNormalizationPreservesComponentIdentity(t *testing.T) {
	t.Parallel()

	message := func(component, started, expires string) string {
		return "The deployment with resource id '/subscriptions/<subscription>/resourcegroups/<resource-group>/providers/Microsoft.Resources/deployments/" +
			component + "' cannot be saved, because this would overwrite an existing deployment which is still active. " +
			"The previous deployment was started at '" + started + "' with correlationId '<uuid>', and will expire at '" + expires + "' if it does not complete before then."
	}

	first := summarizeAzureDetailMessage(message("svc-kv-xehipxbjxawki", "9/3/2026 7:06:26 AM", "9/10/2026 7:06:26 AM"))
	second := summarizeAzureDetailMessage(message("svc-kv-xehipxbjxawki", "9/8/2026 6:48:37 AM", "9/15/2026 6:48:37 AM"))
	otherComponent := summarizeAzureDetailMessage(message("rp-cosmos-account", "9/8/2026 6:15:02 AM", "9/15/2026 6:15:02 AM"))

	if first != second {
		t.Fatalf("expected timestamps not to fragment the same component:\nfirst=%q\nsecond=%q", first, second)
	}
	if first == otherComponent {
		t.Fatalf("expected distinct deployment components to remain separate, got=%q", first)
	}
}

func TestSummarizeAzureDetailMessageScrubsOperationalValues(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "retry delay",
			input: "Number of 'read' requests exceeded. Please try again after '10' seconds after additional tokens are available.",
			want:  "Please try again after '<seconds>' seconds",
		},
		{
			name:  "quota counters",
			input: "Insufficient vcpu quota requested 8, remaining 2 for family standardDSv3Family for region canadacentral.",
			want:  "requested <count>, remaining <count>",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := summarizeAzureDetailMessage(tc.input)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("expected %q in summary, got=%q", tc.want, got)
			}
		})
	}
}

func TestCleanCanonicalPreservesNodePoolCreationAndConditionWords(t *testing.T) {
	t.Parallel()

	input := "node pool creation did not complete before the deadline; node pool condition AllNodesHealthy is False; hypershift NodePool status replicas is 0, want 2"
	got := cleanCanonical(input)
	if strings.Contains(got, "node pool <nodepool> did not complete") {
		t.Fatalf("did not expect 'creation' to be treated as a node-pool name, got=%q", got)
	}
	if strings.Contains(got, "node pool <nodepool> AllNodesHealthy") {
		t.Fatalf("did not expect 'condition' to be treated as a node-pool name, got=%q", got)
	}
	if strings.Contains(strings.ToLower(got), "nodepool <nodepool> status") || strings.Contains(strings.ToLower(got), "nodepool <nodepool> replicas") {
		t.Fatalf("did not expect 'NodePool status' to be treated as a node-pool name, got=%q", got)
	}
	if !strings.Contains(got, "node pool creation did not complete before the deadline") {
		t.Fatalf("expected node pool creation wording to remain, got=%q", got)
	}
}

func TestCleanCanonicalScrubsMachineObjectNames(t *testing.T) {
	t.Parallel()

	input := "Machine private-kas-np-1-5dbrz-khxf4: InspectionFailed: Waiting for AzureMachine to report spec.providerID"
	got := cleanCanonical(input)
	if strings.Contains(got, "private-kas-np-1-5dbrz-khxf4") {
		t.Fatalf("expected Machine object name to be scrubbed, got=%q", got)
	}
	if !strings.Contains(got, "Machine <node>:") {
		t.Fatalf("expected Machine placeholder, got=%q", got)
	}
}

func TestCleanCanonicalScrubsClusterIdentityNamespaceName(t *testing.T) {
	t.Parallel()

	input := `cluster delayed-rbac-87tl668hqnnr/delayed-rbac-cluster entered terminal state "Failed" after role assignment deployment; expected "Succeeded"`
	got := cleanCanonical(input)
	if strings.Contains(got, "delayed-rbac-87tl668hqnnr") {
		t.Fatalf("expected generated identity name to be scrubbed, got=%q", got)
	}
	if !strings.Contains(got, "cluster <resource-group>/<cluster>") {
		t.Fatalf("expected cluster namespace/name placeholder, got=%q", got)
	}
}

func TestStripReleaseFailureWrapperRemovesStampWithOrWithoutColon(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"stamp 1: scheduling must have requested resources",
		"stamp 2 scheduling must have requested resources",
	} {
		if got, want := stripReleaseFailureWrapper(input), "scheduling must have requested resources"; got != want {
			t.Fatalf("stripReleaseFailureWrapper(%q): got=%q want=%q", input, got, want)
		}
	}
}
