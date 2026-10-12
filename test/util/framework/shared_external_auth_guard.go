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
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

const (
	externalAuthAbsenceStabilityWindow = ExternalAuthCreationTimeout
	externalAuthCleanupTimeout         = 2*ExternalAuthCreationTimeout + ExternalAuthDeletionTimeout
	externalAuthCleanupPollInterval    = 2 * time.Second
)

func (client *GuardedNegativeClusterClient) RejectExternalAuthIssuer20240610(ctx context.Context, name, issuer string) (retErr error) {
	if !regexp.MustCompile(`^[a-z]([-a-z0-9]{0,13}[a-z0-9])?$`).MatchString(name) {
		return fmt.Errorf("external auth child name must be a safe, unique name of at most 15 characters")
	}
	lock := flock.New(client.manifestPath + ".guarded-write.lock")
	locked, err := lock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("lock shared cluster: %w", err)
	}
	if !locked {
		return fmt.Errorf("lock shared cluster: %w", ctx.Err())
	}
	defer func() { retErr = errors.Join(retErr, lock.Unlock()) }()
	manifest, err := ReadSharedClusterManifest()
	if err != nil {
		return err
	}
	if manifest != client.manifest || os.Getenv(SharedFixtureManifestEnv) != client.manifestPath {
		return fmt.Errorf("shared cluster manifest changed since borrowing")
	}
	before, err := client.snapshot20240610(ctx, manifest.ClusterResourceID)
	if err != nil {
		return fmt.Errorf("capture cluster before rejected external auth PUT: %w", err)
	}
	resourceID := manifest.ClusterResourceID + "/externalAuths/" + name
	_, err = client.request20240610(ctx, resourceID, http.MethodGet, nil)
	if !isExternalAuthNotFound(err) {
		return fmt.Errorf("refusing to own external auth %s without initial typed ResourceNotFound: %v", resourceID, err)
	}
	verified := false
	defer func() {
		if !verified {
			manifest.Ready = false
			retErr = errors.Join(retErr, WriteSharedClusterManifest(client.manifestPath, manifest))
		}
	}()
	body := map[string]any{"properties": map[string]any{
		"issuer": map[string]any{"url": issuer, "audiences": []string{"shared-negative-client"}},
		"claim":  map[string]any{"mappings": map[string]any{"username": map[string]any{"claim": "sub", "prefixPolicy": "NoPrefix"}}},
	}}
	response, putErr := client.request20240610(ctx, resourceID, http.MethodPut, body)
	var responseError *azcore.ResponseError
	if !errors.As(putErr, &responseError) || responseError.StatusCode != http.StatusBadRequest || responseError.ErrorCode != "InvalidRequestContent" {
		retErr = fmt.Errorf("external auth PUT must return typed HTTP 400 / InvalidRequestContent, got %v", putErr)
	} else {
		var envelope struct {
			Error struct {
				Target  string                 `json:"target"`
				Message string                 `json:"message"`
				Details []PatchRejectionDetail `json:"details"`
			} `json:"error"`
		}
		if err := runtime.UnmarshalAsJSON(response, &envelope); err != nil {
			retErr = fmt.Errorf("decode rejected external auth PUT: %w", err)
		} else if envelope.Error.Target != "properties.issuer.url" || !strings.Contains(envelope.Error.Message, "must be https URL") || len(envelope.Error.Details) != 0 {
			retErr = fmt.Errorf("external auth PUT must reject only properties.issuer.url with 'must be https URL', got %+v", envelope.Error)
		}
	}
	uncertainPut := retErr != nil
	_, err = client.request20240610(ctx, resourceID, http.MethodGet, nil)
	if !isExternalAuthNotFound(err) {
		retErr = errors.Join(retErr, fmt.Errorf("external auth %s must remain absent after rejected PUT, got %v", resourceID, err))
	}
	if uncertainPut || !isExternalAuthNotFound(err) {
		absenceWindow := time.Duration(0)
		if uncertainPut {
			absenceWindow = externalAuthAbsenceStabilityWindow
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), externalAuthCleanupTimeout)
		defer cancel()
		retErr = errors.Join(retErr, client.cleanupGuardedExternalAuth20240610(cleanupCtx, resourceID, absenceWindow))
	}
	after, err := client.snapshot20240610(ctx, manifest.ClusterResourceID)
	if err != nil {
		retErr = errors.Join(retErr, fmt.Errorf("capture cluster after rejected external auth PUT: %w", err))
	} else if !reflect.DeepEqual(before, after) {
		retErr = errors.Join(retErr, fmt.Errorf("shared cluster changed after rejected external auth PUT"))
	}
	verified = retErr == nil
	return retErr
}

func isExternalAuthNotFound(err error) bool {
	var responseError *azcore.ResponseError
	return IsNotFoundError(err) && errors.As(err, &responseError) && responseError.ErrorCode == "ResourceNotFound"
}

func (client *GuardedNegativeClusterClient) cleanupGuardedExternalAuth20240610(ctx context.Context, resourceID string, absenceWindow time.Duration) error {
	lastObservation := ""
	var absentSince time.Time
	err := wait.PollUntilContextCancel(ctx, externalAuthCleanupPollInterval, true, func(ctx context.Context) (bool, error) {
		_, getErr := client.request20240610(ctx, resourceID, http.MethodGet, nil)
		observation := fmt.Sprintf("GET: %v", getErr)
		if isExternalAuthNotFound(getErr) {
			if absentSince.IsZero() {
				absentSince = time.Now()
			}
			if time.Since(absentSince) >= absenceWindow {
				return true, nil
			}
			observation = "GET: typed ResourceNotFound; waiting for stable absence"
		} else {
			absentSince = time.Time{}
			if getErr == nil {
				_, deleteErr := client.request20240610(ctx, resourceID, http.MethodDelete, nil)
				observation += fmt.Sprintf("; DELETE: %v", deleteErr)
			}
		}
		if observation != lastObservation {
			klog.FromContext(ctx).Info("Cleaning up unexpected external auth child", "resourceID", resourceID, "observation", observation)
			lastObservation = observation
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("cleanup external auth %s: %w; last observation: %s", resourceID, err, lastObservation)
	}
	return nil
}
