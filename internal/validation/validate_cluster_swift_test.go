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

package validation

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestClusterSwiftNetworking(t *testing.T) {
	const subnetPath = "customerProperties.platform.vnetIntegrationSubnetId"
	versions := []metadataapi.APIVersion{
		metadataapi.APIVersionV20240610Preview,
		metadataapi.APIVersionV20251223Preview,
		metadataapi.APIVersionV20260630Preview,
		metadataapi.APIVersionV20260901Preview,
		metadataapi.APIVersionV20261001Preview,
	}
	tests := []struct {
		name           string
		update         bool
		enrolled       bool
		tag            *string
		mixedCaseKey   bool
		subnet         bool
		oldSubnet      bool
		oldTag         bool
		privateAPI     bool
		privateKMS     bool
		invalidTag     bool
		conflictingTag bool
		immutable      bool
	}{
		{name: "normal create requires subnet"},
		{name: "AFEC alone does not opt in", enrolled: true},
		{name: "tag alone does not opt in", tag: ptr.To("true")},
		{name: "false does not opt in", enrolled: true, tag: ptr.To("false")},
		{name: "enrolled true opts in", enrolled: true, tag: ptr.To("true")},
		{name: "case insensitive key", enrolled: true, tag: ptr.To("true"), mixedCaseKey: true},
		{name: "empty value rejected", enrolled: true, tag: ptr.To(""), invalidTag: true},
		{name: "uppercase value rejected", enrolled: true, tag: ptr.To("TRUE"), invalidTag: true},
		{name: "other value rejected", enrolled: true, tag: ptr.To("yes"), invalidTag: true},
		{name: "whitespace value rejected", enrolled: true, tag: ptr.To(" true "), invalidTag: true},
		{name: "unenrolled invalid tag ignored", tag: ptr.To("yes"), subnet: true},
		{name: "unenrolled empty tag ignored", tag: ptr.To(""), subnet: true},
		{name: "normal SWIFT create", subnet: true},
		{name: "false allows SWIFT", enrolled: true, tag: ptr.To("false"), subnet: true},
		{name: "true conflicts with subnet", enrolled: true, tag: ptr.To("true"), subnet: true, conflictingTag: true},
		{name: "unenrolled true ignored with subnet", tag: ptr.To("true"), subnet: true},
		{name: "private API cannot opt out", enrolled: true, tag: ptr.To("true"), privateAPI: true},
		{name: "private KMS cannot opt out", enrolled: true, tag: ptr.To("true"), privateKMS: true},
		{name: "both private independently require subnet", enrolled: true, tag: ptr.To("true"), privateAPI: true, privateKMS: true},
		{name: "private API without AFEC", privateAPI: true},
		{name: "private KMS without AFEC", privateKMS: true},
		{name: "both private with SWIFT", subnet: true, privateAPI: true, privateKMS: true},
		{name: "legacy nil update", update: true},
		{name: "nil update retains opt in", update: true, enrolled: true, tag: ptr.To("true"), oldTag: true},
		{name: "nil update removes tag", update: true, enrolled: true, oldTag: true},
		{name: "nil update sets false", update: true, enrolled: true, tag: ptr.To("false"), oldTag: true},
		{name: "nil update revokes AFEC", update: true, tag: ptr.To("true"), oldTag: true},
		{name: "nil update removes tag and revokes AFEC", update: true, oldTag: true},
		{name: "nil update invalid tag rejected", update: true, enrolled: true, tag: ptr.To("TRUE"), invalidTag: true},
		{name: "nil update invalid tag ignored without AFEC", update: true, tag: ptr.To("TRUE")},
		{name: "SWIFT update adds true", update: true, enrolled: true, tag: ptr.To("true"), subnet: true, oldSubnet: true, conflictingTag: true},
		{name: "SWIFT update adds false", update: true, enrolled: true, tag: ptr.To("false"), subnet: true, oldSubnet: true},
		{name: "SWIFT update ignores unenrolled true", update: true, tag: ptr.To("true"), subnet: true, oldSubnet: true},
		{name: "cannot remove subnet with opt in", update: true, enrolled: true, tag: ptr.To("true"), oldSubnet: true, immutable: true},
		{name: "cannot remove subnet without opt in", update: true, oldSubnet: true, immutable: true},
		{name: "cannot add subnet after tag removal", update: true, enrolled: true, oldTag: true, subnet: true, immutable: true},
		{name: "cannot add subnet after AFEC revocation", update: true, oldTag: true, subnet: true, immutable: true},
		{name: "private API update still requires subnet", update: true, enrolled: true, tag: ptr.To("true"), privateAPI: true},
		{name: "private KMS update still requires subnet", update: true, privateKMS: true},
		{name: "both private update independently require subnet", update: true, enrolled: true, tag: ptr.To("true"), privateAPI: true, privateKMS: true},
	}
	for _, version := range versions {
		for _, tt := range tests {
			t.Run(string(version)+"/"+tt.name, func(t *testing.T) {
				cluster := createValidCluster()
				cluster.CustomerProperties.Version.ID = "4.22"
				subnet := cluster.CustomerProperties.Platform.VnetIntegrationSubnetID
				if !tt.subnet {
					cluster.CustomerProperties.Platform.VnetIntegrationSubnetID = nil
				}
				if tt.privateAPI {
					cluster.CustomerProperties.API.Visibility = metadataapi.VisibilityPrivate
				}
				if tt.privateKMS {
					cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility = metadataapi.KeyVaultVisibilityPrivate
				}
				key := metadataapi.TagClusterDisableSwift
				if tt.mixedCaseKey {
					key = strings.ToUpper(key)
				}
				cluster.Tags = nil
				if tt.tag != nil {
					cluster.Tags = map[string]string{key: *tt.tag}
				}
				op := operation.Operation{Type: operation.Create, Options: []string{metadataapi.APIVersionOption(version)}}
				if tt.enrolled {
					op.Options = append(op.Options, metadataapi.FeatureExperimentalReleaseFeatures)
				}
				var oldCluster *coreapi.HCPOpenShiftCluster
				if tt.update {
					op.Type = operation.Update
					oldCluster = cluster.DeepCopy()
					oldCluster.Tags = nil
					if tt.oldTag {
						oldCluster.Tags = map[string]string{metadataapi.TagClusterDisableSwift: "true"}
					}
					oldCluster.CustomerProperties.Platform.VnetIntegrationSubnetID = nil
					if tt.oldSubnet {
						oldCluster.CustomerProperties.Platform.VnetIntegrationSubnetID = subnet
					}
				}
				var expected []utils.ExpectedError
				if tt.invalidTag {
					expected = append(expected, utils.ExpectedError{FieldPath: "tags[" + key + "]", Message: "must be exactly"})
				}
				if tt.conflictingTag {
					expected = append(expected, utils.ExpectedError{FieldPath: "tags[" + key + "]", Message: "cannot disable SWIFT"})
				}
				if tt.immutable {
					expected = append(expected,
						utils.ExpectedError{FieldPath: subnetPath, Message: "field is immutable"})
				}
				if !tt.subnet {
					if tt.privateAPI {
						expected = append(expected, utils.ExpectedError{FieldPath: subnetPath, Message: "required when customerProperties.api.visibility is Private"})
					}
					if tt.privateKMS {
						expected = append(expected, utils.ExpectedError{FieldPath: subnetPath, Message: "required when customerProperties.etcd.dataEncryption.customerManaged.kms.visibility is Private"})
					}
					if !tt.update && version != metadataapi.APIVersionV20240610Preview && (!tt.enrolled || ptr.Deref(tt.tag, "") != "true") {
						expected = append(expected, utils.ExpectedError{FieldPath: subnetPath, Message: "required unless the disable-swift experimental tag"})
					}
				}
				utils.VerifyErrorsMatch(t, expected, ValidateCluster(t.Context(), op, cluster, oldCluster, nil))
			})
		}
	}
}
