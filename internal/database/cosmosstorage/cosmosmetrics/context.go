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

import (
	"context"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type informerNameKey struct{}
type callSiteKey struct{}
type queryKey struct{}

// ContextWithInformerName associates a stable informer name with the context.
// The derived context retains the parent's values, deadline, and cancellation.
func ContextWithInformerName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, informerNameKey{}, name)
}

// InformerNameFromContext returns the informer name, if present.
func InformerNameFromContext(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(informerNameKey{}).(string)
	return name, ok
}

// ContextWithCallSite associates a static, code-defined call-site identifier
// with the context. It becomes a metric label: never use dynamic customer values
// such as resource IDs, subscription IDs, resource names, or query parameters.
func ContextWithCallSite(ctx context.Context, callSite string) context.Context {
	return context.WithValue(ctx, callSiteKey{}, callSite)
}

// CallSiteFromContext returns the call site, or "unknown" if absent or empty.
func CallSiteFromContext(ctx context.Context) string {
	if callSite, ok := ctx.Value(callSiteKey{}).(string); ok && callSite != "" {
		return callSite
	}
	return "unknown"
}

type queryContext struct {
	shape string
	scope string
}

// contextWithQuery carries static query classifications, never query text or
// customer values, for attribution of all HTTP attempts made by a query.
func contextWithQuery(ctx context.Context, shape, scope string) context.Context {
	return context.WithValue(ctx, queryKey{}, queryContext{shape: shape, scope: scope})
}

func queryFromContext(ctx context.Context) (shape, scope string) {
	query, _ := ctx.Value(queryKey{}).(queryContext)
	shape, scope = query.shape, query.scope
	if shape == "" {
		shape = "none"
	}
	if scope == "" {
		scope = "none"
	}
	return shape, scope
}

func sourceFromContext(ctx context.Context) (kind, name string) {
	if name, ok := InformerNameFromContext(ctx); ok && name != "" {
		return sourceKindInformer, name
	}
	if name, ok := utils.ControllerNameFromContext(ctx); ok && name != "" {
		return sourceKindController, name
	}
	return sourceKindUnattributed, "unknown"
}
