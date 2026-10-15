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

package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
)

const (
	maxDeletes = 256
	maxPages   = 16
	pageSize   = 128
)

// NewDeleter deletes live blobs, snapshots and versions through the normal Azure
// API. Soft-delete retention is respected; success does not mean permanent purge.
// The caller must fence writers and retry errors, including bounded partial work.
func NewDeleter(credential azcore.TokenCredential) func(context.Context, Target) error {
	return newDeleter(credential, nil)
}

// Options are private so tests can exercise the real SDK with an inert transport
// without relaxing endpoint validation or changing production credentials.
func newDeleter(credential azcore.TokenCredential, options *azblob.ClientOptions) func(context.Context, Target) error {
	return func(ctx context.Context, target Target) error {
		if err := Validate(target); err != nil {
			return err
		}
		if credential == nil {
			return fmt.Errorf("azure token credential is required")
		}
		client, err := azblob.NewClient(target.AccountURL, credential, options)
		if err != nil {
			return fmt.Errorf("create Azure blob client: %w", err)
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		return deleteRepository(ctx, client, target)
	}
}

func deleteRepository(ctx context.Context, client *azblob.Client, target Target) error {
	options := &azblob.ListBlobsFlatOptions{
		Prefix:     &target.Prefix,
		MaxResults: to.Ptr(int32(pageSize)),
		Include:    azblob.ListBlobsInclude{Snapshots: true, Versions: true},
	}
	deletes := 0
	// The second pass starts from the beginning: pagination while deleting is not
	// proof of emptiness, and deleting a current blob may leave a previous version.
	for pass := 0; pass < 2; pass++ {
		if pass == 1 {
			options.MaxResults = to.Ptr(int32(1))
		}
		pager := client.NewListBlobsFlatPager(target.Container, options)
		for pages := 0; pager.More(); pages++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if pages == maxPages {
				return fmt.Errorf("repository listing page budget exhausted; retry cleanup")
			}
			page, err := pager.NextPage(ctx)
			if missing(err, bloberror.ContainerNotFound) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("list repository blobs: %w", err)
			}
			if page.Segment == nil {
				return fmt.Errorf("azure listing omitted its blob segment")
			}
			for _, item := range page.Segment.BlobItems {
				if item == nil || item.Name == nil || !strings.HasPrefix(*item.Name, target.Prefix) {
					return fmt.Errorf("azure listing returned an invalid or out-of-scope blob")
				}
				if item.Deleted != nil && *item.Deleted {
					continue
				}
				if pass == 1 {
					return fmt.Errorf("repository still contains live blobs, snapshots or versions; retry cleanup")
				}
				blobClient := client.ServiceClient().NewContainerClient(target.Container).NewBlobClient(*item.Name)
				deleteOptions := &blob.DeleteOptions{DeleteSnapshots: to.Ptr(blob.DeleteSnapshotsOptionTypeInclude)}
				snapshot := item.Snapshot != nil && *item.Snapshot != ""
				version := item.VersionID != nil && *item.VersionID != ""
				current := item.IsCurrentVersion != nil && *item.IsCurrentVersion
				// A current version cannot be deleted by version ID until the base
				// blob is deleted. Count both normal API calls against the budget.
				if !snapshot && version && current {
					if deletes == maxDeletes {
						return fmt.Errorf("repository deletion budget exhausted; retry cleanup")
					}
					deletes++
					_, err = blobClient.Delete(ctx, deleteOptions)
					if err != nil && !missing(err, bloberror.BlobNotFound, bloberror.ContainerNotFound) {
						return fmt.Errorf("delete current repository blob: %w", err)
					}
				}
				err = nil
				switch {
				case snapshot:
					blobClient, err = blobClient.WithSnapshot(*item.Snapshot)
					deleteOptions = nil
				case version:
					blobClient, err = blobClient.WithVersionID(*item.VersionID)
					deleteOptions = nil
				}
				if err != nil {
					return fmt.Errorf("select repository blob snapshot/version: %w", err)
				}
				if deletes == maxDeletes {
					return fmt.Errorf("repository deletion budget exhausted; retry cleanup")
				}
				deletes++
				_, err = blobClient.Delete(ctx, deleteOptions)
				if err != nil && !missing(err, bloberror.BlobNotFound, bloberror.ContainerNotFound) {
					return fmt.Errorf("delete repository blob: %w", err)
				}
			}
		}
	}
	return nil
}

// A status alone is insufficient: an account/endpoint 404 is not proof that the
// container is absent. Likewise a misleading code on a 403 must not mean success.
func missing(err error, codes ...bloberror.Code) bool {
	var responseError *azcore.ResponseError
	return errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound && bloberror.HasCode(err, codes...)
}
