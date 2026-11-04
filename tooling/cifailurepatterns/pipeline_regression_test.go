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

func TestExtractEvidencePrefersDeserializationWhenCommandErrorIsBareExitStatus(t *testing.T) {
	t.Parallel()

	raw := `goroutine 1383 gp=0xc00161cfc0 m=nil [sync.WaitGroup.Wait, 3 minutes]:
runtime.gopark(0xc001729af0?, 0x2a657d4?, 0x20?, 0xb9?, 0x7ffb3a4a5d06?)
Command Error: exit status 2
Deserializaion Error: no output from command
crypto/tls.(*Conn).readFromUntil(0xc000806e08, {0x81cbfc0, 0xc000d38128}, 0xc0003829d0?)`

	evidence := Extract(Input{Text: raw})[0]
	if evidence.Canonical != "Deserializaion Error: no output from command" {
		t.Fatalf("expected canonical phrase to use deserialization no-output line, got=%q", evidence.Canonical)
	}
}

func TestExtractEvidenceKeepsDeserializationNoOutputWithoutCommandError(t *testing.T) {
	t.Parallel()

	raw := `Deserializaion Error: no output from command
goroutine 1 [running]:
runtime.throw({0x1, 0x2})`

	evidence := Extract(Input{Text: raw})[0]
	if evidence.Canonical != "Deserializaion Error: no output from command" {
		t.Fatalf("expected canonical phrase to remain deserialization no-output fallback, got=%q", evidence.Canonical)
	}
}

func TestExtractDeserializationPrefilterPreservesCaseAndSpellingVariants(t *testing.T) {
	t.Parallel()
	for _, spelling := range []string{"Deserialization", "Deserializaion"} {
		message := strings.ToUpper(spelling + " Error: no output from command")
		raw := "Command Error: exit status 2\n" + message
		got := Extract(Input{Text: raw})[0]
		if got.Canonical != message {
			t.Fatalf("deserialization detail lost behind exit status: %+v", got)
		}
	}
}

func TestExtractEvidenceProwEntrypointTimestampsMerge(t *testing.T) {
	t.Parallel()

	rawA := `{"component":"entrypoint","file":"sigs.k8s.io/prow/pkg/entrypoint/run.go:169","func":"sigs.k8s.io/prow/pkg/entrypoint.Options.ExecuteProcess","level":"error","msg":"Process did not finish before 2h0m0s timeout","severity":"error","time":"2026-04-12T09:52:11Z"}`
	rawB := `{"component":"entrypoint","file":"sigs.k8s.io/prow/pkg/entrypoint/run.go:169","func":"sigs.k8s.io/prow/pkg/entrypoint.Options.ExecuteProcess","level":"error","msg":"Process did not finish before 2h0m0s timeout","severity":"error","time":"2026-04-16T22:04:25Z"}`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("prow entrypoint lines with different timestamps must canonicalize identically:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if !strings.Contains(gotA, "Process did not finish before 2h0m0s timeout") {
		t.Fatalf("expected prow entrypoint msg in canonical phrase, got=%q", gotA)
	}
}

func TestExtractEvidenceLogfmtTimestampStripped(t *testing.T) {
	t.Parallel()

	rawA := `time=2026-04-17T11:04:19.211Z level=ERROR msg="Step errored." serviceGroup=Microsoft.Azure.ARO.HCP.Management.Infra resourceGroup=management step=delete-non-swift-user-nodepools err="failed to prepare kubeconfig: failed to ensure cluster admin role: /me request is only valid with delegated authentication flow."`
	rawB := `time=2026-04-18T09:00:00.000Z level=ERROR msg="Step errored." serviceGroup=Microsoft.Azure.ARO.HCP.Management.Infra resourceGroup=management step=delete-non-swift-user-nodepools err="failed to prepare kubeconfig: failed to ensure cluster admin role: /me request is only valid with delegated authentication flow."`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("logfmt lines with different timestamps must canonicalize identically:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if strings.Contains(gotA, "2026-04-17") || strings.Contains(gotA, "2026-04-18") {
		t.Fatalf("canonical phrase must not contain raw timestamps, got=%q", gotA)
	}
}

func TestExtractEvidenceProwEntrypointExtractsMsgField(t *testing.T) {
	t.Parallel()

	raw := `{"component":"entrypoint","file":"sigs.k8s.io/prow/pkg/entrypoint/run.go:169","func":"sigs.k8s.io/prow/pkg/entrypoint.Options.ExecuteProcess","level":"error","msg":"Process did not finish before 2h0m0s timeout","severity":"error","time":"2026-04-18T01:21:20Z"}`

	got := Extract(Input{Text: raw})[0].Canonical
	if got != "Process did not finish before 2h0m0s timeout" {
		t.Fatalf("expected clean msg value as canonical phrase, got=%q", got)
	}
}

func TestExtractEvidenceAlignsReleaseStatusInfoAndStepErrorDetails(t *testing.T) {
	t.Parallel()

	rawA := `time=2026-04-21T09:58:27.890Z level=INFO msg="Determined release status." release="mce-config" namespace="open-cluster-management" status=failed description="Release \"mce-config\" failed: resource not ready, name: grc-policy-propagator, kind: Deployment, status: InProgress\ncontext deadline exceeded"`
	rawB := `time=2026-04-21T09:58:28.016Z level=ERROR msg="Step errored." serviceGroup=Microsoft.Azure.ARO.HCP.ACM step=deploy-mce-config err="error running Helm release deployment Step, failed to deploy helm release: resource not ready, name: grc-policy-propagator, kind: Deployment, status: InProgress\ncontext deadline exceeded"`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("release-status info lines and step-error lines should canonicalize identically:\n  A=%q\n  B=%q", gotA, gotB)
	}
	lowered := strings.ToLower(gotA)
	if strings.Contains(lowered, "determined release status") || strings.Contains(lowered, "error running helm release deployment step") {
		t.Fatalf("expected release wrapper text to be stripped from canonical phrase, got=%q", gotA)
	}
	if !strings.Contains(lowered, "resource not ready") || !strings.Contains(lowered, "grc-policy-propagator") {
		t.Fatalf("expected canonical phrase to retain the actionable release detail, got=%q", gotA)
	}
}

func TestExtractEvidenceSplitsCandidateGraphTransportVariants(t *testing.T) {
	t.Parallel()

	rawDNS := `fail [github.com/Azure/ARO-HCP/test/util/framework/deployment_params.go:127]: failed to get latest install version for candidate channel: query candidate graph for candidate-4.20: Get "https://api.openshift.com/api/upgrades_info/v1/graph?channel=candidate-4.20": dial tcp: lookup api.openshift.com on 172.30.0.10:53: no such host`
	rawTimeout := `fail [github.com/Azure/ARO-HCP/test/util/framework/deployment_params.go:127]: failed to get latest install version for candidate channel: query candidate graph for candidate-4.20: Get "https://api.openshift.com/api/upgrades_info/v1/graph?channel=candidate-4.20": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`
	raw503 := `fail [github.com/Azure/ARO-HCP/test/util/framework/deployment_params.go:100]: failed to get latest install version for candidate channel: query candidate graph for candidate-4.20 returned 503 Service Unavailable: upstream connect error or disconnect/reset before headers. reset reason: overflow`

	gotDNS := Extract(Input{Text: rawDNS})[0].Canonical
	gotTimeout := Extract(Input{Text: rawTimeout})[0].Canonical
	got503 := Extract(Input{Text: raw503})[0].Canonical

	if gotDNS == gotTimeout || gotDNS == got503 || gotTimeout == got503 {
		t.Fatalf("expected candidate-graph transport variants to stay distinct:\n  dns=%q\n  timeout=%q\n  503=%q", gotDNS, gotTimeout, got503)
	}
	if !strings.Contains(strings.ToLower(gotDNS), "no such host") {
		t.Fatalf("expected DNS lookup failure detail in canonical phrase, got=%q", gotDNS)
	}
	if !strings.Contains(strings.ToLower(gotTimeout), "awaiting headers") {
		t.Fatalf("expected client-timeout detail in canonical phrase, got=%q", gotTimeout)
	}
	if !strings.Contains(strings.ToLower(got503), "503 service unavailable") {
		t.Fatalf("expected 503 detail in canonical phrase, got=%q", got503)
	}
	if strings.Contains(gotDNS, "candidate-4.20") || strings.Contains(gotTimeout, "candidate-4.20") || strings.Contains(got503, "candidate-4.20") {
		t.Fatalf("expected candidate-channel version to be normalized away:\n  dns=%q\n  timeout=%q\n  503=%q", gotDNS, gotTimeout, got503)
	}
}

func TestExtractEvidenceMergesStepErrorProgressPollingVariants(t *testing.T) {
	t.Parallel()

	rawA := `time=2026-07-27T12:24:07.823Z level=INFO msg="Running step." serviceGroup=Microsoft.Azure.ARO.HCP.Management.Infra resourceGroup=management step=swift-vnet stamp=1 description="Step swift-vnet\n  Kind: Shell\n  Command: ./swift-vnet.sh\n"
time=2026-07-27T12:33:11.965Z level=ERROR msg="Step errored." serviceGroup=Microsoft.Azure.ARO.HCP.Management.Infra resourceGroup=management step=swift-vnet stamp=1 err="stamp 1: error running Shell Step, failed to execute shell command: Launching container group swift-vnet-aks-net as globalMSI...\nWaiting for swift-vnet-aks-net to finish (state: pending, elapsed 2s)...\nWaiting for swift-vnet-aks-net to finish (state: Running, elapsed 240s)...\n=== swift-vnet-aks-net logs ===\n[swift-vnet] dns readiness (login.microsoftonline.com): attempt 1 failed (elapsed 5s/480s; likely container DNS cold-start or RBAC propagation); retrying in 5s...\n[swift-vnet] dns readiness (login.microsoftonline.com): attempt 48 failed (elapsed 477s/480s; likely container DNS cold-start or RBAC propagation); retrying in 5s...\n[swift-vnet] dns readiness (login.microsoftonline.com): giving up after 49 attempt(s) / 487s (limit 480s)\n\nCleaning up swift-vnet-aks-net...\n✗ swift-vnet-aks-net exited with code 1\n exit status 1"`
	rawB := `time=2026-07-21T21:47:26.084Z level=INFO msg="Running step." serviceGroup=Microsoft.Azure.ARO.HCP.Management.Infra resourceGroup=management step=swift-vnet stamp=1 description="Step swift-vnet\n  Kind: Shell\n  Command: ./swift-vnet.sh\n"
time=2026-07-21T21:56:33.375Z level=ERROR msg="Step errored." serviceGroup=Microsoft.Azure.ARO.HCP.Management.Infra resourceGroup=management step=swift-vnet stamp=1 err="stamp 1: error running Shell Step, failed to execute shell command: Launching container group swift-vnet-aks-net as globalMSI...\nWaiting for swift-vnet-aks-net to finish (state: pending, elapsed 1s)...\nWaiting for swift-vnet-aks-net to finish (state: Running, elapsed 190s)...\n=== swift-vnet-aks-net logs ===\n[swift-vnet] dns readiness (login.microsoftonline.com): attempt 1 failed (elapsed 5s/480s; likely container DNS cold-start or RBAC propagation); retrying in 5s...\n[swift-vnet] dns readiness (login.microsoftonline.com): attempt 46 failed (elapsed 457s/480s; likely container DNS cold-start or RBAC propagation); retrying in 5s...\n[swift-vnet] dns readiness (login.microsoftonline.com): giving up after 47 attempt(s) / 465s (limit 480s)\n\nCleaning up swift-vnet-aks-net...\n✗ swift-vnet-aks-net exited with code 1\n exit status 1"`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical

	if gotA != gotB {
		t.Fatalf("expected identical DNS-readiness retry-loop failures to normalize to the same canonical phrase despite differing elapsed-time/attempt counters:\n  A=%q\n  B=%q", gotA, gotB)
	}
	lowered := strings.ToLower(gotA)
	if strings.Contains(lowered, "elapsed") || strings.Contains(lowered, "pending") {
		t.Fatalf("expected repetitive progress-polling detail to be excluded from canonical phrase, got=%q", gotA)
	}
	if !strings.Contains(lowered, "giving up after") {
		t.Fatalf("expected canonical phrase to use the terminal giving-up summary, got=%q", gotA)
	}
}

func TestExtractEvidenceUsesLastLogfmtErrorField(t *testing.T) {
	t.Parallel()

	raw := `time=2026-08-04T21:26:06.127Z level=ERROR msg="Failed to roll out the Helm release." err="resource Deployment/clusters-service/clusters-service not ready. status: InProgress, message: Available: 2/3\ncontext deadline exceeded"
time=2026-08-04T21:26:06.127Z level=ERROR msg="Step errored." stamp=1 err="stamp 1: error running Helm release deployment Step, failed to deploy helm release: failed to roll out Helm release: failed post-install: resource Job/multicluster-engine/finalize-mce-config not ready. status: InProgress, message: Job in progress\ncontext deadline exceeded"`

	got := Extract(Input{Text: raw})[0].Canonical
	if !strings.Contains(got, "resource Job/multicluster-engine/finalize-mce-config not ready") ||
		!strings.HasSuffix(got, "context deadline exceeded") {
		t.Fatalf("expected the final step error without logfmt boilerplate or truncation, got=%q", got)
	}
	if strings.Contains(got, "level=ERROR") || strings.Contains(got, "stamp 1") {
		t.Fatalf("expected logfmt wrapper metadata to be removed, got=%q", got)
	}
}

func TestExtractEvidenceSummarizesRepeatedConfigMapPatchTimeouts(t *testing.T) {
	t.Parallel()

	raw := `time=2026-07-23T14:45:42.101Z level=ERROR msg="Step errored." stamp=1 err="stamp 1: error running Helm release deployment Step, failed to deploy helm release: failed to roll out Helm release: the server was unable to return a response in the time allotted, but may still be processing the request (patch configmaps arohcp-monitor-kube-state-metrics-customresourcestate-config)\nthe server was unable to return a response in the time allotted, but may still be processing the request (patch configmaps ama-metrics-settings-configmap)"`

	got := Extract(Input{Text: raw})[0].Canonical
	want := "server timed out while patching configmaps: ama-metrics-settings-configmap, arohcp-monitor-kube-state-metrics-customresourcestate-config"
	if got != want {
		t.Fatalf("expected complete stable configmap timeout summary, got=%q want=%q", got, want)
	}
}
