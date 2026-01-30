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

package ocm

import (
	"context"
	"iter"
	"math"

	sdk "github.com/openshift-online/ocm-sdk-go"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

type ListIterator[T any] interface {
	Items(ctx context.Context) iter.Seq[*T]
	GetError() error
}

type ClusterListIterator interface {
	Items(ctx context.Context) iter.Seq[*arohcpv1alpha1.Cluster]
	GetError() error
}

type NodePoolListIterator interface {
	Items(ctx context.Context) iter.Seq[*arohcpv1alpha1.NodePool]
	GetError() error
}

type ExternalAuthListIterator interface {
	Items(ctx context.Context) iter.Seq[*arohcpv1alpha1.ExternalAuth]
	GetError() error
}

type simpleListIterator[T any] struct {
	clusters []*T
	err      error
}

func NewSimpleClusterListIterator(objs []*arohcpv1alpha1.Cluster, err error) ClusterListIterator {
	return &simpleListIterator[arohcpv1alpha1.Cluster]{
		clusters: objs,
		err:      err,
	}
}

func NewSimpleNodePoolListIterator(objs []*arohcpv1alpha1.NodePool, err error) NodePoolListIterator {
	return &simpleListIterator[arohcpv1alpha1.NodePool]{
		clusters: objs,
		err:      err,
	}
}

func NewSimpleExternalAuthListIterator(objs []*arohcpv1alpha1.ExternalAuth, err error) ExternalAuthListIterator {
	return &simpleListIterator[arohcpv1alpha1.ExternalAuth]{
		clusters: objs,
		err:      err,
	}
}

// Items returns a push iterator that can be used directly in for/range loops.
// If an error occurs during paging, iteration stops and the error is recorded.
func (iter *simpleListIterator[T]) Items(ctx context.Context) iter.Seq[*T] {
	return func(yield func(*T) bool) {
		for _, cluster := range iter.clusters {
			if !yield(cluster) {
				return
			}
		}
	}
}

// GetError returns any error that occurred during iteration. Call this after the
// for/range loop that calls Items() to check if iteration completed successfully.
func (iter *simpleListIterator[T]) GetError() error {
	return iter.err
}

type clusterListIterator struct {
	conn    *sdk.Connection
	request *arohcpv1alpha1.ClustersListRequest
	err     error
}

type VersionsListIterator struct {
	request *arohcpv1alpha1.VersionsListRequest
	err     error
}

// Items returns a push iterator that can be used directly in for/range loops.
// If an error occurs during paging, iteration stops and the error is recorded.
func (iter *clusterListIterator) Items(ctx context.Context) iter.Seq[*arohcpv1alpha1.Cluster] {
	return func(yield func(*arohcpv1alpha1.Cluster) bool) {
		// Request can be nil to allow for mocking.
		if iter.request != nil {
			var page = 0
			var count = 0
			var total = math.MaxInt

			for count < total {
				page++
				result, err := iter.request.Page(page).SendContext(ctx)
				if err != nil {
					iter.err = err
					return
				}

				total = result.Total()
				items := result.Items()

				// Safety check to prevent an infinite loop in case
				// the result is somehow empty before count = total.
				if items == nil || items.Empty() {
					return
				}

				count += items.Len()

				// XXX ClusterList.Each() lacks a boolean return to
				//     indicate whether iteration fully completed.
				//     ClusterList.Slice() may be less efficient but
				//     is easier to work with.
				for _, item := range items.Slice() {
					item, err = resolveClusterLinks(ctx, iter.conn, item)
					if err != nil {
						iter.err = err
						return
					}

					if !yield(item) {
						return
					}
				}
			}
		}
	}
}

// GetError returns any error that occurred during iteration. Call this after the
// for/range loop that calls Items() to check if iteration completed successfully.
func (iter clusterListIterator) GetError() error {
	return iter.err
}

type nodePoolListIterator struct {
	conn    *sdk.Connection
	request *arohcpv1alpha1.NodePoolsListRequest
	err     error
}

// Items returns a push iterator that can be used directly in for/range loops.
// If an error occurs during paging, iteration stops and the error is recorded.
func (iter *nodePoolListIterator) Items(ctx context.Context) iter.Seq[*arohcpv1alpha1.NodePool] {
	return func(yield func(*arohcpv1alpha1.NodePool) bool) {
		// Request can be nil to allow for mocking.
		if iter.request != nil {
			var page = 0
			var count = 0
			var total = math.MaxInt

			for count < total {
				page++
				result, err := iter.request.Page(page).SendContext(ctx)
				if err != nil {
					iter.err = err
					return
				}

				total = result.Total()
				items := result.Items()

				// Safety check to prevent an infinite loop in case
				// the result is somehow empty before count = total.
				if items == nil || items.Empty() {
					return
				}

				count += items.Len()

				// XXX NodePoolList.Each() lacks a boolean return to
				//     indicate whether iteration fully completed.
				//     NodePoolList.Slice() may be less efficient but
				//     is easier to work with.
				for _, item := range items.Slice() {
					item, err = resolveNodePoolLinks(ctx, iter.conn, item)
					if err != nil {
						iter.err = err
						return
					}

					if !yield(item) {
						return
					}
				}
			}
		}
	}
}

// GetError returns any error that occurred during iteration. Call this after the
// for/range loop that calls Items() to check if iteration completed successfully.
func (iter nodePoolListIterator) GetError() error {
	return iter.err
}

type externalAuthListIterator struct {
	request *arohcpv1alpha1.ExternalAuthsListRequest
	err     error
}

// Items returns a push iterator that can be used directly in for/range loops.
// If an error occurs during paging, iteration stops and the error is recorded.
func (iter *externalAuthListIterator) Items(ctx context.Context) iter.Seq[*arohcpv1alpha1.ExternalAuth] {
	return func(yield func(*arohcpv1alpha1.ExternalAuth) bool) {
		// Request can be nil to allow for mocking.
		if iter.request != nil {
			var page = 0
			var count = 0
			var total = math.MaxInt

			for count < total {
				page++
				result, err := iter.request.Page(page).SendContext(ctx)
				if err != nil {
					iter.err = err
					return
				}

				total = result.Total()
				items := result.Items()

				// Safety check to prevent an infinite loop in case
				// the result is somehow empty before count = total.
				if items == nil || items.Empty() {
					return
				}

				count += items.Len()

				// XXX ExternalAuthList.Each() lacks a boolean return to
				//     indicate whether iteration fully completed.
				//     ExternalAuthList.Slice() may be less efficient but
				//     is easier to work with.
				for _, item := range items.Slice() {
					if !yield(item) {
						return
					}
				}
			}
		}
	}
}

// GetError returns any error that occurred during iteration. Call this after the
// for/range loop that calls Items() to check if iteration completed successfully.
func (iter externalAuthListIterator) GetError() error {
	return iter.err
}

type BreakGlassCredentialListIterator struct {
	request *cmv1.BreakGlassCredentialsListRequest
	err     error
}

// Items returns a push iterator that can be used directly in for/range loops.
// If an error occurs during paging, iteration stops and the error is recorded.
func (iter *BreakGlassCredentialListIterator) Items(ctx context.Context) iter.Seq[*cmv1.BreakGlassCredential] {
	return func(yield func(*cmv1.BreakGlassCredential) bool) {
		// Request can be nil to allow for mocking.
		if iter.request != nil {
			var page = 0
			var count = 0
			var total = math.MaxInt

			for count < total {
				page++
				result, err := iter.request.Page(page).SendContext(ctx)
				if err != nil {
					iter.err = err
					return
				}

				total = result.Total()
				items := result.Items()

				// Safety check to prevent an infinite loop in case
				// the result is somehow empty before count = total.
				if items == nil || items.Empty() {
					return
				}

				count += items.Len()

				// XXX BreakGlassCredentialList.Each() lacks a boolean return
				//     to indicate whether iteration fully completed.
				//     BreakGlassCredentialList.Slice() may be less efficient
				//     but is easier to work with.
				for _, item := range items.Slice() {
					if !yield(item) {
						return
					}
				}
			}
		}
	}
}

// GetError returns any error that occurred during iteration. Call this after the
// for/range loop that calls Items() to check if iteration completed successfully.
func (iter BreakGlassCredentialListIterator) GetError() error {
	return iter.err
}

// Items returns a push iterator that can be used directly in for/range loops.
// If an error occurs during paging, iteration stops and the error is recorded.
// Options can be passed to configure search parameters.
func (iter *VersionsListIterator) Items(ctx context.Context, opts *VersionsListOptions) iter.Seq[*arohcpv1alpha1.Version] {
	return func(yield func(*arohcpv1alpha1.Version) bool) {
		// Request can be nil to allow for mocking.
		if iter.request != nil {
			// Apply options if provided
			if opts != nil && opts.Search != "" {
				iter.request.Search(opts.Search)
			}

			var page = 0
			var count = 0
			var total = math.MaxInt

			for count < total {
				page++
				result, err := iter.request.Page(page).SendContext(ctx)
				if err != nil {
					iter.err = err
					return
				}

				total = result.Total()
				items := result.Items()

				// Safety check to prevent an infinite loop in case
				// the result is somehow empty before count = total.
				if items == nil || items.Empty() {
					return
				}

				count += items.Len()

				// XXX VersionsList.Each() lacks a boolean return to
				//     indicate whether iteration fully completed.
				//     VersionsList.Slice() may be less efficient but
				//     is easier to work with.
				for _, item := range items.Slice() {
					if !yield(item) {
						return
					}
				}
			}
		}
	}
}

// GetError returns any error that occurred during iteration. Call this after the
// for/range loop that calls Items() to check if iteration completed successfully.
func (iter VersionsListIterator) GetError() error {
	return iter.err
}
