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

// Package desirestatuswriter is the generic "mutate-replace" helper that
// writes back the .status section of kube-applier *Desire Cosmos documents
// (ApplyDesire and ReadDesire). It has nothing to do with the
// .status section of the Kubernetes objects those desires target — that
// content, when relevant, lives in ReadDesire.status.kubeContent and is
// written by the per-instance ReadDesireKubernetesController, not by this
// package.
//
// The kube-applier never creates desires (the backend does), so the helper
// deliberately omits the create-if-missing branch present in backend's
// controllerutils.WriteController.
//
// Callers supply the document revision they operated on and a named struct
// implementing Replacer. The writer never re-fetches before writing status.
package desirestatuswriter

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// Fetcher reads the current state of a single desire by a controller-defined
// typed key. Implementations must use a live Cosmos client so retries observe
// the latest revision. The StatusWriter deep-copies the document before mutating.
type Fetcher[T any, K comparable] interface {
	Fetch(ctx context.Context, key K) (*T, error)
}

// Replacer writes back a fully-populated desire. Implementations are free to
// derive the partition / parent scoping from the desire itself (the
// kube-applier controllers parse it out of the desire's resourceID), so the
// StatusWriter does not pass a key here.
// Implementations must conditionally replace using the supplied document's
// etag and return PreconditionFailed (412) if that revision is no longer current.
type Replacer[T any] interface {
	Replace(ctx context.Context, desired *T) error
}

// DeepCopyable is the constraint on the pointer-type parameter that lets the
// StatusWriter clone the value it receives from the caller without knowing
// T's concrete shape. The kubeapplier *Desire types satisfy it via the
// generated zz_generated.deepcopy.go.
type DeepCopyable[T any] interface {
	*T
	DeepCopy() *T
}

// MutateFunc deep-mutates a desire to record the latest controller observation.
// It must not perform IO; precompute everything first.
type MutateFunc[T any] func(*T)

// StatusWriter computes the next desired state via mutate and writes it back
// once. It returns nil for a no-op (status already up-to-date) and surfaces
// errors, including etag conflicts (PreconditionFailed / 412), for retry.
type StatusWriter[T any] interface {
	// UpdateStatusFrom records an operation against the same document revision
	// it used, without fetching again. On conflict, callers must repeat the
	// operation against a fresh document before retrying the status update.
	UpdateStatusFrom(ctx context.Context, operated *T, mutate MutateFunc[T]) error
}

// New returns a StatusWriter that writes via replacer.
// The second type parameter is the pointer type *T; it carries the DeepCopy
// method the StatusWriter needs to clone the existing desire before mutating
// it. Callers spell it out at the call site so we do not need a separate
// "Cloner" parameter.
func New[T any, PT DeepCopyable[T]](replacer Replacer[T]) StatusWriter[T] {
	return &writer[T, PT]{replacer: replacer}
}

type writer[T any, PT DeepCopyable[T]] struct {
	replacer Replacer[T]
}

// UpdateStatusFrom implements the mutate-replace cycle. The contract:
//
//   - Skip the write entirely when mutate produces a deeply-equal copy of the
//     existing desire — this is the steady state for healthy reconciles.
//   - Surface every error to the caller, including PreconditionFailed (412),
//     so the controller's workqueue retries with backoff. The kube-applier
//     can have multiple controllers writing to the same desire (the manager's
//     construction-failure path vs. the per-instance controller's
//     Successful/KubeContent writes); on conflict the loser must retry.
//
// We DeepCopy the supplied desire before applying mutate. A naive
// `desired := *existing` would share the conditions-slice backing array with
// existing, and meta.SetStatusCondition mutates conditions in place, so the
// comparison would always report no change and silently drop writes.
func (w *writer[T, PT]) UpdateStatusFrom(ctx context.Context, operated *T, mutate MutateFunc[T]) error {
	desired := PT(operated).DeepCopy()
	mutate(desired)

	if equality.Semantic.DeepEqual(operated, desired) {
		return nil
	}

	if err := w.replacer.Replace(ctx, desired); err != nil {
		return utils.TrackError(err)
	}
	return nil
}
