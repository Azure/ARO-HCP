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

package middleware

import (
	"fmt"
	"net/http"

	"k8s.io/klog/v2"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
)

func WithSessionProxyClaimHeaderAuthorization(owner sessiongatev1alpha1.Principal, next http.Handler) http.Handler {
	var claimName string
	switch owner.Type {
	case sessiongatev1alpha1.PrincipalTypeAzureUser:
		claimName = "X-JWT-Claim-Upn"
	case sessiongatev1alpha1.PrincipalTypeAzureServicePrincipal:
		claimName = "X-JWT-Claim-Oid"
	default:
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, fmt.Sprintf("unauthorized: unexpected owner type: %s", string(owner.Type)), http.StatusUnauthorized)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger := klog.FromContext(r.Context()).WithValues("identity", owner.Name, "identityType", owner.Type)
		claimValue := r.Header.Get(claimName)
		if claimValue != owner.Name {
			logger.Error(fmt.Errorf("claim validation failed"), "unauthorized",
				"expected", fmt.Sprintf("%s=%s", claimName, owner.Name), "got", fmt.Sprintf("%s=%s", claimName, claimValue))
			http.Error(w, "unauthorized: claim validation failed", http.StatusUnauthorized)
			return
		}
		r = r.WithContext(klog.NewContext(r.Context(), logger))
		next.ServeHTTP(w, r)
	})
}
