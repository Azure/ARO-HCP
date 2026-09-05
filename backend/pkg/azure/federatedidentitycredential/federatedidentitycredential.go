package federatedidentitycredential

import (
	"fmt"

	"github.com/google/uuid"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	// federatedIdentityCredentialNamespaceUuid is the UUID used as the Namespace ID that Clusters Service uses
	// for generating *deterministic* Azure Federated Identity Credential names. The value was generated
	// using an UUIDv4 with the `uuidgen` cli tool and `--random` flag.
	// *WARNING* Do not change this value as changing it changes how the
	// azure federated identity credential names are generated and consumers of the generated names
	// rely on predictability of the generated names based on the given input. It must never
	// change: it is part of the contract that makes the names deterministic across
	// Cluster Service and the backend.
	federatedIdentityCredentialNamespaceUuid = "5a53d59a-a20f-4a1b-9355-c74298eff9a6"
)

// GenerateFederatedIdentityCredentialName generates an Azure Federated Identity Credential
// name based on the provided csClusterID, the name of the data plane operator,
// the service account namespace, and the service account name.
// The generated name isn't a random string, but rather the result of a *deterministic* hash function
// on the parameters. This is, if the function is called several times with the
// same input it will generate the same name. The order of the parameters affects
// the generated name.
// The generated name is a `aro-hcp-<csClusterID>-UUIDv5` where
// UUIDv5 is UUIDv5(federatedIdentityCredentialNamespaceUuid, "{csClusterID}${dataPlaneOperatorName}${serviceAccountNamespace}${serviceAccountName}").
// *WARNING* changing the logic on how the names are generated can break the
// consumers of the function as they rely on deterministic predictable names generation based
// on the given input. Do not change the generation function unless you are
// very aware of the implications and how it impacts already existing resources
// and consumers of it. It must never change: it is part of the contract that
// makes the names deterministic across Cluster Service and the backend.
func GenerateFederatedIdentityCredentialName(csClusterID string, dataPlaneOperatorName string, serviceAccountNamespace string, serviceAccountName string) string {
	input := fmt.Sprintf("%s$%s$%s$%s", csClusterID, dataPlaneOperatorName, serviceAccountNamespace, serviceAccountName)
	namespaceUUID := uuid.MustParse(federatedIdentityCredentialNamespaceUuid)
	generatedUUID := uuid.NewSHA1(namespaceUUID, []byte(input)).String()
	return fmt.Sprintf("aro-hcp-%s-%s", csClusterID, generatedUUID)
}

// GenerateFederatedIdentityCredentialResourceID returns the ARM resource ID of the
// Azure Federated Identity Credential for the given user-assigned identity and
// input. The resource ID is in the format of
// "{userAssignedIdentityResourceID}/federatedIdentityCredentials/{name}"
// where name is the result of GenerateFederatedIdentityCredentialName.
func GenerateFederatedIdentityCredentialResourceID(userAssignedIdentityResourceID *azcorearm.ResourceID, csClusterID string, dataPlaneOperatorName string, serviceAccountNamespace string, serviceAccountName string) (*azcorearm.ResourceID, error) {
	if userAssignedIdentityResourceID == nil {
		return nil, fmt.Errorf("user assigned identity resource ID is nil")
	}
	name := GenerateFederatedIdentityCredentialName(csClusterID, dataPlaneOperatorName, serviceAccountNamespace, serviceAccountName)
	return azcorearm.ParseResourceID(fmt.Sprintf("%s/federatedIdentityCredentials/%s", userAssignedIdentityResourceID.String(), name))
}
