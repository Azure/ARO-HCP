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

func TestExtractEvidenceAvoidsEmptyErrorCodeStructField(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/cluster_authorized_cidrs_connectivity.go:133]: Unexpected error:
    <*fmt.wrapError | 0xc00097b920>: 
    failed to create HCP cluster cidr-connectivity-test: failed starting cluster creation "cidr-connectivity-test" in resourcegroup="e2e-cidr-connectivity-f9k9vw": PUT https://management.example.test/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourceGroups/e2e-cidr-connectivity-f9k9vw/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cidr-connectivity-test
    --------------------------------------------------------------------------------
    RESPONSE 502: 502 Bad Gateway
    ERROR CODE UNAVAILABLE
    --------------------------------------------------------------------------------
    Response contained no body
    --------------------------------------------------------------------------------
    {
        msg: "failed to create HCP cluster cidr-connectivity-test",
        err: <*azcore.ResponseError | 0xc0002a6d80>{
            ErrorCode: "",
        },
    }`

	got := Extract(Input{Text: raw})[0].Canonical
	lowered := strings.ToLower(got)
	if strings.Contains(lowered, "errorcode: \"\"") || strings.Contains(lowered, "errorcode:\"\"") {
		t.Fatalf("expected empty error code struct field to be excluded from canonical phrase, got=%q", got)
	}
	if !strings.Contains(lowered, "failed to create hcp cluster") {
		t.Fatalf("expected cluster create failure line in canonical phrase, got=%q", got)
	}
}

func TestExtractEvidenceUsesAzureInnerThrottlingCodeAndMessage(t *testing.T) {
	t.Parallel()

	raw := `ERROR CODE: InternalServerError
{
  "error": {
    "code": "InternalServerError",
    "message": "failed to get managed identity '/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourceGroups/aro-hcp-test-msi-containers-dev-297/providers/Microsoft.ManagedIdentity/userAssignedIdentities/image-registry': GET https://management.example.test/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourceGroups/aro-hcp-test-msi-containers-dev-297/providers/Microsoft.ManagedIdentity/userAssignedIdentities/image-registry\n--------------------------------------------------------------------------------\nRESPONSE 429: 429 Too Many Requests\nERROR CODE: SubscriptionRequestsThrottled\n--------------------------------------------------------------------------------\n{\n  \"error\": {\n    \"code\": \"SubscriptionRequestsThrottled\",\n    \"message\": \"Number of 'read' requests for subscription 'XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX' actor 'd6b62dfa-87f5-49b3-bbcb-4a687c4faa96' exceeded. Please try again after '10' seconds after additional tokens are available. Refer to https://aka.ms/arm-throttling for additional information.\"\n  }\n}\n--------------------------------------------------------------------------------\n"
  }
}`

	evidence := Extract(Input{Text: raw})[0]
	if !strings.Contains(evidence.Canonical, "ERROR CODE: InternalServerError") {
		t.Fatalf("expected canonical phrase to keep root error code, got=%q", evidence.Canonical)
	}
	if !strings.Contains(evidence.Canonical, "detail code SubscriptionRequestsThrottled") {
		t.Fatalf("expected canonical phrase to include inner throttling code, got=%q", evidence.Canonical)
	}
	if !strings.Contains(strings.ToLower(evidence.Canonical), "detail message number of 'read' requests for subscription") {
		t.Fatalf("expected canonical phrase to include throttling message summary, got=%q", evidence.Canonical)
	}
	if !strings.Contains(evidence.Canonical, "provider Microsoft.ManagedIdentity") {
		t.Fatalf("expected provider Microsoft.ManagedIdentity to be included alongside inner detail, got=%q", evidence.Canonical)
	}
}

func TestExtractEvidenceUsesAzureNestedInvalidRequestDetail(t *testing.T) {
	t.Parallel()

	raw := `ERROR CODE: DeploymentFailed
{
  "status": "Failed",
  "error": {
    "code": "DeploymentFailed",
    "message": "At least one resource deployment operation failed.",
    "details": [
      {
        "code": "Conflict",
        "message": "{\r\n  \"status\": \"Failed\",\r\n  \"error\": {\r\n    \"code\": \"ResourceDeploymentFailure\",\r\n    \"message\": \"The resource write operation failed to complete successfully, because it reached terminal provisioning state 'Failed'.\",\r\n    \"details\": [\r\n      {\r\n        \"code\": \"DeploymentFailed\",\r\n        \"message\": \"At least one resource deployment operation failed.\",\r\n        \"details\": [\r\n          {\r\n            \"code\": \"InvalidRequest\",\r\n            \"message\": \"The current utilization does not meet the criteria for both MaxTimeSeries and MaxEventsPerMinute quota requested. Please reach the required usage threshold of 50% of desired limit before requesting an increase, or request a limit increase of up to 200% of your current usage. For more details, see https://go.microsoft.com/fwlink/?linkid=2270124\"\r\n          }\r\n        ]\r\n      }\r\n    ]\r\n  }\r\n}"
      }
    ]
  }
}`

	evidence := Extract(Input{Text: raw})[0]
	if !strings.Contains(evidence.Canonical, "ERROR CODE: DeploymentFailed") {
		t.Fatalf("expected canonical phrase to keep root deployment code, got=%q", evidence.Canonical)
	}
	if !strings.Contains(evidence.Canonical, "detail code InvalidRequest") {
		t.Fatalf("expected canonical phrase to include nested invalid request code, got=%q", evidence.Canonical)
	}
	if !strings.Contains(strings.ToLower(evidence.Canonical), "detail message the current utilization does not meet the criteria") {
		t.Fatalf("expected canonical phrase to include quota message summary, got=%q", evidence.Canonical)
	}
}

func TestExtractEvidenceKeepsGenericAzureConflictDetailCode(t *testing.T) {
	t.Parallel()

	raw := `time=2026-03-02T16:29:57.122Z level=ERROR msg="Step errored." err="failed to run ARM step: failed to wait for deployment completion: GET https://management.example.test/subscriptions/123/resourcegroups/hcp-underlay/providers/Microsoft.EventGrid/namespaces/arohcp/providers/Microsoft.Resources/deployments/x
ERROR CODE: DeploymentFailed
{ "error": { "code": "DeploymentFailed", "details": [ { "code": "Conflict", "message": "operation failed due to an internal server error" } ] } }"`

	evidence := Extract(Input{Text: raw})[0]
	if !strings.Contains(evidence.Canonical, "detail code Conflict") {
		t.Fatalf("expected canonical phrase to keep generic inner conflict code, got=%q", evidence.Canonical)
	}
	if !strings.Contains(strings.ToLower(evidence.Canonical), "provider microsoft.eventgrid") {
		t.Fatalf("expected provider Microsoft.EventGrid to be appended alongside detail code, got=%q", evidence.Canonical)
	}
}

func TestExtractEvidenceUsesAzureNestedRoleAssignmentLimitDetail(t *testing.T) {
	t.Parallel()

	raw := `ERROR CODE: DeploymentFailed
{
  "status": "Failed",
  "error": {
    "code": "DeploymentFailed",
    "message": "At least one resource deployment operation failed.",
    "details": [
      {
        "code": "Conflict",
        "message": "{\r\n  \"error\": {\r\n    \"code\": \"RoleAssignmentLimitExceeded\",\r\n    \"message\": \"The role assignment limit for the subscription has been reached.\"\r\n  }\r\n}"
      }
    ]
  }
}`

	evidence := Extract(Input{Text: raw})[0]
	if !strings.Contains(evidence.Canonical, "detail code RoleAssignmentLimitExceeded") {
		t.Fatalf("expected canonical phrase to include role-assignment detail code, got=%q", evidence.Canonical)
	}
	if !strings.Contains(strings.ToLower(evidence.Canonical), "detail message the role assignment limit for the subscription has been reached") {
		t.Fatalf("expected canonical phrase to include role-assignment detail message, got=%q", evidence.Canonical)
	}
}

func TestExtractEvidenceUsesAzureOverconstrainedAllocationDetail(t *testing.T) {
	t.Parallel()

	raw := `ERROR CODE: DeploymentFailed
{
  "status": "Failed",
  "error": {
    "code": "DeploymentFailed",
    "message": "At least one resource deployment operation failed.",
    "details": [
      {
        "code": "OverconstrainedZonalAllocationRequest",
        "message": "Allocation failed. We do not have sufficient capacity for the requested VM size in this zone. Please try again later."
      }
    ]
  }
}`

	evidence := Extract(Input{Text: raw})[0]
	if !strings.Contains(evidence.Canonical, "detail code OverconstrainedZonalAllocationRequest") {
		t.Fatalf("expected canonical phrase to include zonal allocation detail code, got=%q", evidence.Canonical)
	}
	if !strings.Contains(strings.ToLower(evidence.Canonical), "detail message allocation failed.") {
		t.Fatalf("expected canonical phrase to include normalized allocation failure summary, got=%q", evidence.Canonical)
	}
}

func TestExtractEvidenceSkipsTruncatedAzureDetailCodeSuffix(t *testing.T) {
	t.Parallel()

	raw := `ERROR CODE: InternalServerError
{
  "error": {
    "code": "InternalServerError",
    "message": "inner payload",
    "details": [
      {
        "code": "SubscriptionRequestsThrottled",
        "message": "Number of 'read' requests for subscription 'XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX' actor '11111111-2222-3333-4444-555555555555' exceeded."
      }
    ]
  }
}
ERROR CODE: Subsc`

	evidence := Extract(Input{Text: raw})[0]
	if !strings.Contains(evidence.Canonical, "detail code SubscriptionRequestsThrottled") {
		t.Fatalf("expected canonical phrase to keep full inner code instead of truncated suffix, got=%q", evidence.Canonical)
	}
	if strings.Contains(evidence.Canonical, "detail code Subsc;") {
		t.Fatalf("expected canonical phrase to exclude truncated inner code suffix, got=%q", evidence.Canonical)
	}
}

func TestExtractEvidenceUsesRootAzureMessageWhenNoInnerCode(t *testing.T) {
	t.Parallel()

	raw := `ERROR CODE: InternalServerError
{
  "id": "/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/providers/Microsoft.RedHatOpenShift/locations/eastus2euap/hcpOperationStatuses/b4b3429d-a139-425f-b110-f68346a4fe8b",
  "error": {
    "code": "InternalServerError",
    "message": "insufficient public IP address quota: required 2, available 0"
  }
}`

	evidence := Extract(Input{Text: raw})[0]
	if !strings.Contains(evidence.Canonical, "ERROR CODE: InternalServerError") {
		t.Fatalf("expected canonical phrase to keep root code, got=%q", evidence.Canonical)
	}
	if !strings.Contains(strings.ToLower(evidence.Canonical), "detail message insufficient public ip address quota: required <count>, available <count>") {
		t.Fatalf("expected canonical phrase to include normalized root Azure quota message, got=%q", evidence.Canonical)
	}
}

func TestExtractEvidenceNormalizesQuotaCountVariants(t *testing.T) {
	t.Parallel()

	rawA := `ERROR CODE: InternalServerError
{
  "error": {
    "code": "InternalServerError",
    "message": "insufficient public IP address quota: required 2, available 0"
  }
}`
	rawB := `ERROR CODE: InternalServerError
{
  "error": {
    "code": "InternalServerError",
    "message": "insufficient public IP address quota: required 2, available 1"
  }
}`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("expected quota count variants to normalize to same canonical phrase: gotA=%q gotB=%q", gotA, gotB)
	}
	if !strings.Contains(strings.ToLower(gotA), "required <count>, available <count>") {
		t.Fatalf("expected normalized quota count placeholders in canonical phrase, got=%q", gotA)
	}
}

func TestExtractEvidenceErrorCodePreferredOverResponse404(t *testing.T) {
	t.Parallel()

	raw := `PUT https://management.example.test/subscriptions/XXXX/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id/federatedIdentityCredentials/fic
--------------------------------------------------------------------------------
RESPONSE 404: 404 Not Found
ERROR CODE: NotFound
--------------------------------------------------------------------------------
{
  "error": {
    "code": "NotFound",
    "message": "MS Graph resource not found during Federated Identity Credential creation."
  }
}`

	got := Extract(Input{Text: raw})[0].Canonical
	if strings.EqualFold(strings.TrimSpace(got), "RESPONSE 404: 404 Not Found") {
		t.Fatalf("ERROR CODE must be preferred over RESPONSE status line, got=%q", got)
	}
	if !strings.Contains(strings.ToLower(got), "notfound") {
		t.Fatalf("expected NotFound error code in canonical phrase, got=%q", got)
	}
}

func TestExtractEvidenceInternalServerErrorDetailSuppressed(t *testing.T) {
	t.Parallel()

	rawWithDetail := `POST https://management.example.test/subscriptions/XXXX/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/requestAdminCredential
RESPONSE 500: 500 Internal Server Error
ERROR CODE: InternalServerError
{
  "error": {
    "code": "InternalServerError",
    "message": "Internal server error."
  }
}`
	rawWithout := `GET https://rp.example/subscriptions/XXXX/providers/Microsoft.RedHatOpenShift/locations/westus3/hcpOperationStatuses/abc123
RESPONSE 200: 200 OK
ERROR CODE: InternalServerError`

	gotWith := Extract(Input{Text: rawWithDetail})[0].Canonical
	gotWithout := Extract(Input{Text: rawWithout})[0].Canonical
	if gotWith != gotWithout {
		t.Fatalf("InternalServerError with and without generic detail must canonicalize identically:\n  with=%q\n  without=%q", gotWith, gotWithout)
	}
}

func TestExtractEvidenceErrorCodeIncludesProvider(t *testing.T) {
	t.Parallel()

	raw := `GET https://management.example.test/subscriptions/XXXX/providers/Microsoft.Network/virtualNetworks
RESPONSE 429: 429 Too Many Requests
ERROR CODE: ResourceCollectionRequestsThrottled`

	got := Extract(Input{Text: raw})[0].Canonical
	if !strings.Contains(strings.ToLower(got), "provider microsoft.network") {
		t.Fatalf("expected provider Microsoft.Network in canonical phrase, got=%q", got)
	}
	if !strings.Contains(got, "ERROR CODE: ResourceCollectionRequestsThrottled") {
		t.Fatalf("expected error code preserved in canonical phrase, got=%q", got)
	}
}

func TestExtractEvidenceRedHatOpenShiftProviderIncluded(t *testing.T) {
	t.Parallel()

	raw := `GET https://rp.example/subscriptions/XXXX/providers/Microsoft.RedHatOpenShift/locations/westus3/hcpOperationStatuses/abc
RESPONSE 500: 500 Internal Server Error
ERROR CODE: InternalServerError`

	got := Extract(Input{Text: raw})[0].Canonical
	if !strings.Contains(strings.ToLower(got), "provider microsoft.redhatopenshift") {
		t.Fatalf("expected provider Microsoft.RedHatOpenShift in canonical phrase, got=%q", got)
	}
}

func TestExtractEvidenceMergesAzureManagedClusterResourceNotFoundVariants(t *testing.T) {
	t.Parallel()

	rawA := `time=2026-04-30T19:15:08.598Z level=ERROR msg="Step errored." serviceGroup=Microsoft.Azure.ARO.HCP.Management.Infra resourceGroup=management step=svc-mgmt-permissions err="failed to run ARM step: failed to poll deployment: failed to wait for deployment completion
ERROR CODE: DeploymentFailed
{ "error": { "code": "DeploymentFailed", "details": [ { "code": "ResourceNotFound", "message": "The Resource 'Microsoft.ContainerService/managedClusters/prow-j1425280-mgmt-1' under resource group 'hcp-underlay-prow-j1425280-mgmt-1' was not found." } ] } }"`
	rawB := `time=2026-05-04T07:35:50.067Z level=ERROR msg="Step errored." serviceGroup=Microsoft.Azure.ARO.HCP.Management.Infra resourceGroup=management step=svc-mgmt-permissions err="failed to run ARM step: failed to poll deployment: failed to wait for deployment completion
ERROR CODE: DeploymentFailed
{ "error": { "code": "DeploymentFailed", "details": [ { "code": "ResourceNotFound", "message": "The Resource 'Microsoft.ContainerService/managedClusters/prow-j7955840-mgmt-1' under resource group 'hcp-underlay-prow-j7955840-mgmt-1' was not found." } ] } }"`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical

	if gotA != gotB {
		t.Fatalf("expected generated managed-cluster names to normalize to the same canonical phrase:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if !strings.Contains(gotA, "'Microsoft.ContainerService/managedClusters/<resource>'") {
		t.Fatalf("expected managed-cluster resource path placeholder, got=%q", gotA)
	}
	if !strings.Contains(gotA, "resource group '<resource-group>'") {
		t.Fatalf("expected quoted resource-group placeholder, got=%q", gotA)
	}
}

func TestExtractEvidenceMergesVaultAlreadyExistsAcrossGeneratedNames(t *testing.T) {
	t.Parallel()

	rawA := `fail [github.com/Azure/ARO-HCP/test/e2e/nodepool_version_upgrade.go:579]: failed to create cluster customer resources
Unexpected error:
    <*fmt.wrapError | 0xc0008a3560>: 
    failed to create customer-infra: failed waiting for deployment "customer-infra-np-downgrade-xrk9p4-fvbtlh" in resourcegroup="rg-np-downgrade-xrk9p4-7gbnwz" to finish: GET https://management.example.test/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourcegroups/rg-np-downgrade-xrk9p4-7gbnwz/providers/Microsoft.Resources/deployments/customer-infra-np-downgrade-xrk9p4-fvbtlh/operationStatuses/08584175744362285058
    --------------------------------------------------------------------------------
    RESPONSE 200: 200 OK
    ERROR CODE: DeploymentFailed
    --------------------------------------------------------------------------------
    {
      "status": "Failed",
      "error": {
        "code": "DeploymentFailed",
        "message": "At least one resource deployment operation failed. Please list deployment operations for details. Please see https://aka.ms/arm-deployment-operations for usage details.",
        "details": [
          {
            "code": "Conflict",
            "message": "{\r\n  \"error\": {\r\n    \"code\": \"VaultAlreadyExists\",\r\n    \"message\": \"The vault name 'cust-kv-7rz373fqcqw3a' is already in use. Vault names are globally unique so it is possible that the name is already taken.\"\r\n  }\r\n}"
          }
        ]
      }
    }
    --------------------------------------------------------------------------------
    
    ...
occurred`
	rawB := `fail [github.com/Azure/ARO-HCP/test/e2e/control_plane_automated_z_stream_upgrade.go:92]: failed to create customer resources for z-stream cluster "cluster-zstream-4-20-lmtsfr"
Unexpected error:
    <*fmt.wrapError | 0xc000d10f60>: 
    failed to create customer-infra: failed waiting for deployment "customer-infra-cluster-zstream-4-20-lmtsfr-7g6lz7" in resourcegroup="rg-zstream-upgrade-4-20-g9whvx" to finish: GET https://management.example.test/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourcegroups/rg-zstream-upgrade-4-20-g9whvx/providers/Microsoft.Resources/deployments/customer-infra-cluster-zstream-4-20-lmtsfr-7g6lz7/operationStatuses/08584175816013259434
    --------------------------------------------------------------------------------
    RESPONSE 200: 200 OK
    ERROR CODE: DeploymentFailed
    --------------------------------------------------------------------------------
    {
      "status": "Failed",
      "error": {
        "code": "DeploymentFailed",
        "message": "At least one resource deployment operation failed. Please list deployment operations for details. Please see https://aka.ms/arm-deployment-operations for usage details.",
        "details": [
          {
            "code": "Conflict",
            "message": "{\r\n  \"error\": {\r\n    \"code\": \"VaultAlreadyExists\",\r\n    \"message\": \"The vault name 'cust-kv-chfzla5wndhwk' is already in use. Vault names are globally unique so it is possible that the name is already taken.\"\r\n  }\r\n}"
          }
        ]
      }
    }
    --------------------------------------------------------------------------------
    
    ...
occurred`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical

	if gotA != gotB {
		t.Fatalf("expected generated vault names to normalize to the same canonical phrase:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if !strings.Contains(strings.ToLower(gotA), "'<vault-name>' is already in use") {
		t.Fatalf("expected vault-name placeholder in canonical phrase, got=%q", gotA)
	}
}

func TestExtractEvidenceNormalizesResourceGroupsPathSegmentInResourceID(t *testing.T) {
	t.Parallel()

	rawA := `fail [github.com/Azure/ARO-HCP/test/e2e/oidc_issuer_workload_identity.go:296]: failed to create federated identity credential for service account system:serviceaccount:e2e-oidc-wi:wi-test-sa
Unexpected error:
    <*exported.ResponseError | 0xc002600150>: 
    PUT https://management.example.test/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourceGroups/oidc-wi-2jj8pc/providers/Microsoft.ManagedIdentity/userAssignedIdentities/e2e-oidc-wi-test/federatedIdentityCredentials/e2e-wi-fic
    --------------------------------------------------------------------------------
    RESPONSE 404: 404 Not Found
    ERROR CODE: NotFound
    --------------------------------------------------------------------------------
    {
      "error": {
        "code": "NotFound",
        "message": "Resource '/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourcegroups/oidc-wi-2jj8pc/providers/Microsoft.ManagedIdentity/userAssignedIdentities/e2e-oidc-wi-test' was not found."
      }
    }
    --------------------------------------------------------------------------------
    
    ...
occurred`
	rawB := `fail [github.com/Azure/ARO-HCP/test/e2e/oidc_issuer_workload_identity.go:296]: failed to create federated identity credential for service account system:serviceaccount:e2e-oidc-wi:wi-test-sa
Unexpected error:
    <*exported.ResponseError | 0xc0029906c0>: 
    PUT https://management.example.test/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourceGroups/oidc-wi-67rdmn/providers/Microsoft.ManagedIdentity/userAssignedIdentities/e2e-oidc-wi-test/federatedIdentityCredentials/e2e-wi-fic
    --------------------------------------------------------------------------------
    RESPONSE 404: 404 Not Found
    ERROR CODE: NotFound
    --------------------------------------------------------------------------------
    {
      "error": {
        "code": "NotFound",
        "message": "Resource '/subscriptions/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX/resourcegroups/oidc-wi-67rdmn/providers/Microsoft.ManagedIdentity/userAssignedIdentities/e2e-oidc-wi-test' was not found."
      }
    }
    --------------------------------------------------------------------------------
    
    ...
occurred`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical

	if gotA != gotB {
		t.Fatalf("expected generated resource-group names embedded in a resource ID path to normalize to the same canonical phrase:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if !strings.Contains(gotA, "/resourcegroups/<resource-group>/") {
		t.Fatalf("expected resourcegroups path-segment placeholder, got=%q", gotA)
	}
}

func TestExtractEvidenceSurfacesClusterServiceNoMessagePlaceholder(t *testing.T) {
	t.Parallel()

	// ClusterService returns this exact JSON error body when it fails but
	// provides no further detail. Go's default json.Marshal HTML-escapes
	// '<'/'>' as \u003c/\u003e, so real payloads carry that literal escaping.
	rawSingleLeaf := `fail [github.com/Azure/ARO-HCP/test/e2e/nodepool_autoscaling.go:101]: failed to create HCP cluster np-autoscale-cluster
Unexpected error:
    <*fmt.wrapError | 0xc0015ba420>: 
    failed to create HCP cluster np-autoscale-cluster: failed waiting for cluster="np-autoscale-cluster" in resourcegroup="np-autoscaling-zkzp62mtgcxk" to finish creating: GET https://rp.example.hcpsvc.osadev.cloud/subscriptions/XXXX/providers/Microsoft.RedHatOpenShift/locations/westus3/hcpOperationStatuses/32fedae4-2ad1-481c-9c94-83f625471ccf
    --------------------------------------------------------------------------------
    RESPONSE 200: 200 OK
    ERROR CODE: InternalServerError
    --------------------------------------------------------------------------------
    {
      "id": "/subscriptions/XXXX/providers/Microsoft.RedHatOpenShift/locations/westus3/hcpOperationStatuses/32fedae4-2ad1-481c-9c94-83f625471ccf",
      "name": "32fedae4-2ad1-481c-9c94-83f625471ccf",
      "status": "Failed",
      "error": {
        "code": "InternalServerError",
        "message": "[clusterServiceClusterStatus] \u003cno_message\u003e"
      }
    }
    --------------------------------------------------------------------------------
occurred`

	got := Extract(Input{Text: rawSingleLeaf})[0].Canonical
	loweredGot := strings.ToLower(got)

	if strings.Contains(loweredGot, `\u003c`) {
		t.Fatalf("expected \\u003c/\\u003e HTML-escapes to be decoded to real angle brackets, got=%q", got)
	}
	if !strings.Contains(loweredGot, "<no_message>") {
		t.Fatalf("expected ClusterService's <no_message> placeholder to be surfaced in the canonical phrase (so it's evident the upstream service, not extraction, lacks detail), got=%q", got)
	}
	if !strings.Contains(loweredGot, "clusterserviceclusterstatus") {
		t.Fatalf("expected the leaf tag preceding <no_message> to be preserved, got=%q", got)
	}
}

func TestExtractEvidenceSurfacesResourceNotFoundDetailMessage(t *testing.T) {
	t.Parallel()

	// "ResourceNotFound" (the code RP actually returns) was missing from the
	// generic-code allowlist that gates detail-message extraction, so any
	// detail (here, the informative "under resource group ''" hint) was
	// silently dropped in favor of a bare "ERROR CODE: ResourceNotFound".
	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/external_auth_list_and_verify.go:86]: failed to create HCP cluster for external auth list test
Unexpected error:
    <*fmt.wrapError | 0xc000669440>: 
    failed to create HCP cluster ea-listsjvd5r: failed waiting for cluster="ea-listsjvd5r" in resourcegroup="ea-list-47q6dt" to finish creating: GET https://rp.example.hcpsvc.osadev.cloud/subscriptions/XXXX/providers/Microsoft.RedHatOpenShift/locations/westus3/hcpOperationStatuses/f5e67f73-1a1b-4a43-b367-a1a98b42834d
    --------------------------------------------------------------------------------
    RESPONSE 404: 404 Not Found
    ERROR CODE: ResourceNotFound
    --------------------------------------------------------------------------------
    {
      "error": {
        "code": "ResourceNotFound",
        "message": "The resource 'locations/hcpOperationStatuses/f5e67f73-1a1b-4a43-b367-a1a98b42834d' under resource group '' was not found.",
        "target": "/subscriptions/XXXX/providers/Microsoft.RedHatOpenShift/locations/westus3/hcpOperationStatuses/f5e67f73-1a1b-4a43-b367-a1a98b42834d"
      }
    }
    --------------------------------------------------------------------------------
occurred`

	got := Extract(Input{Text: raw})[0].Canonical
	if !strings.Contains(got, "detail message") || !strings.Contains(got, "under resource group") {
		t.Fatalf("expected ResourceNotFound's detail message to be surfaced instead of dropped, got=%q", got)
	}
}

func TestExtractEvidenceAlwaysSurfacesDetailForRedHatOpenShiftProviderUnknownCode(t *testing.T) {
	t.Parallel()

	// "QuotaExceeded" is deliberately NOT in genericCodes, simulating a
	// brand-new RP error code we haven't special-cased yet. Because
	// Microsoft.RedHatOpenShift is the provider under test, its detail
	// message must always be surfaced so future/unseen failure patterns are
	// complete without requiring another allowlist patch.
	raw := `PUT https://management.example.test/subscriptions/XXXX/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster
RESPONSE 429: 429 Too Many Requests
ERROR CODE: QuotaExceeded
{
  "error": {
    "code": "QuotaExceeded",
    "message": "Subscription quota for standardDSv5Family cores has been exceeded, requested 32, available 8"
  }
}`
	got := Extract(Input{Text: raw})[0].Canonical
	if !strings.Contains(got, "detail message") || !strings.Contains(got, "quota") {
		t.Fatalf("expected an unrecognized RedHatOpenShift error code to still surface its detail message, got=%q", got)
	}

	// The same unrecognized code for an unrelated provider must keep the
	// existing conservative (no-detail) behavior.
	rawOtherProvider := `PUT https://management.example.test/subscriptions/XXXX/providers/Microsoft.Storage/storageAccounts/foo
RESPONSE 429: 429 Too Many Requests
ERROR CODE: QuotaExceeded
{
  "error": {
    "code": "QuotaExceeded",
    "message": "Subscription quota for storage accounts has been exceeded"
  }
}`
	gotOtherProvider := Extract(Input{Text: rawOtherProvider})[0].Canonical
	if strings.Contains(gotOtherProvider, "detail message") {
		t.Fatalf("expected non-RedHatOpenShift providers to keep the existing allowlist-gated behavior, got=%q", gotOtherProvider)
	}
}

func TestExtractEvidenceSurfacesInvalidTemplateMessage(t *testing.T) {
	t.Parallel()

	rawA := `PUT https://management.example.test/subscriptions/XXXX/providers/Microsoft.Resources/deployments/example
RESPONSE 400: 400 Bad Request
ERROR CODE: InvalidTemplate
{
  "error": {
    "code": "InvalidTemplate",
    "message": "Deployment template validation failed: 'The value for the template parameter 'integrationSubnetName' at line '1' and column '2857' is not provided.'"
  }
}`
	rawB := strings.Replace(rawA, "column '2857'", "column '2942'", 1)

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("expected template source positions to normalize identically:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if !strings.Contains(gotA, "detail message") || !strings.Contains(gotA, "integrationSubnetName") {
		t.Fatalf("expected InvalidTemplate detail to be surfaced, got=%q", gotA)
	}
}

func TestExtractEvidenceSurfacesAzureIdentityRootCause(t *testing.T) {
	t.Parallel()

	raw := `GET https://rp.example/subscriptions/XXXX/providers/Microsoft.RedHatOpenShift/locations/westus3/hcpOperationStatuses/id
ERROR CODE: InternalServerError
{
  "error": {
    "code": "InternalServerError",
    "message": "[clusterServiceNodePoolStatus] ClientCertificateCredential authentication failed. \nPOST https://login.microsoftonline.com/tenant/oauth2/v2.0/token\n{\n  \"error\": \"invalid_client\",\n  \"error_description\": \"AADSTS7000213: Invalid certificate chain. Trace ID: volatile Correlation ID: volatile Timestamp: 2026-07-31\"\n}"
  }
}`

	got := Extract(Input{Text: raw})[0].Canonical
	if !strings.Contains(got, "ClientCertificateCredential authentication failed") ||
		!strings.Contains(got, "AADSTS7000213: Invalid certificate chain.") {
		t.Fatalf("expected RP certificate-auth canonical to include the stable AADSTS root cause, got=%q", got)
	}
	if strings.Contains(got, "Trace ID") || strings.Contains(got, "Timestamp") {
		t.Fatalf("expected volatile identity diagnostics to be removed, got=%q", got)
	}
}

func TestExtractEvidenceUsesAssertionContextForDetailLessAzureError(t *testing.T) {
	t.Parallel()

	raw := `fail [github.com/Azure/ARO-HCP/test/e2e/admin_api.go:339]: failed to retrieve serial console logs for VM "c9e6l7e5k5c8l7z-worker-vqgww-tm445"
Unexpected error:
    expected status 200 OK, got 500: {
        "error": {
            "code": "InternalServerError",
            "message": "Internal server error."
        }
    }
occurred`

	got := Extract(Input{Text: raw})[0].Canonical
	if !strings.Contains(got, "ERROR CODE: InternalServerError") ||
		!strings.Contains(got, "context failed to retrieve serial console logs") {
		t.Fatalf("expected the failing operation to qualify a detail-less Azure error, got=%q", got)
	}
	if strings.Contains(got, "c9e6l7e5k5c8l7z-worker-vqgww-tm445") {
		t.Fatalf("expected the generated VM name to be normalized, got=%q", got)
	}
}

func TestExtractEvidenceSummarizesClusterServiceDeletionDetails(t *testing.T) {
	t.Parallel()

	rawA := `GET https://rp.example/subscriptions/XXXX/providers/Microsoft.RedHatOpenShift/locations/westus3/hcpOperationStatuses/id
ERROR CODE: InternalServerError
{
  "error": {
    "code": "InternalServerError",
    "message": "cluster deletion did not complete before the deadline; [clusterServiceDeletion] ClusterService cluster /api/aro_hcp/v1alpha1/clusters/2rvfr07shlm141ejua6jgajchroj0t6r still exists (deletion dispatched at 2026-08-05T06:39:16Z); [clusterServiceStatus] ClusterService state is \"uninstalling\"; [descendantResources] remaining resources: 1 Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools, 1 microsoft.redhatopenshift/hcpopenshiftclusters/nodepools/serviceProviderNodePools, 1 microsoft.redhatopenshift/hcpopenshiftclusters/serviceProviderClusters"
  }
}`
	rawB := strings.ReplaceAll(rawA, "2rvfr07shlm141ejua6jgajchroj0t6r", "2s08kfkgmp6p466kbsrs48lm52bok333")
	rawB = strings.ReplaceAll(rawB, "2026-08-05T06:39:16Z", "2026-08-05T07:24:38Z")

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("expected cluster IDs and deletion timestamps to merge:\n  A=%q\n  B=%q", gotA, gotB)
	}
	for _, unwanted := range []string{"2rvfr07shlm141ejua6jgajchroj0t6r", "2s08kfkgmp6p466kbsrs48lm52bok333", "2026-08-05", "/api/aro_hcp/"} {
		if strings.Contains(gotA, unwanted) {
			t.Fatalf("expected transient ClusterService identity to be removed, got=%q", gotA)
		}
	}
	for _, detail := range []string{`state is "uninstalling"`, "nodePools", "serviceProviderNodePools", "serviceProviderClusters"} {
		if !strings.Contains(gotA, detail) {
			t.Fatalf("expected complete deletion detail %q, got=%q", detail, gotA)
		}
	}
	if strings.HasSuffix(gotA, "still") {
		t.Fatalf("expected a complete canonical sentence rather than a mid-clause truncation, got=%q", gotA)
	}
}

func TestExtractEvidenceKeepsClusterServiceDeletionVariantsDistinct(t *testing.T) {
	t.Parallel()

	base := `GET https://rp.example/subscriptions/XXXX/providers/Microsoft.RedHatOpenShift/locations/westus3/hcpOperationStatuses/id
ERROR CODE: InternalServerError
{"error":{"code":"InternalServerError","message":"%s"}}`
	withoutHostedCluster := `cluster deletion did not complete before the deadline; [clusterServiceDeletion] ClusterService cluster /api/aro_hcp/v1alpha1/clusters/2rvfr07shlm141ejua6jgajchroj0t6r still exists (deletion dispatched at 2026-08-05T06:39:16Z); [clusterServiceStatus] ClusterService state is "uninstalling"; [descendantResources] remaining resources: 1 microsoft.redhatopenshift/hcpopenshiftclusters/serviceProviderClusters`
	withHostedCluster := `cluster deletion did not complete before the deadline; [clusterServiceDeletion] ClusterService cluster /api/aro_hcp/v1alpha1/clusters/2s094mnvr386tf3h8e6ohtguvd9b65to still exists (deletion dispatched at 2026-08-05T07:24:38Z); [clusterServiceStatus] ClusterService state is "uninstalling"; [hostedCluster] HostedCluster still exists; [descendantResources] remaining resources: 1 microsoft.redhatopenshift/hcpopenshiftclusters/serviceProviderClusters`

	gotWithout := Extract(Input{Text: fmt.Sprintf(base, withoutHostedCluster)})[0].Canonical
	gotWith := Extract(Input{Text: fmt.Sprintf(base, withHostedCluster)})[0].Canonical
	if gotWithout == gotWith {
		t.Fatalf("expected hosted-cluster presence to remain a semantic merge boundary, got=%q", gotWith)
	}
	if !strings.Contains(gotWith, "[hostedCluster] HostedCluster still exists") {
		t.Fatalf("expected hosted-cluster detail to be retained, got=%q", gotWith)
	}
}

func TestExtractEvidenceNormalizesClusterServiceDeletionResourceCounts(t *testing.T) {
	t.Parallel()

	base := `ERROR CODE: InternalServerError
{"error":{"code":"InternalServerError","message":"cluster deletion did not complete before the deadline; [descendantResources] remaining resources: 1 microsoft.redhatopenshift/hcpopenshiftclusters/serviceProviderClusters; [resourceTeardown] tearing down namespaces (%d)"}}`

	gotOne := Extract(Input{Text: fmt.Sprintf(base, 1)})[0].Canonical
	gotTwo := Extract(Input{Text: fmt.Sprintf(base, 2)})[0].Canonical
	if gotOne != gotTwo {
		t.Fatalf("expected resource teardown counts to merge:\n  one=%q\n  two=%q", gotOne, gotTwo)
	}
	if strings.Contains(gotOne, "(1)") || strings.Contains(gotOne, "(2)") {
		t.Fatalf("expected volatile resource counts to be removed, got=%q", gotOne)
	}
	if !strings.Contains(gotOne, "[resourceTeardown] tearing down namespaces") {
		t.Fatalf("expected resource teardown stage to remain visible, got=%q", gotOne)
	}
}

func TestExtractEvidenceNormalizesAzureOperationMetadata(t *testing.T) {
	t.Parallel()

	rawA := `ERROR CODE: DeploymentFailed
{"error":{"code":"DeploymentFailed","details":[{"code":"OperationNotAllowed","message":"Operation is not allowed because there's an in-progress create node pool operation (operation ID: 81d7fb37-c8d0-453c-9a89-3b0ab2b05ec0) on agent pool userswft1 started on UTC 2026-07-22T15:54:24Z. Please wait for it to finish before starting a new operation."}]}}`
	rawB := strings.ReplaceAll(rawA, "81d7fb37-c8d0-453c-9a89-3b0ab2b05ec0", "92e8ac48-d9e1-564d-ab90-4c1bc3c16fd1")
	rawB = strings.ReplaceAll(rawB, "userswft1", "usersabcd")
	rawB = strings.ReplaceAll(rawB, "2026-07-22T15:54:24Z", "2026-08-05T07:12:01Z")

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("expected operation IDs, agent-pool names, and timestamps to merge:\n  A=%q\n  B=%q", gotA, gotB)
	}
	for _, artifact := range []string{"81d7fb37", "92e8ac48", "userswft1", "usersabcd", "2026-"} {
		if strings.Contains(gotA, artifact) {
			t.Fatalf("expected operation artifact %q to be normalized, got=%q", artifact, gotA)
		}
	}
}

func TestExtractEvidenceNormalizesHostedClusterComponentLists(t *testing.T) {
	t.Parallel()

	rawA := `ERROR CODE: InternalServerError
{"error":{"code":"InternalServerError","message":"[clusterServiceClusterStatus] <no_message>; [hypershiftHostedCluster] hosted cluster is not available: ComponentsNotAvailable: Waiting for components to be available: packageserver, catalog-operator, olm-operator; hosted cluster degraded: UnavailableReplicas: [packageserver deployment has 3 unavailable replicas, catalog-operator deployment has 1 unavailable replicas]"}}`
	rawB := `ERROR CODE: InternalServerError
{"error":{"code":"InternalServerError","message":"[clusterServiceClusterStatus] <no_message>; [hypershiftHostedCluster] hosted cluster is not available: ComponentsNotAvailable: Waiting for components to be available: olm-operator, catalog-operator, packageserver; hosted cluster degraded: UnavailableReplicas: [catalog-operator deployment has 2 unavailable replicas, packageserver deployment has 1 unavailable replicas]"}}`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("expected component ordering and replica counts to merge:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if !strings.Contains(gotA, "ComponentsNotAvailable") ||
		!strings.Contains(gotA, "UnavailableReplicas: [catalog-operator, packageserver]") {
		t.Fatalf("expected compact complete hosted-cluster summary, got=%q", gotA)
	}
	if strings.Contains(gotA, "deployment has") {
		t.Fatalf("expected volatile replica counts to be removed, got=%q", gotA)
	}
}

func TestExtractEvidenceNormalizesHostedClusterVersionAndElapsedTime(t *testing.T) {
	t.Parallel()

	rawA := `ERROR CODE: InternalServerError
{"error":{"code":"InternalServerError","message":"[hypershiftHostedCluster] hosted cluster control plane version not yet completed: version 4.21.33 is Partial (want Completed), started 20m44s ago; hosted cluster degraded: UnavailableReplicas: [router]"}}`
	rawB := `ERROR CODE: InternalServerError
{"error":{"code":"InternalServerError","message":"[hypershiftHostedCluster] hosted cluster control plane version not yet completed: version 5.0.0-rc.2 is Partial (want Completed), started 1h2m3s ago; hosted cluster degraded: UnavailableReplicas: [router]"}}`

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("expected control-plane versions and elapsed times to merge:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if !strings.Contains(gotA, "version <version>") || !strings.Contains(gotA, "started <duration> ago") {
		t.Fatalf("expected normalized version and elapsed-time placeholders, got=%q", gotA)
	}

	differentReason := strings.Replace(rawB, "UnavailableReplicas: [router]", "KubeconfigWaitingForCreate: Waiting for kubeconfig", 1)
	gotDifferentReason := Extract(Input{Text: differentReason})[0].Canonical
	if gotA == gotDifferentReason {
		t.Fatalf("expected distinct hosted-cluster reasons to remain separate, got=%q", gotA)
	}
}

func TestExtractEvidenceMergesHostedClusterUnavailableReplicaLists(t *testing.T) {
	t.Parallel()

	base := `GET https://rp.example/subscriptions/XXXX/providers/Microsoft.RedHatOpenShift/locations/westus3/hcpOperationStatuses/id
ERROR CODE: DeadlineExceeded
{"error":{"code":"DeadlineExceeded","message":%q}}`
	rawA := fmt.Sprintf(base, `cluster creation did not complete before the deadline; [clusterServiceClusterStatus] cluster service is installing; [hypershiftHostedCluster] hosted cluster is not available: KubeconfigWaitingForCreate: Waiting for hosted control plane kubeconfig to be created; hosted cluster degraded: UnavailableReplicas: capi-provider deployment has 2 unavailable replicas`)
	rawB := fmt.Sprintf(base, `cluster creation did not complete before the deadline; [clusterServiceClusterStatus] cluster service is installing; [hypershiftHostedCluster] hosted cluster is not available: KubeconfigWaitingForCreate: Waiting for hosted control plane kubeconfig to be created; hosted cluster degraded: UnavailableReplicas: capi-provider deployment has 1 unavailable replicas`)
	rawRouter := fmt.Sprintf(base, `cluster creation did not complete before the deadline; [clusterServiceClusterStatus] cluster service is installing; [hypershiftHostedCluster] hosted cluster is not available: KubeconfigWaitingForCreate: Waiting for hosted control plane kubeconfig to be created; hosted cluster degraded: UnavailableReplicas: [router deployment has 2 unavailable replicas]`)
	rawVersion := fmt.Sprintf(base, `cluster creation did not complete before the deadline; [hypershiftHostedCluster] hosted cluster control plane version not yet completed: version 4.20.40 is Partial (want Completed), started 1m39s ago; version 4.20.40 is Partial (want Completed), started 4m49s ago; version 4.20.40 is Partial (want Completed), started 10m52s ago; hosted cluster degraded: UnavailableReplicas: [openshift-apiserver]`)

	gotA := Extract(Input{Text: rawA})[0].Canonical
	gotB := Extract(Input{Text: rawB})[0].Canonical
	if gotA != gotB {
		t.Fatalf("expected UnavailableReplicas replica counts to merge:\n  A=%q\n  B=%q", gotA, gotB)
	}
	if !strings.Contains(gotA, "UnavailableReplicas: [capi-provider]") {
		t.Fatalf("expected UnavailableReplicas to keep the affected workload, got=%q", gotA)
	}

	gotRouter := Extract(Input{Text: rawRouter})[0].Canonical
	if gotA == gotRouter {
		t.Fatalf("expected distinct UnavailableReplicas workloads to remain separate:\n  capi=%q\n  router=%q", gotA, gotRouter)
	}
	if !strings.Contains(gotRouter, "UnavailableReplicas: [router]") {
		t.Fatalf("expected router to remain in UnavailableReplicas, got=%q", gotRouter)
	}

	gotVersion := Extract(Input{Text: rawVersion})[0].Canonical
	if strings.Count(strings.ToLower(gotVersion), "is partial (want completed)") != 1 {
		t.Fatalf("expected repeated Partial version clauses to collapse, got=%q", gotVersion)
	}
	if strings.Contains(gotVersion, "4.20.40") {
		t.Fatalf("expected control-plane version to be normalized, got=%q", gotVersion)
	}
}
