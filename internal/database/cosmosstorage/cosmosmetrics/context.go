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

package cosmosmetrics

import "context"

type informerNameKey struct{}

// ContextWithInformerName attributes Cosmos requests to a stable informer name.
// The derived context retains the parent's values, deadline, and cancellation.
func ContextWithInformerName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, informerNameKey{}, name)
}

// InformerNameFromContext returns the explicit informer attribution, if present.
func InformerNameFromContext(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(informerNameKey{}).(string)
	return name, ok
}
