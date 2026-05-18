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

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api/fleet"
)

func ValidateStampCreate(_ context.Context, stamp *fleet.Stamp) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, validateStampIdentifier(stamp)...)
	return errs
}

func ValidateStampUpdate(_ context.Context, newStamp *fleet.Stamp, _ *fleet.Stamp) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, validateStampIdentifier(newStamp)...)
	return errs
}

func validateStampIdentifier(stamp *fleet.Stamp) field.ErrorList {
	var errs field.ErrorList
	stampIdentifier := stamp.GetStampIdentifier()
	if !stampIdentifierRegex.MatchString(stampIdentifier) {
		errs = append(errs, field.Invalid(
			field.NewPath("cosmosMetadata", "resourceID"),
			stampIdentifier,
			"stamp identifier must match [0-9a-z]{1,3}",
		))
	}
	return errs
}
