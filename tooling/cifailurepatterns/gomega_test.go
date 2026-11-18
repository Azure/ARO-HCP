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

func TestGomegaDottedDetailsPreserveDistinctCauses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		detail string
	}{
		{"hostname", "failed to call api.example.test"},
		{"version", "failed to validate version 4.18"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var keys [2]string
			for i, cause := range []string{"permission denied", "quota exceeded"} {
				detail := tc.detail + ": " + cause
				for _, wrapper := range []string{"", "    <*errors.errorString | 0x1234>:\n"} {
					raw := "Expected success, but got an error:\n" + wrapper + "    " + detail
					finding := Extract(Input{Text: raw})[0]
					if finding.Canonical != detail || finding.RuleID != "assertion.context" {
						t.Fatalf("lost dotted error detail: got %+v, want %q", finding, detail)
					}
					if wrapper != "" && finding.Key != keys[i] {
						t.Fatalf("type wrapper changed semantic identity for %q", detail)
					}
					keys[i] = finding.Key
				}
			}
			if keys[0] == keys[1] {
				t.Fatal("distinct causes were merged after a period in the detail line")
			}
		})
	}
}

func TestGomegaStandaloneEllipsisIsNotAssertionEvidence(t *testing.T) {
	t.Parallel()

	for _, wrapper := range []string{"", "    <*errors.errorString | 0x1234>:\n"} {
		raw := "Expected success, but got an error:\n" + wrapper + "    ...\n"
		if detail := extractGomegaSuccessFailureContext(raw); detail != "" {
			t.Fatalf("ellipsis was selected as assertion evidence: %q", detail)
		}
		if finding := Extract(Input{Text: raw})[0]; finding.RuleID == "assertion.context" || finding.Canonical == "..." {
			t.Fatalf("ellipsis produced an assertion-context finding: %+v", finding)
		}
	}
}

func TestExtractGomegaSuccessFailureSkipsLabelLine(t *testing.T) {
	t.Parallel()

	raw := `Expected success, but got an error:
    IDMS verification failed:
        No trust bundle available for cluster`

	got := extractGomegaSuccessFailureContext(raw)
	if strings.HasSuffix(strings.TrimSpace(got), ":") {
		t.Fatalf("extractGomegaSuccessFailureContext should skip label lines ending with ':', got=%q", got)
	}
	if !strings.Contains(strings.ToLower(got), "no trust bundle") {
		t.Fatalf("expected the actual error detail line, got=%q", got)
	}
}

func TestGomegaDeadlineRetainsUnavailableOperator(t *testing.T) {
	t.Parallel()
	var previousKey string
	for _, operator := range []string{"image-registry", "ingress"} {
		detail := "cluster operators not available: " + operator + " (Available=False, Progressing=True, Degraded=True)"
		plain := Extract(Input{Text: "context deadline exceeded\n" + detail})[0]
		for _, annotation := range []string{"", "    <*errors.joinError | 0x1234>:\n"} {
			raw := "Expected success, but got an error:\n" + annotation + "    context deadline exceeded\n    " + detail + "\n    {\n        errs: [],\n    }"
			got := Extract(Input{Text: raw})[0]
			if !strings.Contains(got.Canonical, detail) || got.Key != plain.Key {
				t.Fatalf("assertion wrapper lost the actual operator: %+v / %+v", got, plain)
			}
		}
		if previousKey == plain.Key {
			t.Fatal("different unavailable operators merged")
		}
		previousKey = plain.Key
	}
	const context = "failed waiting for the requested operation to stabilize"
	withDetail := Extract(Input{Text: "Expected success, but got an error:\n    " + context + "\n    context deadline exceeded\n    cluster operators not available: ingress"})[0]
	withoutDetail := Extract(Input{Text: "Expected success, but got an error:\n    " + context})[0]
	if withDetail.Key != withoutDetail.Key {
		t.Fatalf("operator detail displaced meaningful assertion context: %+v / %+v", withDetail, withoutDetail)
	}
}

func TestExtractEvidenceGomegaOccurredExtracts(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/complete_cluster_create_multiversion.go:75]: Unexpected error:
    <*errors.errorString | 0xc001051550>: 
    no graph nodes found for stable-5.2
    {
        s: "no graph nodes found for stable-5.2",
    }
occurred`

	got := Extract(Input{Text: raw})[0].Canonical
	if strings.EqualFold(strings.TrimSpace(got), "occurred") {
		t.Fatalf("canonical should not be the Gomega boilerplate 'occurred', got=%q", got)
	}
	if !strings.Contains(strings.ToLower(got), "no graph nodes found") {
		t.Fatalf("canonical should contain inner error 'no graph nodes found', got=%q", got)
	}
}

func TestExtractEvidenceGomegaCauseStructExtracts(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/control_plane_automated_z_stream_upgrade.go:137]: Unexpected error:
    <wait.errInterrupted>: 
    timed out waiting for the condition
    {
        cause: <*errors.errorString | 0xc0009c2890>{
            s: "timed out waiting for the condition",
        },
    }
occurred`

	got := Extract(Input{Text: raw})[0].Canonical
	if strings.EqualFold(strings.TrimSpace(got), "cause: {") {
		t.Fatalf("canonical should not be 'cause: {', got=%q", got)
	}
	if strings.EqualFold(strings.TrimSpace(got), "occurred") {
		t.Fatalf("canonical should not be 'occurred', got=%q", got)
	}
	if !strings.Contains(strings.ToLower(got), "timed out waiting") {
		t.Fatalf("canonical should contain 'timed out waiting', got=%q", got)
	}
}

func TestExtractEvidenceGomegaJoinErrorOccurred(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/util/framework/per_test_framework.go:246]: Unexpected error:
    <*errors.joinError | 0xc000e4a0f0>: 
    found 1 managed resource groups left behind HCP clusters in cluster-listing-xk5f98
    {
        errs: [
            <*errors.errorString | 0xc00198c880>{
                s: "found 1 managed resource groups left behind HCP clusters in cluster-listing-xk5f98",
            },
        ],
    }
occurred`

	got := Extract(Input{Text: raw})[0].Canonical
	if strings.EqualFold(strings.TrimSpace(got), "occurred") {
		t.Fatalf("canonical should not be 'occurred', got=%q", got)
	}
	if !strings.Contains(strings.ToLower(got), "managed resource groups left behind") {
		t.Fatalf("canonical should contain 'managed resource groups left behind', got=%q", got)
	}
}

func TestBestGomegaInnerError(t *testing.T) {
	t.Parallel()

	text := `    <*errors.errorString | 0xc001051550>: 
    no graph nodes found for stable-5.2
    {
        s: "no graph nodes found for stable-5.2",
    }`

	got := bestGomegaInnerError(text)
	if !strings.Contains(got, "no graph nodes found") {
		t.Fatalf("expected inner error, got=%q", got)
	}
}

func TestExtractEvidenceLogfmtStepErrorExtracts(t *testing.T) {
	t.Parallel()

	raw := `time=2026-04-17T11:04:14.653Z level=INFO msg="Running step." serviceGroup=Microsoft.Azure.ARO.HCP.Management.Infra resourceGroup=management step=delete-non-swift-user-nodepools
time=2026-04-17T11:04:19.211Z level=ERROR msg="Step errored." serviceGroup=Microsoft.Azure.ARO.HCP.Management.Infra resourceGroup=management step=delete-non-swift-user-nodepools err="failed to prepare kubeconfig: failed to ensure cluster admin role: /me request is only valid with delegated authentication flow."`

	got := Extract(Input{Text: raw})[0].Canonical
	if strings.Contains(strings.ToLower(got), "step errored") {
		t.Fatalf("canonical phrase should not contain logfmt boilerplate 'Step errored.', got=%q", got)
	}
	if !strings.Contains(strings.ToLower(got), "failed to prepare kubeconfig") {
		t.Fatalf("canonical phrase should contain the actionable err= value, got=%q", got)
	}
}

func TestExtractEvidenceLogfmtImageMirrorPrefersInnerTransferFailure(t *testing.T) {
	t.Parallel()

	raw := `time=2026-04-20T03:12:18.644Z level=ERROR msg="Step errored." serviceGroup=Microsoft.Azure.ARO.HCP.RP.Frontend resourceGroup=global step=mirror-image err="error running Image Mirror Step, failed to execute shell command: Checking USE_OC_LOGIN_REGISTRIES: registry.build05.ci.openshift.org registry.build05.ci.openshift.org registry.build05.ci.openshift.org registry.build05.ci.openshift.org registry.build05.ci.openshift.org
Setting up registry authentication for CI source registry.
info: Using registry public hostname registry.build05.ci.openshift.org
Saved credentials for registry.build05.ci.openshift.org into /tmp/tmp.w3CTaww54H/containers/auth.json
Logging into target ACR example-registry.
Login Succeeded
Mirroring image registry.build05.ci.openshift.org/ci-op-98gxy5d6/pipeline@sha256:94b3bb4a4fb4e0fc3d4dc38328ba5f9dde008d11c9255e631310d86a3a96523f to example-registry.azurecr.io/ci-op-98gxy5d6/pipeline:94b3bb4a4fb4e0fc3d4dc38328ba5f9dde008d11c9255e631310d86a3a96523f.
The image will still be available under it's original digest sha256:94b3bb4a4fb4e0fc3d4dc38328ba5f9dde008d11c9255e631310d86a3a96523f in the target registry.
Error: Put "https://example-registry.azurecr.io/v2/ci-op-98gxy5d6/pipeline/blobs/uploads/2ec77b7f-548b-4f97-9b7f-430a6c8db396?_nouploadcache=false&_state=...": read tcp 172.24.116.8:38578->172.64.66.1:443: read: connection reset by peer
 exit status 1"`

	got := Extract(Input{Text: raw})[0].Canonical
	lowered := strings.ToLower(got)
	if strings.Contains(lowered, "checking use_oc_login_registries") {
		t.Fatalf("expected canonical phrase to skip image-mirror setup preamble, got=%q", got)
	}
	if !strings.Contains(lowered, "connection reset by peer") {
		t.Fatalf("expected canonical phrase to keep the inner transfer failure, got=%q", got)
	}
}

func TestExtractEvidenceLogfmtImageMirrorPrefersRegistryResolutionFailure(t *testing.T) {
	t.Parallel()

	raw := `time=2026-05-05T04:33:39.731Z level=ERROR msg="Step errored." serviceGroup=Microsoft.Azure.ARO.HCP.Velero resourceGroup=global step=mirror-hypershift-plugin-image err="error running Image Mirror Step, failed to execute shell command: Checking USE_OC_LOGIN_REGISTRIES: registry.build05.ci.openshift.org registry.build05.ci.openshift.org registry.build05.ci.openshift.org registry.build05.ci.openshift.org registry.build05.ci.openshift.org
Configure authentication for source registry registry.redhat.io using synthetic test credentials.
Logging into target ACR example-registry.
Login Succeeded
Mirroring image registry.redhat.io/oadp/oadp-hypershift-velero-plugin-rhel9@sha256:381926cbc8ac3a769b17174452453ddc98c731981332374de7a0927617513a96 to example-registry.azurecr.io/oadp/oadp-hypershift-velero-plugin-rhel9:381926cbc8ac3a769b17174452453ddc98c731981332374de7a0927617513a96.
The image will still be available under it's original digest sha256:381926cbc8ac3a769b17174452453ddc98c731981332374de7a0927617513a96 in the target registry.
Error response from registry: failed to resolve sha256:381926cbc8ac3a769b17174452453ddc98c731981332374de7a0927617513a96: unavailable: Server error encountered while finding repo
 exit status 1"`

	got := Extract(Input{Text: raw})[0].Canonical
	lowered := strings.ToLower(got)
	if strings.Contains(lowered, "checking use_oc_login_registries") {
		t.Fatalf("expected canonical phrase to skip image-mirror setup preamble, got=%q", got)
	}
	if !strings.Contains(lowered, "failed to resolve") || !strings.Contains(lowered, "server error encountered while finding repo") {
		t.Fatalf("expected canonical phrase to keep the registry resolution failure, got=%q", got)
	}
}
