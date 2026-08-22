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

package coreapitesting

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/randfill"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

// FuzzArmResourceID constructs a lexically valid *azcorearm.ResourceID for the
// given ARM resource type (e.g. "Microsoft.Network/virtualNetworks/subnets").
// genName is called once per path segment that needs a random name (resource
// group plus one per resource-type level). All path components are lowercased
// so the result survives case-normalizing round-trips.
func FuzzArmResourceID(resourceType string, genName func() string) *azcorearm.ResourceID {
	idx := strings.IndexByte(resourceType, '/')
	if idx < 0 {
		panic("FuzzArmResourceID: resource type must contain a provider separator '/'")
	}
	provider := resourceType[:idx]
	typeSegments := strings.Split(resourceType[idx+1:], "/")

	sub := uuid.New().String()
	rg := sanitizeFuzzName(genName())

	var b strings.Builder
	b.WriteString("/subscriptions/")
	b.WriteString(sub)
	b.WriteString("/resourcegroups/")
	b.WriteString(rg)
	b.WriteString("/providers/")
	b.WriteString(strings.ToLower(provider))
	for _, seg := range typeSegments {
		b.WriteByte('/')
		b.WriteString(strings.ToLower(seg))
		b.WriteByte('/')
		b.WriteString(sanitizeFuzzName(genName()))
	}

	return metadataapi.Must(azcorearm.ParseResourceID(b.String()))
}

// FuzzInternalID constructs a lexically valid InternalID for a Cluster Service
// cluster resource with a random name component.
func FuzzInternalID(genName func() string) metadataapi.InternalID {
	return metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/" + sanitizeFuzzName(genName())))
}

func sanitizeFuzzName(s string) string {
	h := hex.EncodeToString([]byte(s))
	if h == "" {
		return "fuzz"
	}
	return h
}

// GenName returns a name-generator closure backed by the given randfill.Continue.
func GenName(c randfill.Continue) func() string {
	return func() string { return c.String(10) }
}

// FuzzerFor creates a randfill.Filler with NilChance=.5, NumElements=0,1
// suitable for round-trip conversion tests.
func FuzzerFor(funcs []interface{}, src rand.Source) *randfill.Filler {
	f := randfill.New().NilChance(.5).NumElements(0, 1)
	if src != nil {
		f.RandSource(src)
	}
	f.Funcs(funcs...)
	return f
}

// DeepCopyFuzzerFor creates a randfill.Filler with NilChance=.5, NumElements=1,3
// pre-loaded with CommonDeepCopyFuzzFuncs.
func DeepCopyFuzzerFor(src rand.Source) *randfill.Filler {
	f := randfill.New().NilChance(.5).NumElements(1, 3)
	if src != nil {
		f.RandSource(src)
	}
	f.Funcs(CommonDeepCopyFuzzFuncs()...)
	return f
}

// DoDeepCopyTest verifies that DeepCopyObject produces an independent copy.
// It encodes the original to JSON, then re-fuzzes the copy. If the copy and
// original share any references, re-fuzzing the copy would mutate the original,
// and the JSON encoding would differ.
func DoDeepCopyTest(t *testing.T, original runtime.Object, fuzzer *randfill.Filler) {
	t.Helper()

	prefuzz, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal original: %v", err)
	}

	copied := original.DeepCopyObject()

	copiedJSON, err := json.Marshal(copied)
	if err != nil {
		t.Fatalf("failed to marshal copy: %v", err)
	}
	if !bytes.Equal(prefuzz, copiedJSON) {
		t.Errorf("DeepCopy did not preserve data:\n%s", cmp.Diff(string(prefuzz), string(copiedJSON)))
	}

	fuzzer.Fill(copied)

	postfuzz, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal original after fuzzing copy: %v", err)
	}
	if !bytes.Equal(prefuzz, postfuzz) {
		t.Errorf("fuzzing copy modified original:\nbefore: %s\nafter:  %s", string(prefuzz), string(postfuzz))
	}
}

// CommonRoundTripFuzzFuncs returns fuzz funcs shared by all API version
// round-trip conversion tests. It builds on CommonDeepCopyFuzzFuncs (which
// generates valid data for every field) and appends overrides that zero
// internal-only fields that do not survive the external API round-trip.
// Version-specific overrides can be appended after this slice; randfill
// uses the last-registered func for a type.
func CommonRoundTripFuzzFuncs() []interface{} {
	return append(CommonDeepCopyFuzzFuncs(),
		// Override: round-trip always generates a value (no 5% nil chance).
		func(j *azcorearm.ResourceID, c randfill.Continue) {
			*j = *FuzzArmResourceID("Microsoft.RedHatOpenShift/hcpOpenShiftClusters", GenName(c))
		},
		func(j *coreapi.CosmosMetadata, c randfill.Continue) {
			c.FillNoCustom(j)
			// ResourceID is synchronized with coreapi.Resource.ID after Fill.
			j.ResourceID = nil
			j.ExistingCosmosUID = ""
			j.CosmosETag = ""
			j.InstanceVersion = 0
			j.PartitionKey = ""
		},
		func(j *coreapi.ImageDigestMirror, c randfill.Continue) {
			c.FillNoCustom(j)
			j.MirrorSourcePolicy = metadataapi.MirrorSourcePolicyAllowContactingSource
		},
		func(j *coreapi.HCPOpenShiftClusterStatus, c randfill.Continue) {
			*j = coreapi.HCPOpenShiftClusterStatus{}
		},
		func(j *coreapi.HCPOpenShiftClusterNodePoolStatus, c randfill.Continue) {
			*j = coreapi.HCPOpenShiftClusterNodePoolStatus{}
		},
		func(j *coreapi.HCPOpenShiftClusterExternalAuthStatus, c randfill.Continue) {
			*j = coreapi.HCPOpenShiftClusterExternalAuthStatus{}
		},
		// Override: zero internal-only fields instead of populating them.
		func(j *coreapi.HCPOpenShiftClusterServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			j.ActiveOperationID = ""
			j.RevokeCredentialsOperationID = ""
			j.PendingClusterServiceID = nil
			j.ClusterServiceID = nil
			j.ExperimentalFeatures = coreapi.ExperimentalFeatures{}
			j.ManagedIdentitiesDataPlaneIdentityURL = ""
			j.ClusterUID = ""
			j.BillingDocumentCosmosID = ""
			j.UsesNewClusterDeletionApproach = false
			j.DeleteOperationCompletionTimeout = nil
			j.DeleteOperationCompletionDeadline = nil
		},
		func(j *coreapi.HCPOpenShiftClusterNodePoolServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			j.ActiveOperationID = ""
			j.ClusterServiceID = nil
			j.UsesNewNodePoolDeletionApproach = false
		},
		func(j *coreapi.HCPOpenShiftClusterExternalAuthServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			j.ActiveOperationID = ""
			j.ClusterServiceID = nil
			j.UsesNewExternalAuthDeletionApproach = false
		},
		func(j *coreapi.CustomerManagedEncryptionProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			if j.Kms != nil && j.Kms.ActiveKey.Name == "" && j.Kms.ActiveKey.Version == "" {
				j.Kms = nil
			}
			if j.Kms != nil {
				if j.Kms.Visibility == "" {
					j.Kms.Visibility = metadataapi.KeyVaultVisibilityPublic
				}
				// Use alphanumeric values for KMS key fields so they
				// survive URL construction/parsing round-trips in
				// v20260901preview.
				j.Kms.ActiveKey.VaultName = fmt.Sprintf("vault%d", c.Int31())
				j.Kms.ActiveKey.Name = fmt.Sprintf("key%d", c.Int31())
				j.Kms.ActiveKey.Version = fmt.Sprintf("ver%d", c.Int31())
				// Construct a consistent KeyEncryptionKeyURL from the
				// cleaned ActiveKey fields so it round-trips correctly.
				j.Kms.KeyEncryptionKeyURL = fmt.Sprintf("https://%s.vault.azure.net/keys/%s/%s",
					j.Kms.ActiveKey.VaultName, j.Kms.ActiveKey.Name, j.Kms.ActiveKey.Version)
			}
		},
	)
}

// CommonDeepCopyFuzzFuncs returns fuzz funcs for deep-copy tests.
func CommonDeepCopyFuzzFuncs() []interface{} {
	return []interface{}{
		func(j *azcorearm.ResourceID, c randfill.Continue) {
			if c.Intn(100) < 5 {
				return
			}
			*j = *FuzzArmResourceID("Microsoft.RedHatOpenShift/hcpOpenShiftClusters", GenName(c))
		},
		func(j *metadataapi.InternalID, c randfill.Continue) {
			*j = FuzzInternalID(GenName(c))
		},
		func(j *coreapi.Resource, c randfill.Continue) {
			c.FillNoCustom(j)
			j.ID = FuzzArmResourceID("Microsoft.RedHatOpenShift/hcpOpenShiftClusters", GenName(c))
			j.Name = j.ID.Name
			j.Type = j.ID.ResourceType.String()
		},
		func(j *coreapi.CustomerPlatformProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			if j.SubnetID != nil {
				j.SubnetID = FuzzArmResourceID("Microsoft.Network/virtualNetworks/subnets", GenName(c))
			}
			if j.VnetIntegrationSubnetID != nil {
				j.VnetIntegrationSubnetID = FuzzArmResourceID("Microsoft.Network/virtualNetworks/subnets", GenName(c))
			}
			if j.NetworkSecurityGroupID != nil {
				j.NetworkSecurityGroupID = FuzzArmResourceID("Microsoft.Network/networkSecurityGroups", GenName(c))
			}
		},
		func(j *coreapi.UserAssignedIdentitiesProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			for k := range j.ControlPlaneOperators {
				j.ControlPlaneOperators[k] = FuzzArmResourceID("Microsoft.ManagedIdentity/userAssignedIdentities", GenName(c))
			}
			for k := range j.DataPlaneOperators {
				j.DataPlaneOperators[k] = FuzzArmResourceID("Microsoft.ManagedIdentity/userAssignedIdentities", GenName(c))
			}
			if j.ServiceManagedIdentity != nil {
				j.ServiceManagedIdentity = FuzzArmResourceID("Microsoft.ManagedIdentity/userAssignedIdentities", GenName(c))
			}
		},
		func(j *coreapi.NodePoolPlatformProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			if j.SubnetID != nil {
				j.SubnetID = FuzzArmResourceID("Microsoft.Network/virtualNetworks/subnets", GenName(c))
			}
		},
		func(j *coreapi.OSDiskProfile, c randfill.Continue) {
			c.FillNoCustom(j)
			if j.EncryptionSetID != nil {
				j.EncryptionSetID = FuzzArmResourceID("Microsoft.Compute/diskEncryptionSets", GenName(c))
			}
		},
		func(j *coreapi.HCPOpenShiftClusterServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			if j == nil {
				return
			}
			j.ClusterServiceID = metadataapi.Ptr(FuzzInternalID(GenName(c)))
			j.PendingClusterServiceID = metadataapi.Ptr(FuzzInternalID(GenName(c)))
		},
		func(j *coreapi.HCPOpenShiftClusterNodePoolServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			if j == nil {
				return
			}
			j.ClusterServiceID = metadataapi.Ptr(FuzzInternalID(GenName(c)))
		},
		func(j *coreapi.HCPOpenShiftClusterExternalAuthServiceProviderProperties, c randfill.Continue) {
			c.FillNoCustom(j)
			if j == nil {
				return
			}
			j.ClusterServiceID = metadataapi.Ptr(FuzzInternalID(GenName(c)))
		},
		func(j *coreapi.Operation, c randfill.Continue) {
			c.FillNoCustom(j)
			if j == nil {
				return
			}
			j.InternalID = FuzzInternalID(GenName(c))
			if j.ExternalID != nil {
				j.ExternalID = FuzzArmResourceID("Microsoft.RedHatOpenShift/hcpOpenShiftClusters", GenName(c))
			}
			if j.OperationID != nil {
				j.OperationID = FuzzArmResourceID("Microsoft.RedHatOpenShift/hcpOpenShiftClusters", GenName(c))
			}
		},
		func(j *coreapi.ManagedServiceIdentity, c randfill.Continue) {
			c.FillNoCustom(j)
		},
	}
}
