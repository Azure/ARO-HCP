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

package operationcontrollers

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api"
)

// DiffCustomerPropertiesAgainstStatus walks HCPOpenShiftClusterCustomerProperties
// (the customer's desired state) and compares each field that has a
// counterpart in ServiceProviderClusterStatus (the observed state) for equality.
// It returns one field.Error per mismatch; an empty list means the observed
// state matches the desired customer properties.
//
// The traversal mirrors the style used in internal/validation: one function
// per struct, each taking a field path and the matching pair of pointers.
// Customer-properties subfields without an observed counterpart in
// ServiceProviderClusterStatus are skipped (nothing to compare).
func DiffCustomerPropertiesAgainstStatus(customerProperties *api.HCPOpenShiftClusterCustomerProperties, status *api.ServiceProviderClusterStatus) field.ErrorList {
	errs := field.ErrorList{}
	fldPath := field.NewPath("customerProperties")

	// Version VersionProfile -> Status.CustomerPropertiesStatus.Version VersionProfileStatus
	errs = append(errs, diffVersionProfileAgainstStatus(
		fldPath.Child("version"),
		&customerProperties.Version,
		&status.CustomerPropertiesStatus.Version,
	)...)

	// DNS, Network, API, Platform, Autoscaling, NodeDrainTimeoutMinutes, Etcd,
	// ClusterImageRegistry, ImageDigestMirrors have no counterparts in
	// ServiceProviderClusterStatus.CustomerPropertiesStatus today, so nothing
	// to compare. Add per-struct diff functions here as status surfaces grow.

	return errs
}

// diffVersionProfileAgainstStatus compares the customer-desired VersionProfile
// against the observed VersionProfileStatus and emits one entry per field
// that does not match.
func diffVersionProfileAgainstStatus(fldPath *field.Path, desired *api.VersionProfile, observed *api.VersionProfileStatus) field.ErrorList {
	errs := field.ErrorList{}

	// ID string
	if desired.ID != observed.ID {
		errs = append(errs, field.Invalid(
			fldPath.Child("id"),
			desired.ID,
			fmt.Sprintf("does not match observed value %q", observed.ID),
		))
	}

	// ChannelGroup string
	if desired.ChannelGroup != observed.ChannelGroup {
		errs = append(errs, field.Invalid(
			fldPath.Child("channelGroup"),
			desired.ChannelGroup,
			fmt.Sprintf("does not match observed value %q", observed.ChannelGroup),
		))
	}

	return errs
}
