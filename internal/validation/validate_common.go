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
	"context"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/safe"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

// AFECsToValidationOptions converts the API logic into validation compatible options.
// Feature names are normalized to lowercase for case-insensitive comparison.
func AFECsToValidationOptions(features []coreapi.Feature) []string {
	ret := []string{}

	for _, curr := range features {
		if curr.Name == nil || len(*curr.Name) == 0 {
			continue
		}
		if ptr.Deref(curr.State, "") == "Registered" {
			ret = append(ret, strings.ToLower(*curr.Name))
		}
	}

	return ret
}

// BuildValidationOptions combines AFEC feature flags and the ARM API version
// into a single options slice for operation.Operation.Options.
func BuildValidationOptions(features []coreapi.Feature, apiVersion metadataapi.APIVersion) []string {
	options := AFECsToValidationOptions(features)
	//apiVersion can be empty if this is call from within the backend
	// as backend doesn't have this context
	if apiVersion != "" {
		options = append(options, metadataapi.APIVersionOption(apiVersion))
	}
	return options
}

var (
	toTrackedResourceResource = func(oldObj *coreapi.TrackedResource) *coreapi.Resource { return &oldObj.Resource }
	toTrackedResourceLocation = func(oldObj *coreapi.TrackedResource) *string { return &oldObj.Location }
)

func validateTrackedResource(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.TrackedResource) field.ErrorList {
	errs := field.ErrorList{}

	//Resource
	errs = append(errs, validateResource(ctx, op, fldPath.Child("resource"), &newObj.Resource, safe.Field(oldObj, toTrackedResourceResource))...)

	//Location string            `json:"location,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("location"), &newObj.Location, safe.Field(oldObj, toTrackedResourceLocation))...)
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("location"), &newObj.Location, safe.Field(oldObj, toTrackedResourceLocation))...)

	//Tags     map[string]string `json:"tags,omitempty"`

	return errs
}

var (
	toResourceID         = func(oldObj *coreapi.Resource) *azcorearm.ResourceID { return oldObj.ID }
	toResourceName       = func(oldObj *coreapi.Resource) *string { return &oldObj.Name }
	toResourceType       = func(oldObj *coreapi.Resource) *string { return &oldObj.Type }
	toResourceSystemData = func(oldObj *coreapi.Resource) *coreapi.SystemData { return oldObj.SystemData }
)

// Version                 VersionProfile              `json:"version,omitempty"`
func validateResource(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.Resource) field.ErrorList {
	errs := field.ErrorList{}

	//ID         string      `json:"id,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("id"), newObj.ID, safe.Field(oldObj, toResourceID))...)
	errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("id"), newObj.ID, safe.Field(oldObj, toResourceID))...)
	errs = append(errs, GenericResourceID(ctx, op, fldPath.Child("id"), newObj.ID, safe.Field(oldObj, toResourceID))...)

	//Name       string      `json:"name,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("name"), &newObj.Name, safe.Field(oldObj, toResourceName))...)
	if newObj.ID != nil {
		errs = append(errs, EqualFold(ctx, op, fldPath.Child("name"), &newObj.Name, safe.Field(oldObj, toResourceName), newObj.ID.Name)...)
	}

	//Type       string      `json:"type,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("type"), &newObj.Type, safe.Field(oldObj, toResourceType))...)
	if newObj.ID != nil {
		errs = append(errs, EqualFold(ctx, op, fldPath.Child("type"), &newObj.Type, safe.Field(oldObj, toResourceType), newObj.ID.ResourceType.String())...)
	}

	//SystemData *SystemData `json:"systemData,omitempty"`
	errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("systemData"), newObj.SystemData, safe.Field(oldObj, toResourceSystemData))...)
	errs = append(errs, validateSystemData(ctx, op, fldPath.Child("systemData"), newObj.SystemData, safe.Field(oldObj, toResourceSystemData))...)

	return errs
}

var (
	toSystemDataCreatedAt     = func(oldObj *coreapi.SystemData) *time.Time { return oldObj.CreatedAt }
	toSystemDataCreatedBy     = func(oldObj *coreapi.SystemData) *string { return &oldObj.CreatedBy }
	toSystemDataCreatedByType = func(oldObj *coreapi.SystemData) *coreapi.CreatedByType { return &oldObj.CreatedByType }
)

func validateSystemData(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.SystemData) field.ErrorList {
	if newObj == nil {
		return nil
	}

	errs := field.ErrorList{}

	//CreatedBy string `json:"createdBy,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("createdBy"), &newObj.CreatedBy, safe.Field(oldObj, toSystemDataCreatedBy))...)
	if oldObj != nil && len(oldObj.CreatedBy) > 0 {
		// allow bad old data until we count records and get zero
		errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("createdBy"), &newObj.CreatedBy, safe.Field(oldObj, toSystemDataCreatedBy))...)
	}

	//CreatedAt *time.Time `json:"createdAt,omitempty"`
	errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("createdAt"), newObj.CreatedAt, safe.Field(oldObj, toSystemDataCreatedAt))...)
	if newObj.CreatedAt != nil {
		errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("createdAt"), newObj.CreatedAt, safe.Field(oldObj, toSystemDataCreatedAt))...)
	}
	if oldObj != nil && oldObj.CreatedAt != nil {
		errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("createdAt"), newObj.CreatedAt, safe.Field(oldObj, toSystemDataCreatedAt))...)
	}

	//CreatedByType CreatedByType `json:"createdByType,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("createdByType"), &newObj.CreatedByType, safe.Field(oldObj, toSystemDataCreatedByType))...)
	if oldObj != nil && len(oldObj.CreatedByType) > 0 {
		errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("createdByType"), &newObj.CreatedByType, safe.Field(oldObj, toSystemDataCreatedByType))...)
	}

	//LastModifiedBy string `json:"lastModifiedBy,omitempty"`
	//LastModifiedByType CreatedByType `json:"lastModifiedByType,omitempty"`
	//LastModifiedAt *time.Time `json:"lastModifiedAt,omitempty"`

	return errs
}
