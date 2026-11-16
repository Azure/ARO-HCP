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
	"fmt"
	"strings"
	"testing"
)

func TestExtractEvidenceAvoidsBraceOnlyCanonicalFromWrappedErrors(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/cluster_version_backlevel.go:193]: Unexpected error:
    <*fmt.wrapError | 0xc000a823a0>: 
    route host was never found: Get "https://agnhost-e2e-serving-app-p8ds6.apps.aro.example.net": tls: failed to verify certificate: x509: certificate signed by unknown authority
    {
        msg: "route host was never found",
        err: <*url.Error | 0xc000e42f90>{
            Err: <*tls.CertificateVerificationError | 0xc000e42f60>{
                Err: <*x509.UnknownAuthorityError | 0xc0003ca3f0>{},
            },
        },
    }`

	got := Extract(Input{Text: raw})[0].Canonical
	lowered := strings.ToLower(got)
	if got == "{" || got == "}" || got == "{}" || got == "null" {
		t.Fatalf("expected non-struct canonical phrase, got=%q", got)
	}
	if !strings.Contains(lowered, "route host was never found") && !strings.Contains(lowered, "certificate signed by unknown authority") {
		t.Fatalf("expected wrapped error details in canonical phrase, got=%q", got)
	}
}

func TestExtractEvidenceCollapsesGetAdminRESTConfigTimeoutVariants(t *testing.T) {
	t.Parallel()

	rawA := `failed waiting for hcpcluster="ea-list" in resourcegroup="external-auth-rg-pxk72q" to finish getting creds, caused by: timeout '10.000000' minutes exceeded during GetAdminRESTConfigForHCPCluster for cluster ea-list`
	rawB := `failed waiting for hcpcluster="ea-list" in resourcegroup="external-auth-rg-pxk72q" to finish getting creds, caused by: timeout '10.000000' minutes exceeded during GetAdminRESTConfigForHCPCluster for cluster ea-list in resource group external-auth-rg-pxk72q`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	want := "timeout during GetAdminRESTConfigForHCPCluster while waiting for hcpcluster creds"

	if gotA != want {
		t.Fatalf("unexpected canonical for short variant: got=%q want=%q", gotA, want)
	}
	if gotB != want {
		t.Fatalf("unexpected canonical for long variant: got=%q want=%q", gotB, want)
	}
}

func TestExtractEvidenceUsesHTTP502StatusLineWhenOnlySignal(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/cluster_versions.go:42]: Unexpected error:
    <*exported.ResponseError | 0xc000736630>:
    GET https://management.example.test/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/providers/Microsoft.RedHatOpenShift/locations/uksouth/hcpOpenShiftVersions
    --------------------------------------------------------------------------------
    RESPONSE 502: 502 Bad Gateway
    ERROR CODE UNAVAILABLE
    --------------------------------------------------------------------------------
    Response contained no body
    --------------------------------------------------------------------------------
    {
        ErrorCode: "",
        StatusCode: 502,
    }`

	evidence := Extract(Input{Text: raw})[0]
	if evidence.Canonical != "RESPONSE 502: 502 Bad Gateway" {
		t.Fatalf("expected canonical phrase to use HTTP response status line, got=%q", evidence.Canonical)
	}
}

func TestExtractEvidenceNormalizesRouteHostLookupVariants(t *testing.T) {
	t.Parallel()

	rawA := `fail [github.com/Azure/ARO-HCP/test/e2e/complete_cluster_create.go:137]: Unexpected error:
route host was never found: Get "https://agnhost-e2e-serving-app-sckjc.apps.aro.u0e2e1n2t9u1a8h.4rck.j1302400.hcp.osadev.cloud": dial tcp: lookup agnhost-e2e-serving-app-sckjc.apps.aro.u0e2e1n2t9u1a8h.4rck.j1302400.hcp.osadev.cloud on 172.30.0.10:53: no such host`
	rawB := `fail [github.com/Azure/ARO-HCP/test/e2e/cluster_version_backlevel.go:194]: Unexpected error:
route host was never found: Get "https://agnhost-e2e-serving-app-9f7x5.apps.aro.l6q3l5t4y9r4i6k.15br.j8542976.hcp.osadev.cloud": dial tcp: lookup agnhost-e2e-serving-app-9f7x5.apps.aro.l6q3l5t4y9r4i6k.15br.j8542976.hcp.osadev.cloud on 172.30.0.10:53: no such host`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("expected route-host variants to normalize to same canonical phrase: gotA=%q gotB=%q", gotA, gotB)
	}
	if !strings.Contains(strings.ToLower(gotA), "lookup <host> on <dns-server>") {
		t.Fatalf("expected canonical phrase to normalize lookup host/server details, got=%q", gotA)
	}
}

func TestExtractEvidenceDialTCPAddressNormalized(t *testing.T) {
	t.Parallel()

	rawA := `fail [github.com/Azure/ARO-HCP/test/e2e/complete_cluster_create_multiversion.go:183]: Unexpected error:
    <*fmt.wrapError | 0xc000c8e620>: 
    route host was never found: Get "https://agnhost-e2e-serving-app-k8g25.apps.aro.u2q3n3k0t9h9m8l.pb5a.hcp.osadev.cloud": dial tcp 134.33.16.231:443: connect: connection timed out`
	rawB := `fail [github.com/Azure/ARO-HCP/test/e2e/cluster_version_backlevel.go:194]: Unexpected error:
    <*fmt.wrapError | 0xc001096040>: 
    route host was never found: Get "https://agnhost-e2e-serving-app-mbnnt.apps.aro.h7i5w5u7j5b3g2u.fova.hcp.osadev.cloud": dial tcp 20.40.25.244:443: connect: connection timed out`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("route-host dial-tcp errors with different IPs must canonicalize identically:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if strings.Contains(gotA, "134.33") || strings.Contains(gotA, "20.40") {
		t.Fatalf("canonical phrase must not contain raw IP addresses, got=%q", gotA)
	}
}

func TestExtractEvidenceCreateHCPClusterTimeoutVariantsUnify(t *testing.T) {
	t.Parallel()

	want := "timeout during CreateHCPClusterAndWait; context deadline exceeded"

	cases := []struct {
		name string
		raw  string
	}{
		{
			"FromParam",
			`failed to create HCP cluster hcp-cluster, caused by: timeout '45.000000' minutes exceeded during CreateHCPClusterFromParam for cluster hcp-cluster in resource group rg-abc, error: failed waiting for cluster="hcp-cluster" in resourcegroup="rg-abc" to finish creating, caused by: timeout '45.000000' minutes exceeded during CreateHCPClusterFromParam for cluster hcp-cluster in resource group rg-abc, error: context deadline exceeded`,
		},
		{
			"20251223FromParam",
			`failed to create HCP cluster idms-e2e-hcp-cluster, caused by: timeout '45.000000' minutes exceeded during CreateHCPCluster20251223FromParam for cluster idms-e2e-hcp-cluster in resource group idms-v9cd6x, error: failed waiting for cluster="idms-e2e-hcp-cluster" in resourcegroup="idms-v9cd6x" to finish creating, caused by: timeout '45.000000' minutes exceeded during CreateHCPCluster20251223FromParam for cluster idms-e2e-hcp-cluster in resource group idms-v9cd6x, error: context deadline exceeded`,
		},
		{
			"20251223AndWait",
			`failed waiting for cluster="cilium-cluster" in resourcegroup="e2e-cilium-hvlzkd" to finish creating, caused by: timeout '45.000000' minutes exceeded during CreateHCPCluster20251223AndWait for cluster cilium-cluster in resource group e2e-cilium-hvlzkd, error: context deadline exceeded`,
		},
		{
			"AndWait",
			`failed waiting for cluster="cluster-ver-4-19" in resourcegroup="rg-cluster-back-version-g5hsfc" to finish creating, caused by: timeout '45.000000' minutes exceeded during CreateHCPClusterAndWait for cluster cluster-ver-4-19 in resource group rg-cluster-back-version-g5hsfc, error: context deadline exceeded`,
		},
	}

	for _, tc := range cases {
		got := Extract(Input{Text: tc.raw})[0].Canonical
		if got != want {
			t.Errorf("case %q: got=%q want=%q", tc.name, got, want)
		}
	}
}

func TestExtractEvidenceOCPVersionStringNormalized(t *testing.T) {
	t.Parallel()

	rawV4 := `PUT https://rp.example/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster
RESPONSE 400: 400 Bad Request
ERROR CODE: InvalidRequestContent
{"error":{"code":"InvalidRequestContent","message":"Version 'openshift-v4.22.0-candidate' doesn't exist"}}`

	rawV5 := `PUT https://rp.example/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster
RESPONSE 400: 400 Bad Request
ERROR CODE: InvalidRequestContent
{"error":{"code":"InvalidRequestContent","message":"Version 'openshift-v5.1.0-candidate' doesn't exist"}}`

	gotV4 := Extract(Input{Text: rawV4})[0].Canonical
	gotV5 := Extract(Input{Text: rawV5})[0].Canonical
	if gotV4 != gotV5 {
		t.Fatalf("different OCP candidate versions must produce the same canonical:\n  v4=%q\n  v5=%q", gotV4, gotV5)
	}
	if strings.Contains(gotV4, "4.22.0") || strings.Contains(gotV4, "5.1.0") {
		t.Fatalf("canonical phrase must not contain raw version numbers, got=%q", gotV4)
	}
}

func TestExtractEvidenceClusterInternalIDNormalized(t *testing.T) {
	t.Parallel()

	rawA := `PATCH https://management.example.test/subscriptions/XXXX/resourceGroups/rg-a/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster
RESPONSE 400: 400 Bad Request
ERROR CODE: InvalidRequestContent
{"error":{"code":"InvalidRequestContent","message":"Cluster '2pmeojr923nt08rchn2mn56al24muh61' is in state 'pending_update', can't update"}}`

	rawB := `PATCH https://management.example.test/subscriptions/XXXX/resourceGroups/rg-b/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster
RESPONSE 400: 400 Bad Request
ERROR CODE: InvalidRequestContent
{"error":{"code":"InvalidRequestContent","message":"Cluster '2pni671e890elvabbe631mnqcb4pi6te' is in state 'pending_update', can't update"}}`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("same cluster-state error with different cluster IDs must canonicalize identically:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if strings.Contains(gotA, "2pmeojr923nt08rchn2mn56al24muh61") {
		t.Fatalf("canonical phrase must not contain raw cluster IDs, got=%q", gotA)
	}
}

func TestCleanCanonicalNormalizesHCPApiHostname(t *testing.T) {
	t.Parallel()

	rawA := `tls: failed to verify certificate: x509: certificate is valid for api.a5t3f6u4j8a4a5h.4ufg.eastus2.aroapp-hcp.io, reserved.aroapp-hcp.io, not api.a5t3f6u4j8a4a5h.o0jt.eastus2.aroapp-hcp.io`
	rawB := `tls: failed to verify certificate: x509: certificate is valid for api.ea-cluster.kv02.uksouth.aroapp-hcp.io, reserved.aroapp-hcp.io, not api.ea-cluster.50y5.uksouth.aroapp-hcp.io`

	gotA := cleanCanonical(rawA)
	gotB := cleanCanonical(rawB)

	if strings.Contains(gotA, "a5t3f6u4j8a4a5h") || strings.Contains(gotA, "4ufg") {
		t.Fatalf("cleanCanonical should normalize HCP hostname tokens, got=%q", gotA)
	}
	if !strings.Contains(gotA, "<hcp-api-host>") {
		t.Fatalf("cleanCanonical should replace HCP hostname with <hcp-api-host>, got=%q", gotA)
	}
	if gotA != gotB {
		t.Fatalf("cert-mismatch errors from different clusters must canonicalize identically:\n  A=%q\n  B=%q", gotA, gotB)
	}
}

func TestExtractEvidenceNormalizesLongHCPApiHostnameBeforeTruncation(t *testing.T) {
	t.Parallel()

	rawA := `VerifyAllAPIServicesAvailable failed: failed to list all APIServices: Get "https://api.a5t3f6u4j8a4a5h.4ufg.eastus2.aroapp-hcp.io:443/apis/apiregistration.k8s.io/v1/apiservices": tls: failed to verify certificate: x509: certificate is valid for api.a5t3f6u4j8a4a5h.4ufg.eastus2.aroapp-hcp.io, reserved.aroapp-hcp.io, not api.a5t3f6u4j8a4a5h.o0jt.eastus2.aroapp-hcp.io`
	rawB := `VerifyAllAPIServicesAvailable failed: failed to list all APIServices: Get "https://api.ea-cluster.kv02.uksouth.aroapp-hcp.io:443/apis/apiregistration.k8s.io/v1/apiservices": tls: failed to verify certificate: x509: certificate is valid for api.ea-cluster.kv02.uksouth.aroapp-hcp.io, reserved.aroapp-hcp.io, not api.ea-cluster.50y5.uksouth.aroapp-hcp.io`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if !strings.Contains(gotA, "<hcp-api-host>") {
		t.Fatalf("expected canonical phrase to normalize HCP API hostnames, got=%q", gotA)
	}
	if gotA != gotB {
		t.Fatalf("long hostname variants should canonicalize identically:\n  A=%q\n  B=%q", gotA, gotB)
	}
}

func TestExtractEvidencePrefersCertMismatchDetailAcrossVerifyWrappers(t *testing.T) {
	t.Parallel()

	rawA := `VerifyAllAPIServicesAvailable failed: failed to list all APIServices: Get "https://api.i2o7e9m2u0f5e5k.kyxh.uksouth.aroapp-hcp.azure-test.net:443/apis/apiregistration.k8s.io/v1/apiservices": tls: failed to verify certificate: x509: certificate is valid for api.i2o7e9m2u0f5e5k.zser.uksouth.aroapp-hcp.azure-test.net, reserved.aroapp-hcp.azure-test.net, not api.i2o7e9m2u0f5e5k.kyxh.uksouth.aroapp-hcp.azure-test.net`
	rawB := `VerifyBasicAccess failed: failed to list services: Get "https://api.m4s3u7n2e5l1m2p.nay8.uksouth.aroapp-hcp.azure-test.net:443/api/v1/namespaces/default/services": tls: failed to verify certificate: x509: certificate is valid for api.m4s3u7n2e5l1m2p.kj53.uksouth.aroapp-hcp.azure-test.net, reserved.aroapp-hcp.azure-test.net, not api.m4s3u7n2e5l1m2p.nay8.uksouth.aroapp-hcp.azure-test.net`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("cert mismatch variants should canonicalize identically across wrapper differences:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if !strings.Contains(gotA, "tls: failed to verify certificate") || !strings.Contains(gotA, "<hcp-api-host>") {
		t.Fatalf("expected canonical phrase to keep the leaf cert mismatch detail with normalized hosts, got=%q", gotA)
	}
	lowered := strings.ToLower(gotA)
	if strings.Contains(lowered, "verifybasicaccess failed") || strings.Contains(lowered, "verifyallapiservicesavailable failed") {
		t.Fatalf("expected wrapper-specific Verify* text to be excluded from canonical phrase, got=%q", gotA)
	}
}

func TestExtractEvidenceStripsTransportGetWrapperFromTLSMismatch(t *testing.T) {
	t.Parallel()

	rawA := `Get "https://api.i2o7e9m2u0f5e5k.kyxh.uksouth.aroapp-hcp.azure-test.net:443/apis/apiregistration.k8s.io/v1/apiservices": tls: failed to verify certificate: x509: certificate is valid for api.i2o7e9m2u0f5e5k.zser.uksouth.aroapp-hcp.azure-test.net, reserved.aroapp-hcp.azure-test.net, not api.i2o7e9m2u0f5e5k.kyxh.uksouth.aroapp-hcp.azure-test.net`
	rawB := `tls: failed to verify certificate: x509: certificate is valid for api.m4s3u7n2e5l1m2p.kj53.uksouth.aroapp-hcp.azure-test.net, reserved.aroapp-hcp.azure-test.net, not api.m4s3u7n2e5l1m2p.nay8.uksouth.aroapp-hcp.azure-test.net`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("transport wrapped and plain TLS mismatch variants should canonicalize identically:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if strings.Contains(strings.ToLower(gotA), `get "<url>`) {
		t.Fatalf("expected transport Get wrapper to be stripped from canonical phrase, got=%q", gotA)
	}
	if !strings.HasPrefix(strings.ToLower(gotA), "tls: failed to verify certificate:") {
		t.Fatalf("expected canonical phrase to keep the TLS mismatch detail, got=%q", gotA)
	}
}

func TestExtractEvidenceNormalizesCleanupWorkflowDeletionVariants(t *testing.T) {
	t.Parallel()

	rawA := `failed to cleanup resource group: ordered cleanup workflow failed for admin-api-serialconsole-bootdiag-wfsm7c: Delete virtual networks: failed deleting sre-vnet-name (Microsoft.Network/virtualNetworks): DELETE https://management.example.test/subscriptions/XXXX/resourceGroups/rg-a/providers/Microsoft.Network/virtualNetworks/sre-vnet-name`
	rawB := `failed to cleanup resource group: ordered cleanup workflow failed for admin-api-serialconsole-bootdiag-kgr5zt: Delete virtual networks: failed deleting customer-vnet (Microsoft.Network/virtualNetworks): GET https://management.example.test/subscriptions/XXXX/resourceGroups/rg-b/providers/Microsoft.Network/virtualNetworks/customer-vnet`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("cleanup workflow deletion variants should canonicalize identically:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if !strings.Contains(gotA, "<cleanup-target>") {
		t.Fatalf("expected cleanup target name to be normalized, got=%q", gotA)
	}
	if !strings.Contains(gotA, "<cleanup-resource>") {
		t.Fatalf("expected cleanup resource name to be normalized, got=%q", gotA)
	}
	if strings.Contains(strings.ToLower(gotA), "delete <url>") || strings.Contains(strings.ToLower(gotA), "get <url>") {
		t.Fatalf("expected transport method before cleanup URL to be normalized away, got=%q", gotA)
	}
}

func TestExtractEvidenceNormalizesOnClusterPhrase(t *testing.T) {
	t.Parallel()

	rawA := `fail [ ]: expected exactly one external auth <external-auth> on cluster ea-listcf4xll`
	rawB := `fail [ ]: expected exactly one external auth <external-auth> on cluster ea-listz4ptjc`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical

	if gotA != gotB {
		t.Fatalf("expected generated cluster-name suffixes after 'on cluster' to normalize to the same canonical phrase:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if !strings.Contains(gotA, "on cluster <cluster>") {
		t.Fatalf("expected 'on cluster' placeholder, got=%q", gotA)
	}
}

func TestExtractEvidenceKeepsFullMessageWithEmbeddedEscapedQuote(t *testing.T) {
	t.Parallel()

	// Real RP response: the message legitimately contains an escaped quoted
	// substring. decodeEscapedErrorPayload's blanket \" -> " unescape (needed
	// to unwrap nested-JSON-in-string cases like VaultAlreadyExists) used to
	// make extractAzureMessageForCode's `[^"]+` capture stop at the first
	// unescaped quote, truncating the message down to "Invalid value:" and
	// silently dropping the actionable detail.
	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/cluster_fips_mode.go:84]: failed to create HCP cluster "fips-enabled-cluster" with FIPS enabled
Unexpected error:
    <*fmt.wrapError | 0xc000ccd860>: 
    failed starting cluster creation "fips-enabled-cluster" in resourcegroup="fips-enabled-m894z8": PUT https://management.example.test/subscriptions/XXXX/resourceGroups/fips-enabled-m894z8/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/fips-enabled-cluster
    --------------------------------------------------------------------------------
    RESPONSE 400: 400 Bad Request
    ERROR CODE: InvalidRequestContent
    --------------------------------------------------------------------------------
    {
      "error": {
        "code": "InvalidRequestContent",
        "message": "Invalid value: \"aro-hcp.experimental.cluster.max-creation-duration\": unrecognized experimental tag",
        "target": "tags[aro-hcp.experimental.cluster.max-creation-duration]"
      }
    }
    --------------------------------------------------------------------------------
occurred`

	got := Extract(Input{Text: raw})[0].Canonical
	if !strings.Contains(got, "unrecognized experimental tag") {
		t.Fatalf("expected the full RP detail message (including its embedded escaped quote) to survive extraction instead of being truncated to just \"Invalid value:\", got=%q", got)
	}
	if !strings.Contains(got, "detail message") {
		t.Fatalf("expected a detail message segment in the canonical phrase, got=%q", got)
	}
}

func TestExtractEvidenceMergesOperationTimeoutDurations(t *testing.T) {
	t.Parallel()

	rawA := `failed waiting for hcpCluster="kms-key-rotate-a" in resourcegroup="rg-a" to finish updating, caused by: timeout '18.000000' minutes exceeded during UpdateHCPCluster20260630 for cluster kms-key-rotate-a in resource group rg-a, error: context deadline exceeded`
	rawB := `failed waiting for hcpCluster="kms-key-rotate-b" in resourcegroup="rg-b" to finish updating, caused by: timeout '30.000000' minutes exceeded during UpdateHCPCluster20260630 for cluster kms-key-rotate-b in resource group rg-b, error: context deadline exceeded`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	want := "UpdateHCPCluster20260630 timed out after <minutes> minutes; context deadline exceeded"
	if gotA != want || gotB != want {
		t.Fatalf("expected timeout durations to merge on the operation and terminal cause:\n  A=%q\n  B=%q\nwant=%q", gotA, gotB, want)
	}
}

func TestExtractEvidenceKeepsCleanupTimeoutOperation(t *testing.T) {
	t.Parallel()

	raw := `failed to cleanup resource group: failed deleting hcp clusters in resourcegroup="rg-a", caused by: failed waiting for hcpCluster="cluster-a" in resourcegroup="rg-a" to finish deleting, caused by: timeout '25.000000' minutes exceeded during DeleteHCPCluster for cluster cluster-a in resource group rg-a, error: context deadline exceeded`

	got := Extract(Input{Text: raw})[0].Canonical
	want := "DeleteHCPCluster timed out after <minutes> minutes; context deadline exceeded"
	if got != want {
		t.Fatalf("expected cleanup wrapper truncation to preserve the timed-out operation, got=%q want=%q", got, want)
	}
}

func TestExtractEvidenceMergesWrappedUnknownAuthorityFailures(t *testing.T) {
	t.Parallel()

	rawA := `VerifySimpleWebApp failed: [strict TLS verification] route was never reachable: Get "https://app-a.example": tls: failed to verify certificate: x509: certificate signed by unknown authority`
	rawB := `[strict TLS verification] route was never reachable: Get "https://app-b.example": tls: failed to verify certificate: x509: certificate signed by unknown authority`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	want := "tls: failed to verify certificate: x509: certificate signed by unknown authority"
	if gotA != want || gotB != want {
		t.Fatalf("expected TLS transport wrappers to merge on the certificate failure:\n  A=%q\n  B=%q\nwant=%q", gotA, gotB, want)
	}
}

func TestExtractEvidenceNormalizesGeneratedRouteAndNetworkArtifacts(t *testing.T) {
	t.Parallel()

	rawA := `VerifySimpleWebApp failed: DNS for route host agnhost-e2e-sample-app-bmdd7.apps.aro.cilium-cl.dus8.j7009920.hcp.osadev.cloud did not resolve: DNS for agnhost-e2e-sample-app-bmdd7.apps.aro.cilium-cl.dus8.j7009920.hcp.osadev.cloud did not resolve within 10m0s (last error: lookup agnhost-e2e-sample-app-bmdd7.apps.aro.cilium-cl.dus8.j7009920.hcp.osadev.cloud on 172.30.0.10:53: no such host)`
	rawB := `VerifySimpleWebApp failed: DNS for route host agnhost-e2e-sample-app-q26kx.apps.aro.cilium-cl.0290.j8953600.hcp.osadev.cloud did not resolve: DNS for agnhost-e2e-sample-app-q26kx.apps.aro.cilium-cl.0290.j8953600.hcp.osadev.cloud did not resolve within 12m0s (last error: lookup agnhost-e2e-sample-app-q26kx.apps.aro.cilium-cl.0290.j8953600.hcp.osadev.cloud on 172.31.0.10:53: no such host)`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("expected generated route hosts, DNS servers, and durations to merge:\n  A=%q\n  B=%q", gotA, gotB)
	}
	for _, artifact := range []string{"bmdd7", "q26kx", "j7009920", "j8953600", "172.30.0.10", "172.31.0.10", "10m0s", "12m0s"} {
		if strings.Contains(gotA, artifact) {
			t.Fatalf("expected route/network artifact %q to be normalized, got=%q", artifact, gotA)
		}
	}
}

func TestExtractEvidenceNormalizesKubernetesTestResourceNames(t *testing.T) {
	t.Parallel()

	rawA := `pods "policy-test-allowed" is forbidden: error looking up service account image-policy-test-bc57p/default: serviceaccount "default" not found`
	rawB := `pods "policy-test-other" is forbidden: error looking up service account image-policy-test-vghkn/default: serviceaccount "default" not found`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("expected generated pod and namespace names to merge:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if !strings.Contains(gotA, `pod "<pod>"`) || !strings.Contains(gotA, "service account <namespace>/default") {
		t.Fatalf("expected Kubernetes resource placeholders, got=%q", gotA)
	}
}

func TestExtractEvidenceSummarizesBreakglassSessionPath(t *testing.T) {
	t.Parallel()

	rawA := `failed to get ready session kubeconfig from /admin/v1/hcp/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourcegroups/admin-api-breakglass-ps66xjjnqrfj/providers/microsoft.redhatopenshift/hcpopenshiftclusters/sre-hcp-cluster/breakglass/breakglass-h7gq5/kubeconfig: timeout waiting for session to become ready (last status: {"HostedControlPlaneAvailable":{"message":"HostedControlPlane exists but is not ready"}})`
	rawB := strings.ReplaceAll(rawA, "admin-api-breakglass-ps66xjjnqrfj", "admin-api-breakglass-other")
	rawB = strings.ReplaceAll(rawB, "breakglass-h7gq5", "breakglass-other")

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	want := "breakglass session kubeconfig not ready: timeout waiting for session to become ready; HostedControlPlane exists but is not ready"
	if gotA != want || gotB != want {
		t.Fatalf("expected stable breakglass readiness summary:\n  A=%q\n  B=%q\nwant=%q", gotA, gotB, want)
	}
}

func TestExtractEvidenceSummarizesDenyAssignmentWithoutResourceIDs(t *testing.T) {
	t.Parallel()

	raw := `ERROR CODE: InternalServerError
{"error":{"code":"InternalServerError","details":[{"code":"DenyAssignmentAuthorizationFailed","message":"The client 'XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX' with object id '0e96dbb7-17a2-4393-b2d3-d84524e39c32' has permission to perform action 'Microsoft.Compute/virtualMachines/retrieveBootDiagnosticsData/action' on scope '/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourceGroups/generated/providers/Microsoft.Compute/virtualMachines/generated'; however, the access is denied because of the deny assignment with Id '4e1d5354563b53f0a6639624ce82fcc6'."}]}}`

	got := Extract(Input{Text: raw})[0].Canonical
	if !strings.Contains(got, `Access denied by deny assignment for action "Microsoft.Compute/virtualMachines/retrieveBootDiagnosticsData/action".`) {
		t.Fatalf("expected stable deny-assignment summary, got=%q", got)
	}
	for _, artifact := range []string{"0e96dbb7", "4e1d5354", "resourceGroups/generated"} {
		if strings.Contains(got, artifact) {
			t.Fatalf("expected deny-assignment artifact %q to be removed, got=%q", artifact, got)
		}
	}
}

func TestExtractEvidenceMergesNodePoolDeadlineMachineDumps(t *testing.T) {
	t.Parallel()

	base := `GET https://rp.example/subscriptions/XXXX/providers/Microsoft.RedHatOpenShift/locations/westus3/hcpOperationStatuses/id
ERROR CODE: DeadlineExceeded
{"error":{"code":"DeadlineExceeded","message":%q}}`
	rawA := fmt.Sprintf(base, `node pool creation did not complete before the deadline; [clusterServiceNodePoolStatus] cluster service node pool is installing; [hypershiftNodePool] hypershift NodePool status replicas is 0, want 2; node pool condition AllNodesHealthy is False: 2 of 2 machines are not healthy Machine r7c9a5t8w8z7a7s-np-g7cq98-q7czc-7skt7: InspectionFailed: Waiting for AzureMachine to report spec.providerID Machine r7c9a5t8w8z7a7s-np-g7cq98-q7czc-v7h4x: InspectionFailed: Waiting for AzureMachine to report spec.providerID; node pool condition AllMachinesReady is False: 2 of 2 machines are not ready Machine r7c9a5t8w8z7a7s-np-g7cq98-q7czc-7skt7: Creating: virtualmachine creating or updating Machine r7c9a5t8w8z7a7s-np-g7cq98-q7czc-v7h4x: Creating: virtualmachine creating or updating`)
	rawB := fmt.Sprintf(base, `node pool creation did not complete before the deadline; [clusterServiceNodePoolStatus] cluster service node pool is installing; [hypershiftNodePool] hypershift NodePool status replicas is 1, want 2; node pool condition AllNodesHealthy is False: 1 of 2 machines are not healthy Machine cilium-cluster-cilium-np-597b9-prdsc: InspectionFailed: Waiting for AzureMachine to report spec.providerID; node pool condition AllMachinesReady is False: 1 of 2 machines are not ready Machine cilium-cluster-cilium-np-597b9-prdsc: Creating: virtualmachine creating or updating`)
	rawAutoscale := fmt.Sprintf(base, `node pool creation did not complete before the deadline; [clusterServiceNodePoolStatus] cluster service node pool is installing; [hypershiftNodePool] hypershift NodePool status replicas is 2, want >= 3 (autoscaling min); node pool condition AllNodesHealthy is False: 1 of 3 machines are not healthy Machine p5d6o0j0i5s4u7a-autoscale-noaz-686hx-dvr6p: InspectionFailed: Waiting for AzureMachine to report spec.providerID; node pool condition AllMachinesReady is False: 1 of 3 machines are not ready Machine p5d6o0j0i5s4u7a-autoscale-noaz-686hx-dvr6p: Creating: virtualmachine creating or updating`)

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("expected node-pool machine dumps and replica counts to merge:\n  A=%q\n  B=%q", gotA, gotB)
	}
	for _, artifact := range []string{"r7c9a5t8w8z7a7s", "cilium-cluster-cilium-np-597b9-prdsc", "replicas is 0", "replicas is 1", "2 of 2 machines", "1 of 2 machines"} {
		if strings.Contains(gotA, artifact) {
			t.Fatalf("expected node-pool artifact %q to be normalized, got=%q", artifact, gotA)
		}
	}
	if !strings.Contains(gotA, "node pool creation did not complete before the deadline") {
		t.Fatalf("expected node pool creation wording to be preserved, got=%q", gotA)
	}
	if !strings.Contains(gotA, "NodePool status replicas is <count>, want <count>") {
		t.Fatalf("expected replica-count placeholders, got=%q", gotA)
	}
	if strings.Count(strings.ToLower(gotA), "machine <node>: inspectionfailed") != 1 {
		t.Fatalf("expected duplicate machine InspectionFailed rows to collapse, got=%q", gotA)
	}

	gotAutoscale := Extract(Input{Text: rawAutoscale})[0].Canonical
	if gotA == gotAutoscale {
		t.Fatalf("expected autoscaling replica target to remain a merge boundary, got=%q", gotA)
	}
	if !strings.Contains(gotAutoscale, "want >= <count> (autoscaling min)") {
		t.Fatalf("expected autoscaling replica placeholder, got=%q", gotAutoscale)
	}
}

func TestExtractEvidenceMergesNightlyVersionAndOrasArtifacts(t *testing.T) {
	t.Parallel()

	rawA := `ERROR CODE: InvalidRequestContent
{"error":{"code":"InvalidRequestContent","message":"Invalid value: \"4.20.0-0.nightly-multi-2026-09-30-014938\": must be at least 4.20.8"}}`
	rawB := `ERROR CODE: InvalidRequestContent
{"error":{"code":"InvalidRequestContent","message":"Invalid value: \"4.20.0-0.nightly-multi-2026-10-01-125400\": must be at least 4.20.8"}}`
	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("expected nightly payload timestamps to merge:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if strings.Contains(gotA, "2026-09-30") || strings.Contains(gotA, "014938") {
		t.Fatalf("expected nightly timestamp to be normalized, got=%q", gotA)
	}

	orasA := `Command failed after 5 attempts: oras cp registry.build11.ci.openshift.org/ci-op-49jbb95d/pipeline@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa example-registry.azurecr.io/ci-op-49jbb95d/pipeline:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb --from-registry-config /tmp/tmp.WQPJJsgkPI/containers/auth.json --to-registry-config /tmp/tmp.WQPJJsgkPI/containers/auth.json`
	orasB := `Command failed after 5 attempts: oras cp registry.build11.ci.openshift.org/ci-op-8mw8v43n/pipeline@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc example-registry.azurecr.io/ci-op-8mw8v43n/pipeline:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd --from-registry-config /tmp/tmp.RKxD35P2Nz/containers/auth.json --to-registry-config /tmp/tmp.RKxD35P2Nz/containers/auth.json`
	gotOrasA := Extract(Input{Text: orasA})[0].Canonical
	gotOrasB := Extract(Input{Text: orasB})[0].Canonical
	if gotOrasA != gotOrasB {
		t.Fatalf("expected ci-op ids and tmp dirs to merge:\n  A=%q\n  B=%q", gotOrasA, gotOrasB)
	}
	if strings.Contains(gotOrasA, "ci-op-49jbb95d") || strings.Contains(gotOrasA, "/tmp/tmp.WQPJJsgkPI") {
		t.Fatalf("expected oras artifacts to be normalized, got=%q", gotOrasA)
	}
}

func TestExtractEvidenceMergesDelayedRbacIdentityNames(t *testing.T) {
	t.Parallel()

	rawA := `cluster delayed-rbac-87tl668hqnnr/delayed-rbac-cluster entered terminal state "Failed" after role assignment deployment; expected "Succeeded"`
	rawB := `cluster delayed-rbac-7fppc52tbxcc/delayed-rbac-cluster entered terminal state "Failed" after role assignment deployment; expected "Succeeded"`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("expected generated delayed-rbac identities to merge:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if strings.Contains(gotA, "delayed-rbac-87tl668hqnnr") || strings.Contains(gotA, "delayed-rbac-7fppc52tbxcc") {
		t.Fatalf("expected identity name to be scrubbed, got=%q", gotA)
	}
	want := `cluster <resource-group>/<cluster> entered terminal state "Failed" after role assignment deployment; expected "Succeeded"`
	if gotA != want {
		t.Fatalf("unexpected canonical phrase: got=%q want=%q", gotA, want)
	}
}

func TestExtractSwiftRouterChecksMergeBoundaries(t *testing.T) {
	t.Parallel()

	const resource = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/sanitized-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/engineering-hcp-cluster"
	const scopeA = "dev/centralus/ci01-j1234567-mgmt-1/ocm-arohcpci01-2trdkknmfi0cmhb1a4nkohkjgrp69dn9-cluster"
	const scopeB = "int/westus3/ci02-j7654321-mgmt-2/ocm-arohcpci02-2trdlr774ff7plvv8r6kmkd288vpjt17-cluster"
	const mapping = "no customer resource ID -> live ReadDesire HostedCluster -> fleet management cluster mapping in Kusto"
	const summary = "expected matching summary after test start and within 8m"
	const replicas = "expected all current routers >= desired replicas > 0"
	groups := make(map[string]Finding)
	for _, tc := range []struct {
		name, group, duration, reason, predicate string
	}{
		{"mapping first", "mapping", "12m0.001s", mapping, mapping},
		{"mapping second", "mapping", "12m0.007s", mapping, mapping},
		{
			"summary first", "summary", "12m0.004s",
			scopeA + " router=11111111-2222-3333-4444-555555555555 node=aks-userswft1-12345678-vmss000001: " + summary + "; summaryUID=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee node=aks-userswft1-12345678-vmss000001 pass=ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb at=2026-10-01T12:30:00Z",
			summary,
		},
		{
			"summary second", "summary", "12m0.001s",
			scopeB + " router=22222222-3333-4444-5555-666666666666 node=aks-userswft3-87654321-vmss000004: " + summary + "; summaryUID=bbbbbbbb-cccc-dddd-eeee-ffffffffffff node=aks-userswft3-87654321-vmss000004 pass=aaaaaaaa-ffff-eeee-dddd-cccccccccccc at=2026-10-02T14:45:01Z",
			summary,
		},
		{
			"summary missing values", "summary", "12m0.005s",
			scopeB + " router=22222222-3333-4444-5555-666666666666 node=aks-userswft3-87654321-vmss000004: " + summary + "; summaryUID= node=aks-userswft3-87654321-vmss000004 pass= at=0001-01-01T00:00:00Z",
			summary,
		},
		{"replicas first", "replicas", "12m0.005s", scopeA + ": " + replicas + "; desired=3 current=2 rows=2", replicas},
		{"replicas second", "replicas", "12m0.002s", scopeB + ": " + replicas + "; desired=5 current=4 rows=3", replicas},
		{"different freshness predicate", "shorter freshness", "12m0.004s", scopeA + ": expected matching summary after test start and within 5m", "within 5m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "VerifySwiftRouterChecks " + resource + " timed out after " + tc.duration + ": " + tc.reason
			got := Extract(Input{Text: raw})[0]
			if !strings.Contains(got.Canonical, tc.predicate) {
				t.Fatalf("meaningful SWIFT predicate lost: %q", got.Canonical)
			}
			if !strings.Contains(got.Canonical, "timed out after <duration>:") {
				t.Fatalf("timeout duration survived: %q", got.Canonical)
			}
			for _, identity := range []string{"ci01-j", "ci02-j", "ocm-arohcp", "aks-userswft", "2026-10-", "0001-01-", "desired=3", "current=2", "rows=2"} {
				if strings.Contains(got.Canonical, identity) {
					t.Fatalf("ephemeral identity %q survived: %q", identity, got.Canonical)
				}
			}
			if previous, ok := groups[tc.group]; ok && previous.Key != got.Key {
				t.Fatalf("same SWIFT cause split:\n  A=%q\n  B=%q", previous.Canonical, got.Canonical)
			}
			for group, previous := range groups {
				if group != tc.group && previous.Key == got.Key {
					t.Fatalf("different SWIFT causes merged: %s / %s: %q", group, tc.group, got.Canonical)
				}
			}
			groups[tc.group] = got
		})
	}
}

func TestExtractWildcardDNSRecordMergeBoundaries(t *testing.T) {
	t.Parallel()

	recordError := func(record string) string {
		message := "The resource record '" + record + "' does not exist in resource group 'sanitized-rg' of subscription '11111111-1111-1111-1111-111111111111'."
		return fmt.Sprintf("ERROR CODE: NotFound\n"+`{"error":{"code":"NotFound","message":%q}}`+"\nprovider Microsoft.Network", message)
	}
	for _, tc := range []struct {
		name, a, b string
		merge      bool
	}{
		{"generated wildcard record", "*.e2e-dns-hqz2c6", "*.e2e-dns-7g8cdz", true},
		{"wildcard versus apex", "*.e2e-dns-hqz2c6", "e2e-dns-hqz2c6", false},
		{"different workload", "*.e2e-dns-hqz2c6", "*.other-dns-hqz2c6", false},
		{"stable record suffix", "*.e2e-dns-primary", "*.e2e-dns-secondary", false},
		{"version record suffix", "*.e2e-dns-v20260901preview", "*.e2e-dns-v20261001preview", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := Extract(Input{Text: recordError(tc.a)})[0]
			b := Extract(Input{Text: recordError(tc.b)})[0]
			if equal := a.Key == b.Key; equal != tc.merge {
				t.Fatalf("merge=%v want %v:\n  A=%q\n  B=%q", equal, tc.merge, a.Canonical, b.Canonical)
			}
			if tc.merge && !strings.Contains(a.Canonical, "resource record '*.e2e-dns-<id>' does not exist") {
				t.Fatalf("wildcard DNS predicate lost: %q", a.Canonical)
			}
		})
	}
}

func TestExtractScopedVMIdentityMergeBoundaries(t *testing.T) {
	t.Parallel()

	vmError := func(name string) string {
		message := "node pool creation did not complete before the deadline; Machine generated-machine: InspectionFailed: Waiting for a Node with spec.providerID azure:///subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/sanitized-rg/providers/Microsoft.Compute/virtualMachines/" + name + " to exist"
		return fmt.Sprintf("ERROR CODE: DeploymentFailed\n"+`{"error":{"code":"DeploymentFailed","details":[{"code":"DeadlineExceeded","message":%q}]}}`, message)
	}
	for _, tc := range []struct {
		name, a, b string
		merge      bool
	}{
		{"upgrade VM suffixes", "cluster-npupgrade-4-20-hrfgd-khxf4", "cluster-npupgrade-4-20-tzrbd-5dbrz", true},
		{"letter-only VM suffixes", "cluster-npupgrade-4-20-hrfgd-jglhd", "cluster-npupgrade-4-20-tzrbd-prdsc", true},
		{"ordinary VM suffixes", "cluster-np-1-tzrbd-khxf4", "cluster-np-1-hrfgd-5dbrz", true},
		{"different workload", "cluster-npupgrade-4-20-hrfgd-khxf4", "cluster-worker-4-20-tzrbd-5dbrz", false},
		{"different version", "cluster-npupgrade-4-20-hrfgd-khxf4", "cluster-npupgrade-4-21-tzrbd-5dbrz", false},
		{"ambiguous VM suffix", "worker-abcdef", "worker-ghijkl", false},
		{"version-like VM name", "release-v20260901preview", "release-v20261001preview", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := Extract(Input{Text: vmError(tc.a)})[0]
			b := Extract(Input{Text: vmError(tc.b)})[0]
			if equal := a.Key == b.Key; equal != tc.merge {
				t.Fatalf("merge=%v want %v:\n  A=%q\n  B=%q", equal, tc.merge, a.Canonical, b.Canonical)
			}
			if tc.merge && (!strings.Contains(a.Canonical, "/virtualMachines/cluster-") || !strings.Contains(a.Canonical, "-<id> to exist")) {
				t.Fatalf("VM reference context lost: %q", a.Canonical)
			}
		})
	}
	inspection := Extract(Input{Text: vmError("cluster-npupgrade-4-20-hrfgd-khxf4")})[0]
	ready := Extract(Input{Text: strings.ReplaceAll(vmError("cluster-npupgrade-4-20-hrfgd-khxf4"), "InspectionFailed", "ReadyUnknown")})[0]
	if inspection.Key == ready.Key {
		t.Fatalf("different VM readiness causes merged: %q / %q", inspection.Canonical, ready.Canonical)
	}
}

func TestExtractPreservesIdentitiesOutsideScopedContracts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, a, b string
	}{
		{"VM-like global name", "cluster-npupgrade-4-20-hrfgd-khxf4", "cluster-npupgrade-4-20-tzrbd-5dbrz"},
		{"global wildcard", "*.e2e-dns-hqz2c6", "*.e2e-dns-7g8cdz"},
		{"non-VM resource", "/providers/Microsoft.Compute/disks/cluster-np-1-hrfgd-khxf4", "/providers/Microsoft.Compute/disks/cluster-np-1-tzrbd-5dbrz"},
		{"global node", "node=aks-userswft1-12345678-vmss000001", "node=aks-userswft3-87654321-vmss000004"},
		{"global counter", "desired=3 current=2 rows=2", "desired=5 current=4 rows=3"},
		{"global pass", "pass=first-pass", "pass=second-pass"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := Extract(Input{Text: "failed reconciling widget " + tc.a})[0]
			b := Extract(Input{Text: "failed reconciling widget " + tc.b})[0]
			if a.Key == b.Key {
				t.Fatalf("out-of-contract identities merged: %q / %q", a.Canonical, b.Canonical)
			}
		})
	}
}
