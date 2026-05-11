// Copyright 2025 Microsoft Corporation
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
	"regexp"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api"
)

const (
	conditionTypeMaxLength    = 316
	conditionReasonMinLength  = 1
	conditionReasonMaxLength  = 1024
	conditionMessageMaxLength = 32768
	conditionMaxItems         = 10
)

var (
	conditionTypePattern   = regexp.MustCompile(`^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?([A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?)$`)
	conditionReasonPattern = regexp.MustCompile(`^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$`)
)

// validateConditions validates a slice of conditions using the standard
// validation helpers. Constraints are aligned with Kubernetes metav1.Condition.
func validateConditions(ctx context.Context, op operation.Operation, fldPath *field.Path, conditions []api.Condition) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, MaxItems(ctx, op, fldPath, conditions, nil, conditionMaxItems)...)

	errs = append(errs, validate.Unique(
		ctx, op, fldPath,
		conditions, nil,
		func(a, b api.Condition) bool {
			return a.Type == b.Type
		},
	)...)

	errs = append(errs, validate.EachSliceVal(
		ctx, op, fldPath,
		conditions, nil,
		nil, nil,
		validateCondition,
	)...)

	return errs
}

func validateCondition(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, _ *api.Condition) field.ErrorList {
	errs := field.ErrorList{}

	condType := string(newObj.Type)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("type"), &newObj.Type, nil, api.ValidConditionTypes, nil)...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("type"), &condType, nil, conditionTypeMaxLength)...)
	errs = append(errs, MatchesRegex(ctx, op, fldPath.Child("type"), &condType, nil, conditionTypePattern, "must match Kubernetes condition type pattern")...)

	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("status"), &newObj.Status, nil, api.ValidConditionStatusTypes, nil)...)

	if newObj.LastTransitionTime.IsZero() {
		errs = append(errs, field.Required(fldPath.Child("lastTransitionTime"), "must be set"))
	}

	errs = append(errs, MinLen(ctx, op, fldPath.Child("reason"), &newObj.Reason, nil, conditionReasonMinLength)...)
	errs = append(errs, MaxLen(ctx, op, fldPath.Child("reason"), &newObj.Reason, nil, conditionReasonMaxLength)...)
	errs = append(errs, MatchesRegex(ctx, op, fldPath.Child("reason"), &newObj.Reason, nil, conditionReasonPattern, "must match CamelCase pattern")...)

	errs = append(errs, MaxLen(ctx, op, fldPath.Child("message"), &newObj.Message, nil, conditionMessageMaxLength)...)

	return errs
}
