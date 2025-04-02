package ocm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"iter"
	"math"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

type ClusterListIterator struct {
	request *arohcpv1alpha1.ClustersListRequest
	err     error
}

// Items returns a push iterator that can be used directly in for/range loops.
// If an error occurs during paging, iteration stops and the error is recorded.
func (iter ClusterListIterator) Items(ctx context.Context) iter.Seq[*arohcpv1alpha1.Cluster] {
	return func(yield func(*arohcpv1alpha1.Cluster) bool) {
		// Request can be nil to allow for mocking.
		if iter.request != nil {
			var page int = 0
			var count int = 0
			var total int = math.MaxInt

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
func (iter ClusterListIterator) GetError() error {
	return iter.err
}

type NodePoolListIterator struct {
	request *arohcpv1alpha1.NodePoolsListRequest
	err     error
}

// Items returns a push iterator that can be used directly in for/range loops.
// If an error occurs during paging, iteration stops and the error is recorded.
func (iter NodePoolListIterator) Items(ctx context.Context) iter.Seq[*arohcpv1alpha1.NodePool] {
	return func(yield func(*arohcpv1alpha1.NodePool) bool) {
		// Request can be nil to allow for mocking.
		if iter.request != nil {
			var page int = 0
			var count int = 0
			var total int = math.MaxInt

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
func (iter NodePoolListIterator) GetError() error {
	return iter.err
}

type BreakGlassCredentialListIterator struct {
	request *cmv1.BreakGlassCredentialsListRequest
	err     error
}

// Items returns a push iterator that can be used directly in for/range loops.
// If an error occurs during paging, iteration stops and the error is recorded.
func (iter BreakGlassCredentialListIterator) Items(ctx context.Context) iter.Seq[*cmv1.BreakGlassCredential] {
	return func(yield func(*cmv1.BreakGlassCredential) bool) {
		// Request can be nil to allow for mocking.
		if iter.request != nil {
			var page int = 0
			var count int = 0
			var total int = math.MaxInt

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
