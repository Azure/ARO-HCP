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

package admission

import (
	"context"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

// AdmitExternalAuth performs non-static checks of external authentication.  Checks that require more information than is contained inside of
// of the external authentication instance itself.
// We support returning a non-field error in case the database client call fails.
func AdmitExternalAuth(ctx context.Context, extAuthCosmosClient database.ExternalAuthsCRUD, incomingExternalAuth *api.HCPOpenShiftClusterExternalAuth) (field.ErrorList, error) {
	validationErrors := field.ErrorList{}

	iter, err := extAuthCosmosClient.List(ctx, &database.DBClientListResourceDocsOptions{})
	if err != nil {
		return validationErrors, err
	}

	count := 0
	for _, externalAuth := range iter.Items(ctx) {
		// Only count existing resources that are different from the incoming one
		if !strings.EqualFold(externalAuth.Name, incomingExternalAuth.Name) {
			count++
		}
	}

	if count > 0 {
		validationErrors = append(validationErrors, field.Invalid(
			field.NewPath("name"),
			incomingExternalAuth.Name,
			"Only one external authentication resource is allowed per cluster.",
		))
	}

	return validationErrors, nil
}
