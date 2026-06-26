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

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api"
)

// ExternalAuthAdmissionContext carries dependencies that external auth mutation/admission needs
// beyond the external auth object itself.
type ExternalAuthAdmissionContext struct {
	// ClusterExternalAuths is a list of all external auths for the cluster
	ClusterExternalAuths []*api.HCPOpenShiftClusterExternalAuth
}

// AdmitExternalAuth performs non-static checks of external auth. Checks that require more information than is contained inside of
// the external auth instance itself.
func AdmitExternalAuth(ctx context.Context, admissionContext *ExternalAuthAdmissionContext, op operation.Operation, newExternalAuth, oldExternalAuth *api.HCPOpenShiftClusterExternalAuth) field.ErrorList {
	errs := field.ErrorList{}

	// We do a *best-effort* to check to see if there are other external auths on the cluster and if there are we
	// prevent the creation of a new external auth. This is because as of now (2026-06-26) we do not allow creating more than one external auth
	// per cluster.
	if op.Type == operation.Create {
		if len(admissionContext.ClusterExternalAuths) > 0 {
			errs = append(errs, field.Forbidden(field.NewPath("name"), "There are other external auths on the cluster. Only one external auth is allowed per cluster."))
		}
	}

	return errs
}
