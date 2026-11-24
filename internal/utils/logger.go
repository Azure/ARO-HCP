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

package utils

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/component-base/metrics/legacyregistry"
)

const TracerName = "github.com/Azure/ARO-HCP/frontend"

var (
	// PanicTotal counts the total number of panics caught byt this handler.
	// If available, it labels by controller.  If not available, they go into "" bucket.
	// This could be expanded later for a resource type for the frontend if necessay, but something
	// counting crashes is better than nothing.
	PanicTotal = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "panic_total",
			Help: "Total number of panics per controller.",
		},
		[]string{"controller"},
	)
)

func IncrementPanicMetrics(ctx context.Context, r interface{}) {
	if r == http.ErrAbortHandler {
		// honor the http.ErrAbortHandler sentinel panic value:
		//   ErrAbortHandler is a sentinel panic value to abort a handler.
		//   While any panic from ServeHTTP aborts the response to the client,
		//   panicking with ErrAbortHandler also suppresses logging of a stack trace to the server's error log.
		return
	}

	controllerName, _ := ControllerNameFromContext(ctx)
	PanicTotal.WithLabelValues(controllerName).Inc()
}
