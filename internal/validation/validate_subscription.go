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

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func ValidateSubscriptionCreate(ctx context.Context, newObj *arm.Subscription) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return validateSubscription(ctx, op, newObj, nil)
}

func validateSubscription(ctx context.Context, op operation.Operation, newObj, oldObj *arm.Subscription) field.ErrorList {
	errs := field.ErrorList{}

	// these are the only two validated fields
	//State            SubscriptionState       `json:"state"            validate:"required,enum_subscriptionstate"`
	errs = append(errs, validate.Enum(ctx, op, field.NewPath("required"), &newObj.State, nil, arm.ValidSubscriptionStates)...)

	//RegistrationDate *string                 `json:"registrationDate" validate:"required"`
	errs = append(errs, validate.RequiredPointer(ctx, op, field.NewPath("registrationDate"), newObj.RegistrationDate, nil)...)
	if newObj.RegistrationDate != nil {
		errs = append(errs, NoExtraWhitespace(ctx, op, field.NewPath("registrationDate"), newObj.RegistrationDate, nil)...)
	}

	//Properties       *SubscriptionProperties `json:"properties"`
	//LastUpdated int `json:"-"`

	return errs
}
