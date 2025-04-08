package database

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// Copied from azcore/internal/shared/shared.go
func Delay(ctx context.Context, delay time.Duration) error {
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type LockClient struct {
	name              string
	containerClient   *azcosmos.ContainerClient
	defaultTimeToLive int32
}

// lockDocument implements a global distributed lock.
// Its contents should be opaque outside of LockClient.
type lockDocument struct {
	baseDocument
	Owner string `json:"owner,omitempty"`
	TTL   int32  `json:"ttl,omitempty"`
}

// NewLockClient creates a LockClient around a ContainerClient. It attempts to
// read container properties to extract a default TTL. If this fails or if the
// container does not define a default TTL, the function returns an error.
func NewLockClient(ctx context.Context, containerClient *azcosmos.ContainerClient) (*LockClient, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	c := &LockClient{
		name:            hostname,
		containerClient: containerClient,
	}

	response, err := containerClient.Read(ctx, nil)
	if err != nil {
		return nil, err
	}

	if response.ContainerProperties != nil && response.ContainerProperties.DefaultTimeToLive != nil {
		c.defaultTimeToLive = *response.ContainerProperties.DefaultTimeToLive
	} else {
		return nil, fmt.Errorf("container '%s' does not have a default TTL", containerClient.ID())
	}

	return c, nil
}

// SetName overrides how a lock item identifies the owner. This is for
// informational purposes only. LockClient uses the hostname by default.
func (c *LockClient) SetName(name string) {
	c.name = name
}

// GetDefaultTimeToLive returns the default time-to-live value of the
// container as a time.Duration.
func (c *LockClient) GetDefaultTimeToLive() time.Duration {
	return time.Duration(c.defaultTimeToLive) * time.Second
}

// SetRetryAfterHeader sets a "Retry-After" header to the default TTL value.
func (c *LockClient) SetRetryAfterHeader(header http.Header) {
	header.Set("Retry-After", strconv.Itoa(int(c.defaultTimeToLive)))
}

// AcquireLock persistently tries to acquire a lock for the given ID. If a
// timeout is provided, the function will cease after the timeout duration
// and return a context.DeadlineExceeded error.
func (c *LockClient) AcquireLock(ctx context.Context, id string, timeout *time.Duration) (*azcosmos.ItemResponse, error) {
	var lock *azcosmos.ItemResponse

	if timeout != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	for lock == nil {
		var err error

		lock, err = c.TryAcquireLock(ctx, id)
		if err != nil {
			return nil, err
		}
		if lock == nil {
			// TTL values are in whole seconds,
			// so wait one second before retrying.
			err = Delay(ctx, time.Second)
			if err != nil {
				return nil, err
			}
		}
	}

	return lock, nil
}

// TryAcquireLock tries once to acquire a lock for the given ID. If the lock
// is already taken, it returns a nil azcosmos.ItemResponse and no error.
func (c *LockClient) TryAcquireLock(ctx context.Context, id string) (*azcosmos.ItemResponse, error) {
	doc := &lockDocument{
		baseDocument: baseDocument{ID: id},
		Owner:        c.name,
		TTL:          c.defaultTimeToLive,
	}

	data, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}

	pk := azcosmos.NewPartitionKeyString(doc.ID)
	options := &azcosmos.ItemOptions{
		EnableContentResponseOnWrite: true,
	}
	response, err := c.containerClient.CreateItem(ctx, pk, data, options)
	if isResponseError(err, http.StatusConflict) {
		return nil, nil // lock already acquired by someone else
	} else if err != nil {
		return nil, err
	}

	return &response, nil
}

type StopHoldLock func() *azcosmos.ItemResponse

// HoldLock tries to hold an acquired lock by renewing it periodically from a
// goroutine until the returned stop function is called. The function also returns
// a new context which is cancelled if the lock is lost or some other error occurs.
// The stop function terminates the goroutine and returns the current lock, or nil
// if the lock was lost.
func (c *LockClient) HoldLock(ctx context.Context, item *azcosmos.ItemResponse) (cancelCtx context.Context, stop StopHoldLock) {
	cancelCtx, cancelCause := context.WithCancelCause(ctx)
	done := make(chan struct{})

	stop = func() *azcosmos.ItemResponse {
		cancelCause(nil)
		<-done // wait for goroutine to finish
		return item
	}

	go func() {
		defer close(done)
		for {
			var doc *lockDocument

			err := json.Unmarshal(item.Value, &doc)
			if err != nil {
				cancelCause(fmt.Errorf("failed to unmarshal lock: %w", err))
				return
			}

			// Aim to renew one second before TTL expires.
			timeToRenew := time.Unix(int64(doc.CosmosTimestamp), 0)
			if doc.TTL > 0 {
				timeToRenew = timeToRenew.Add(time.Duration(doc.TTL-1) * time.Second)
			}

			select {
			case <-time.After(time.Until(timeToRenew)):
				item, err = c.RenewLock(cancelCtx, item)
				if err != nil {
					cancelCause(fmt.Errorf("failed to renew lock: %w", err))
					return
				}
				if item == nil {
					// We lost the lock, cancel the context.
					cancelCause(nil)
					return
				}
			case <-cancelCtx.Done():
				return
			}
		}
	}()

	return
}

// RenewLock attempts to renew an acquired lock. If successful it returns a new lock.
// If the lock was somehow lost, it returns a nil azcosmos.ItemResponse and no error.
func (c *LockClient) RenewLock(ctx context.Context, item *azcosmos.ItemResponse) (*azcosmos.ItemResponse, error) {
	var doc *lockDocument

	err := json.Unmarshal(item.Value, &doc)
	if err != nil {
		return nil, err
	}

	pk := azcosmos.NewPartitionKeyString(doc.ID)
	options := &azcosmos.ItemOptions{
		EnableContentResponseOnWrite: true,
		IfMatchEtag:                  &item.ETag,
	}
	response, err := c.containerClient.UpsertItem(ctx, pk, item.Value, options)
	if isResponseError(err, http.StatusPreconditionFailed) {
		return nil, nil // lock already acquired by someone else
	} else if err != nil {
		return nil, err
	}

	return &response, nil
}

// ReleaseLock attempts to release an acquired lock. Errors should be logged but not
// treated as fatal, since the container item's TTL value guarantees that it will be
// released eventually.
func (c *LockClient) ReleaseLock(ctx context.Context, item *azcosmos.ItemResponse) error {
	var doc *lockDocument

	err := json.Unmarshal(item.Value, &doc)
	if err != nil {
		return err
	}

	pk := azcosmos.NewPartitionKeyString(doc.ID)
	options := &azcosmos.ItemOptions{
		IfMatchEtag: &item.ETag,
	}
	_, err = c.containerClient.DeleteItem(ctx, pk, doc.ID, options)
	if isResponseError(err, http.StatusPreconditionFailed) {
		return nil // lock already acquired by someone else
	}

	return err
}
