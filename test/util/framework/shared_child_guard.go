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

func (client *GuardedNegativeClusterClient) rejectChildCreate20240610(ctx context.Context, childType, name string, body map[string]any, target, message string) (retErr error) {
	if childType != "externalAuths" && childType != "nodePools" {
		return fmt.Errorf("unsupported guarded child type %q", childType)
	}
	if !regexp.MustCompile(`^[a-z]([-a-z0-9]{0,13}[a-z0-9])?$`).MatchString(name) {
		return fmt.Errorf("child name must be a safe, unique name of at most 15 characters")
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
		return fmt.Errorf("capture cluster before rejected %s PUT: %w", childType, err)
	}
	resourceID := manifest.ClusterResourceID + "/" + childType + "/" + name
	_, err = client.request20240610(ctx, resourceID, http.MethodGet, nil)
	if !isGuardedChildNotFound(err) {
		return fmt.Errorf("refusing to own child %s without initial typed ResourceNotFound: %v", resourceID, err)
	}
	verified := false
	defer func() {
		if !verified {
			manifest.Ready = false
			retErr = errors.Join(retErr, WriteSharedClusterManifest(client.manifestPath, manifest))
		}
	}()
	response, putErr := client.request20240610(ctx, resourceID, http.MethodPut, body)
	var responseError *azcore.ResponseError
	if !errors.As(putErr, &responseError) || responseError.StatusCode != http.StatusBadRequest || responseError.ErrorCode != "InvalidRequestContent" {
		retErr = fmt.Errorf("%s PUT must return typed HTTP 400 / InvalidRequestContent, got %v", resourceID, putErr)
	} else {
		var envelope struct {
			Error struct {
				Target  string                 `json:"target"`
				Message string                 `json:"message"`
				Details []PatchRejectionDetail `json:"details"`
			} `json:"error"`
		}
		if err := runtime.UnmarshalAsJSON(response, &envelope); err != nil {
			retErr = fmt.Errorf("decode rejected %s PUT: %w", resourceID, err)
		} else if envelope.Error.Target != target || !strings.Contains(envelope.Error.Message, message) || len(envelope.Error.Details) != 0 {
			retErr = fmt.Errorf("%s PUT must reject only %s with %q, got %+v", resourceID, target, message, envelope.Error)
		}
	}
	_, err = client.request20240610(ctx, resourceID, http.MethodGet, nil)
	if !isGuardedChildNotFound(err) {
		retErr = errors.Join(retErr, fmt.Errorf("child %s must remain absent after rejected PUT, got %v", resourceID, err))
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		retErr = errors.Join(retErr, client.cleanupGuardedChild20240610(cleanupCtx, resourceID))
	}
	after, err := client.snapshot20240610(ctx, manifest.ClusterResourceID)
	if err != nil {
		retErr = errors.Join(retErr, fmt.Errorf("capture cluster after rejected %s PUT: %w", childType, err))
	} else if !reflect.DeepEqual(before, after) {
		retErr = errors.Join(retErr, fmt.Errorf("shared cluster changed after rejected %s PUT", childType))
	}
	verified = retErr == nil
	return retErr
}

func isGuardedChildNotFound(err error) bool {
	var responseError *azcore.ResponseError
	return IsNotFoundError(err) && errors.As(err, &responseError) && responseError.ErrorCode == "ResourceNotFound"
}

func (client *GuardedNegativeClusterClient) cleanupGuardedChild20240610(ctx context.Context, resourceID string) error {
	lastObservation := ""
	err := wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
		_, getErr := client.request20240610(ctx, resourceID, http.MethodGet, nil)
		if isGuardedChildNotFound(getErr) {
			return true, nil
		}
		observation := fmt.Sprintf("GET: %v", getErr)
		if getErr == nil {
			_, deleteErr := client.request20240610(ctx, resourceID, http.MethodDelete, nil)
			observation += fmt.Sprintf("; DELETE: %v", deleteErr)
		}
		if observation != lastObservation {
			klog.FromContext(ctx).Info("Cleaning up unexpected shared fixture child", "resourceID", resourceID, "observation", observation)
			lastObservation = observation
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("cleanup child %s: %w; last observation: %s", resourceID, err, lastObservation)
	}
	return nil
}
