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

package recorder

import (
	_ "k8s.io/component-base/metrics/prometheus/workqueue" // Register the named client-go queue metrics.

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

// Labels are code-owned outcomes only, never pod, namespace, node or error text.
var (
	episodes  = metrics.NewCounterVec(&metrics.CounterOpts{Namespace: "swift_recorder", Name: "episodes_total", Help: "Startup episode lifecycle outcomes."}, []string{"outcome"})
	captures  = metrics.NewCounterVec(&metrics.CounterOpts{Namespace: "swift_recorder", Name: "captures_total", Help: "Namespace capture outcomes."}, []string{"outcome"})
	overflows = metrics.NewCounterVec(&metrics.CounterOpts{Namespace: "swift_recorder", Name: "overflows_total", Help: "Evidence dropped at configured bounds."}, []string{"limit"})
)

func init() { legacyregistry.MustRegister(episodes, captures, overflows) }
