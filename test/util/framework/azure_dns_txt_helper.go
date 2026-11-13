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

package framework

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
)

// PresentAzureDNSTXT adds one TXT value without replacing existing values or
// metadata. The returned cleanup removes only the added value, preserving later
// changes by other writers. If the value already existed, cleanup is a no-op.
// Callers should register any returned cleanup even when err is non-nil: a failed
// response can follow a successful Azure write. ETags protect concurrent updates.
func PresentAzureDNSTXT(ctx context.Context, client *armdns.RecordSetsClient, resourceGroup, zone, recordName, value string) (cleanup func(context.Context) error, err error) {
	if value == "" {
		return nil, errors.New("DNS TXT validation value must not be empty")
	}
	owned := false
	cleanup = func(ctx context.Context) error {
		if !owned {
			return nil
		}
		return changeAzureDNSTXT(ctx, client, resourceGroup, zone, recordName, value, true, nil)
	}
	err = changeAzureDNSTXT(ctx, client, resourceGroup, zone, recordName, value, false, &owned)
	return cleanup, err
}

func changeAzureDNSTXT(ctx context.Context, client *armdns.RecordSetsClient, resourceGroup, zone, recordName, value string, remove bool, owned *bool) error {
	var lastErr error
	err := wait.PollUntilContextTimeout(ctx, time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		response, err := client.Get(ctx, resourceGroup, zone, recordName, armdns.RecordTypeTXT, nil)
		missing := azureDNSStatus(err, 404)
		if err != nil && !missing {
			return false, err
		}
		if missing && remove {
			return true, nil
		}
		record := response.RecordSet
		options := &armdns.RecordSetsClientCreateOrUpdateOptions{}
		if missing {
			record = armdns.RecordSet{Properties: &armdns.RecordSetProperties{TTL: to.Ptr[int64](60)}}
			options.IfNoneMatch = to.Ptr("*")
		} else {
			if record.Etag == nil || *record.Etag == "" || record.Properties == nil {
				return false, fmt.Errorf("DNS TXT %s.%s has no ETag or properties", recordName, zone)
			}
			options.IfMatch = record.Etag
		}
		var kept []*armdns.TxtRecord
		found := false
		for _, txt := range record.Properties.TxtRecords {
			var joined strings.Builder
			if txt != nil {
				for _, part := range txt.Value {
					if part != nil {
						joined.WriteString(*part)
					}
				}
			}
			if joined.String() == value {
				found = true
				if remove {
					continue
				}
			}
			kept = append(kept, txt)
		}
		if (remove && !found) || (!remove && found) {
			return true, nil
		}
		if remove && len(kept) == 0 {
			_, err = client.Delete(ctx, resourceGroup, zone, recordName, armdns.RecordTypeTXT, &armdns.RecordSetsClientDeleteOptions{IfMatch: record.Etag})
		} else {
			if !remove {
				kept = append(kept, &armdns.TxtRecord{Value: []*string{to.Ptr(value)}})
				*owned = true
			}
			record.Properties.TxtRecords = kept
			_, err = client.CreateOrUpdate(ctx, resourceGroup, zone, recordName, armdns.RecordTypeTXT, record, options)
		}
		lastErr = err
		if azureDNSStatus(err, 409) || azureDNSStatus(err, 412) {
			if !remove {
				*owned = false
			}
			return false, nil
		}
		if remove && azureDNSStatus(err, 404) {
			return true, nil
		}
		return err == nil, err
	})
	if err != nil {
		return fmt.Errorf("updating DNS TXT %s in zone %s/%s (remove=%t, last Azure error=%v): %w", recordName, resourceGroup, zone, remove, lastErr, err)
	}
	return nil
}

func azureDNSStatus(err error, status int) bool {
	var responseErr *azcore.ResponseError
	return errors.As(err, &responseErr) && responseErr.StatusCode == status
}
