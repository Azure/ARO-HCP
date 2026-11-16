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

func TestExtractEvidencePrefersPreStructDeadlineLine(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/gpu_nodepools_create_delete.go:96]: Unexpected error:
    <*fmt.wrapError | 0xc0004ac420>: 
    failed waiting for deployment "aro-hcp-demo" in resourcegroup="gpu-nodepools-NC4asT4v3-z4g56q" to finish: context deadline exceeded
    {
        msg: "failed waiting for deployment \"aro-hcp-demo\" in resourcegroup=\"gpu-nodepools-NC4asT4v3-z4g56q\" to finish: context deadline exceeded",
        err: <context.deadlineExceededError>{},
    }`

	got := Extract(Input{Text: raw})[0].Canonical
	lowered := strings.ToLower(got)
	if strings.Contains(lowered, "<context.deadlineexceedederror>{},") {
		t.Fatalf("expected context type stub to be excluded from canonical phrase, got=%q", got)
	}
	if !strings.Contains(lowered, "failed waiting for deployment") {
		t.Fatalf("expected deployment timeout line in canonical phrase, got=%q", got)
	}
}

func TestExtractEvidenceEventuallyWrapperPrefersLineBeforeExpected(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/cluster_authorized_cidrs_connectivity.go:390]: Timed out after 600.002s.
The function passed to Eventually failed at /opt/app-root/src/github.com/Azure/ARO-HCP/test/e2e/cluster_authorized_cidrs_connectivity.go:389 with:
All ClusterOperators should report Available=True, but these are not available: [image-registry (Available=False)]
Expected
    <[]string | len:1, cap:1>: [
        "image-registry (Available=False)",
    ]
to be empty`

	evidence := Extract(Input{Text: raw})[0]
	lowered := strings.ToLower(evidence.Canonical)
	if !strings.Contains(lowered, "all clusteroperators should report available=true, but these are not available") {
		t.Fatalf("expected canonical phrase to use context line before Expected, got=%q", evidence.Canonical)
	}
	if strings.Contains(lowered, "the function passed to eventually failed at") {
		t.Fatalf("expected Eventually wrapper line to be excluded from canonical phrase, got=%q", evidence.Canonical)
	}
}

func TestExtractEvidenceContextDeadlinePrefersInnerDetail(t *testing.T) {
	t.Parallel()

	raw := `Unexpected error:
    <*errors.joinError | 0xc001101680>: 
    context deadline exceeded
    cluster operators not available: image-registry (Available=False, Progressing=True, Degraded=True)
    {
        errs: [
            <context.deadlineExceededError>{},
            <*errors.errorString | 0xc0006ec9b0>{
                s: "cluster operators not available: image-registry (Available=False, Progressing=True, Degraded=True)",
            },
        ],
    }`

	evidence := Extract(Input{Text: raw})[0]
	lowered := strings.ToLower(evidence.Canonical)
	if !strings.Contains(lowered, "cluster operators not available: image-registry") {
		t.Fatalf("expected canonical phrase to include inner cluster-operator detail, got=%q", evidence.Canonical)
	}
	if lowered == "context deadline exceeded" {
		t.Fatalf("expected canonical phrase to be more specific than generic context deadline wrapper, got=%q", evidence.Canonical)
	}
}

func TestExtractEvidenceSkipsIsTimeoutStructFieldAndUsesRouteHostLine(t *testing.T) {
	t.Parallel()

	raw := `Err: "no such host",
Name: "agnhost-e2e-serving-app-sckjc.apps.aro.example.net",
Server: "172.30.0.10:53",
IsTimeout: false,
IsTemporary: false,
IsNotFound: true,
occurred
fail [github.com/Azure/ARO-HCP/test/e2e/complete_cluster_create.go:137]: Unexpected error:
    <*fmt.wrapError | 0xc000e143c0>: 
    route host was never found: Get "https://agnhost-e2e-serving-app-sckjc.apps.aro.example.net": dial tcp: lookup agnhost-e2e-serving-app-sckjc.apps.aro.example.net on 172.30.0.10:53: no such host`

	evidence := Extract(Input{Text: raw})[0]
	lowered := strings.ToLower(evidence.Canonical)
	if strings.Contains(lowered, "istimeout: false") {
		t.Fatalf("expected struct field dump line to be excluded from canonical phrase, got=%q", evidence.Canonical)
	}
	if !strings.Contains(lowered, "route host was never found") {
		t.Fatalf("expected canonical phrase to use route host failure line, got=%q", evidence.Canonical)
	}
}

func TestExtractEvidenceGomegaEllipsisNotExtracted(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/nodepool_update_nodes.go:262]: Expected success, but got an error:
    <*errors.errorString | 0xc000981550>: 
    expected 4 nodes, found 5
    ...
fail [github.com/Azure/ARO-HCP/test/e2e/nodepool_update_nodes.go:262]: Expected success, but got an error:
    <*errors.errorString | 0xc000981550>: 
    expected 4 nodes, found 5
    ...`

	got := Extract(Input{Text: raw})[0].Canonical
	if got == "..." {
		t.Fatalf("canonical phrase must not be the Gomega ellipsis marker")
	}
	if !strings.Contains(got, "expected 4 nodes, found 5") {
		t.Fatalf("expected the inner assertion error in canonical phrase, got=%q", got)
	}
}

func TestExtractEvidenceGomegaSuccessFailureExtractsInnerError(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/cluster_create_private_kv.go:180]: Timed out after 600.005s.
router-default deployment logs should be fetchable
Expected success, but got an error:
    <*errors.errorString | 0xc001178270>: 
    deployment router-default -n openshift-ingress has no running pods
    ...`

	got := Extract(Input{Text: raw})[0].Canonical
	if strings.EqualFold(got, "Expected success, but got an error:") {
		t.Fatalf("canonical phrase must not be the Gomega wrapper line, got=%q", got)
	}
	if !strings.Contains(strings.ToLower(got), "router-default") || !strings.Contains(strings.ToLower(got), "no running pods") {
		t.Fatalf("expected inner deployment error in canonical phrase, got=%q", got)
	}
}

func TestExtractEvidenceUsesModelDiffSummaryInsteadOfStructFieldLine(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/cluster_autoscaling.go:141]: Expected
    operation result model did not match expected model for type *armredhatopenshifthcp.HcpOpenShiftCluster:
      NodeDrainTimeoutMinutes: nil,
    to equal
      <string>: ...`

	evidence := Extract(Input{Text: raw})[0]
	lowered := strings.ToLower(evidence.Canonical)
	if !strings.Contains(lowered, "operation result model did not match expected model") {
		t.Fatalf("expected canonical phrase to use the model-diff summary, got=%q", evidence.Canonical)
	}
	if strings.Contains(lowered, "nodedraintimeoutminutes") {
		t.Fatalf("expected canonical phrase to skip interior struct-field lines, got=%q", evidence.Canonical)
	}
}

func TestExtractEvidenceUsesStableLabelForPlaceholderOnlyEqualityAssertion(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/mise_routing.go:89]: Expected
    <string>: ...
to equal
    <string>: ...`

	evidence := Extract(Input{Text: raw})[0]
	if got, want := evidence.Canonical, "assertion failed: expected values to equal"; got != want {
		t.Fatalf("unexpected canonical for placeholder-only equality assertion: got=%q want=%q", got, want)
	}
}

func TestExtractEvidenceNormalizesTimedOutAfterPrecision(t *testing.T) {
	t.Parallel()

	rawA := `fail [github.com/Azure/ARO-HCP/test/e2e/exporter_metrics.go:114]: Timed out after 1800.001s.
Expected
    <bool>: false
to be true`
	rawB := `fail [github.com/Azure/ARO-HCP/test/e2e/exporter_metrics.go:114]: Timed out after 1800.002s.
Expected
    <bool>: false
to be true`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	want := "Timed out after <duration>s."
	if gotA != want {
		t.Fatalf("unexpected canonical for first timeout variant: got=%q want=%q", gotA, want)
	}
	if gotB != want {
		t.Fatalf("unexpected canonical for second timeout variant: got=%q want=%q", gotB, want)
	}
}

func TestExtractEvidenceContextualizesGenericTimedOutCanonicalWithTestName(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/exporter_metrics.go:114]: Timed out after 1800.001s.
Expected
    <bool>: false
to be true`

	got := Extract(Input{Text: raw, TestName: "Engineering should be able to retrieve expected metrics from the /metrics endpoint"})[0].Canonical
	want := "Engineering should be able to retrieve expected metrics from the /metrics endpoint: Timed out after <duration>s."
	if got != want {
		t.Fatalf("unexpected contextualized timeout canonical: got=%q want=%q", got, want)
	}
}

func TestExtractEvidenceContextualizesGenericAssertionCanonicalWithTestName(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/mise_routing.go:89]: Expected
    <string>: ...
to equal
    <string>: ...`

	got := Extract(Input{Text: raw, TestName: "MISE routing returns the versioned frontend"})[0].Canonical
	want := "MISE routing returns the versioned frontend: assertion failed: expected values to equal"
	if got != want {
		t.Fatalf("unexpected contextualized assertion canonical: got=%q want=%q", got, want)
	}
}

func TestExtractEvidenceDistinguishesContextDeadlineFailuresByAssertionHeader(t *testing.T) {
	t.Parallel()

	rawAdminCred := `fail [github.com/Azure/ARO-HCP/test/e2e/admin_credential_lifecycle.go:191]: failed to poll admin credential 1 to completion
Unexpected error:
    <context.deadlineExceededError>: 
    context deadline exceeded
occurred`
	rawFirstCluster := `fail [github.com/Azure/ARO-HCP/test/e2e/clusters_sharing_resgroup.go:131]: failed to wait for first cluster "basic-hcp-cluster" to complete creation (timeout '20.000000' minutes)
Unexpected error:
    <context.deadlineExceededError>: 
    context deadline exceeded
occurred`
	rawSecondCluster := `fail [github.com/Azure/ARO-HCP/test/e2e/clusters_sharing_resgroup.go:138]: failed to wait for second cluster "basic-hcp-cluster2" to complete creation (timeout '20.000000' minutes)
Unexpected error:
    <context.deadlineExceededError>: 
    context deadline exceeded
occurred`

	gotAdminCred := Extract(Input{Text: rawAdminCred})[0].Canonical
	gotFirstCluster := Extract(Input{Text: rawFirstCluster})[0].Canonical
	gotSecondCluster := Extract(Input{Text: rawSecondCluster})[0].Canonical

	if gotAdminCred == gotFirstCluster || gotAdminCred == gotSecondCluster || gotFirstCluster == gotSecondCluster {
		t.Fatalf("expected distinct assertion failures to stay distinct instead of collapsing into the generic wrapper:\n  adminCred=%q\n  firstCluster=%q\n  secondCluster=%q", gotAdminCred, gotFirstCluster, gotSecondCluster)
	}
	for _, got := range []string{gotAdminCred, gotFirstCluster, gotSecondCluster} {
		if strings.EqualFold(strings.TrimSpace(got), "context deadline exceeded") {
			t.Fatalf("expected canonical phrase to be more specific than the generic context-deadline wrapper, got=%q", got)
		}
	}
	if !strings.Contains(strings.ToLower(gotAdminCred), "failed to poll admin credential") {
		t.Fatalf("expected admin-credential detail in canonical phrase, got=%q", gotAdminCred)
	}
	if !strings.Contains(strings.ToLower(gotFirstCluster), "first cluster") {
		t.Fatalf("expected first-cluster detail in canonical phrase, got=%q", gotFirstCluster)
	}
	if !strings.Contains(strings.ToLower(gotSecondCluster), "second cluster") {
		t.Fatalf("expected second-cluster detail in canonical phrase, got=%q", gotSecondCluster)
	}
}

func TestExtractEvidenceUsesAssertionHeaderWhenExpectedErrorIsNil(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/cluster_create_private_ingress.go:176]: private ingress should not be reachable from outside the VNet, but connection succeeded
Expected an error to have occurred.  Got:
    <nil>: ...`

	got := Extract(Input{Text: raw})[0].Canonical
	want := "private ingress should not be reachable from outside the VNet, but connection succeeded"
	if got != want {
		t.Fatalf("expected assertion header detail instead of the generic Gomega message, got=%q want=%q", got, want)
	}
}
