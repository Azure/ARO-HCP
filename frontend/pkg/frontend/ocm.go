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

package frontend

import (
	"net/http"
)

// transportFunc implements the http.RoundTripper interface.
type transportFunc func(*http.Request) (*http.Response, error)

var _ = http.RoundTripper(transportFunc(nil))

func (rtf transportFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return rtf(r)
}

const clusterServiceRequestIDHeader = "X-Request-ID"

// RequestIDPropagator returns an http.RoundTripper interface which reads the
// request ID from the request's context and propagates it to the Clusters
// Service API via the "X-Request-ID" header.
func RequestIDPropagator(next http.RoundTripper) http.RoundTripper {
	return transportFunc(func(r *http.Request) (*http.Response, error) {
		correlationData, err := CorrelationDataFromContext(r.Context())
		if err == nil {
			r = r.Clone(r.Context())
			r.Header.Set(clusterServiceRequestIDHeader, correlationData.RequestID.String())
		}

		return next.RoundTrip(r)
	})
}
