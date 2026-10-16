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
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type ClusterPatchRejection struct {
	StatusCode int
	Code       string
	Target     string
	Message    string
	Details    []PatchRejectionDetail
}

type PatchRejectionDetail struct {
	Code    string `json:"code"`
	Target  string `json:"target"`
	Message string `json:"message"`
}

type GuardedNegativeClusterClient struct {
	manifest     SharedClusterManifest
	manifestPath string
	client       *azcorearm.Client
}

func SharedGuardedNegativeCluster(ctx context.Context) (SharedClusterManifest, *GuardedNegativeClusterClient, error) {
	manifest, _, err := SharedReadOnlyCluster(ctx)
	if err != nil {
		return manifest, nil, err
	}
	invocation, err := newInvocationContext()
	if err != nil {
		return manifest, nil, err
	}
	credential, err := invocation.getAzureCredentials()
	if err != nil {
		return manifest, nil, err
	}
	client, err := azcorearm.NewClient("shared-cluster-guard", "v0.0.0", credential, invocation.getHCPClientFactoryOptions())
	if err != nil {
		return manifest, nil, err
	}
	return manifest, &GuardedNegativeClusterClient{manifest: manifest, manifestPath: os.Getenv(SharedFixtureManifestEnv), client: client}, nil
}

func (client *GuardedNegativeClusterClient) RejectPatch20240610(ctx context.Context, patch map[string]any, expected ClusterPatchRejection) error {
	return client.rejectPatch20240610(ctx, client.manifest.ClusterResourceID, patch, expected)
}

func (client *GuardedNegativeClusterClient) RejectNodePoolPatch20240610(ctx context.Context, patch map[string]any, expected ClusterPatchRejection) error {
	return client.rejectPatch20240610(ctx, client.manifest.NodePoolResourceID, patch, expected)
}

func (client *GuardedNegativeClusterClient) rejectPatch20240610(ctx context.Context, resourceID string, patch map[string]any, expected ClusterPatchRejection) (retErr error) {
	if expected.StatusCode < 400 || expected.StatusCode >= 500 || expected.Code == "" || (expected.Target == "" && len(expected.Details) == 0) || expected.Message == "" {
		return fmt.Errorf("guarded PATCH requires a specific client-error status, code, target, and message")
	}
	for _, detail := range expected.Details {
		if detail.Code == "" || detail.Target == "" || detail.Message == "" {
			return fmt.Errorf("guarded PATCH error details require a code, target, and message")
		}
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
	before, err := client.snapshot20240610(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("capture fixture resource %s before rejected PATCH: %w", resourceID, err)
	}
	verified := false
	defer func() {
		if !verified {
			manifest.Ready = false
			retErr = errors.Join(retErr, WriteSharedClusterManifest(client.manifestPath, manifest))
		}
	}()
	response, patchErr := client.request20240610(ctx, resourceID, http.MethodPatch, patch)
	var responseError *azcore.ResponseError
	if !errors.As(patchErr, &responseError) || responseError.StatusCode != expected.StatusCode || responseError.ErrorCode != expected.Code {
		retErr = fmt.Errorf("PATCH must return typed HTTP %d / %s, got %v", expected.StatusCode, expected.Code, patchErr)
	} else {
		var envelope struct {
			Error struct {
				Target  string                 `json:"target"`
				Message string                 `json:"message"`
				Details []PatchRejectionDetail `json:"details"`
			} `json:"error"`
		}
		if err := runtime.UnmarshalAsJSON(response, &envelope); err != nil {
			retErr = fmt.Errorf("decode rejected PATCH: %w", err)
		} else if envelope.Error.Target != expected.Target || !strings.Contains(envelope.Error.Message, expected.Message) {
			retErr = fmt.Errorf("PATCH rejection must target %q with %q, got target %q and message %q", expected.Target, expected.Message, envelope.Error.Target, envelope.Error.Message)
		} else if len(envelope.Error.Details) != len(expected.Details) {
			retErr = fmt.Errorf("PATCH rejection must contain %d error details, got %d", len(expected.Details), len(envelope.Error.Details))
		} else {
			for _, wanted := range expected.Details {
				found := false
				for _, actual := range envelope.Error.Details {
					if actual.Code == wanted.Code && actual.Target == wanted.Target && strings.Contains(actual.Message, wanted.Message) {
						found = true
						break
					}
				}
				if !found {
					retErr = errors.Join(retErr, fmt.Errorf("PATCH rejection missing detail %s / %s / %q", wanted.Code, wanted.Target, wanted.Message))
				}
			}
		}
	}
	after, err := client.snapshot20240610(ctx, resourceID)
	if err != nil {
		retErr = errors.Join(retErr, fmt.Errorf("capture fixture resource %s after rejected PATCH: %w", resourceID, err))
	} else if !reflect.DeepEqual(before, after) {
		retErr = errors.Join(retErr, fmt.Errorf("shared fixture resource %s changed after guarded PATCH; fixture quarantined", resourceID))
	}
	verified = retErr == nil
	return retErr
}

func (client *GuardedNegativeClusterClient) snapshot20240610(ctx context.Context, resourceID string) (map[string]any, error) {
	response, err := client.request20240610(ctx, resourceID, http.MethodGet, nil)
	if err != nil {
		return nil, err
	}
	var snapshot map[string]any
	if err := runtime.UnmarshalAsJSON(response, &snapshot); err != nil {
		return nil, err
	}
	if snapshot == nil || !strings.EqualFold(fmt.Sprint(snapshot["id"]), resourceID) {
		return nil, fmt.Errorf("GET did not return the fixture resource ID %s", resourceID)
	}
	return snapshot, nil
}

func (client *GuardedNegativeClusterClient) request20240610(ctx context.Context, resourceID, method string, body map[string]any) (*http.Response, error) {
	endpoint := strings.TrimRight(client.client.Endpoint(), "/") + resourceID + "?api-version=2024-06-10-preview"
	request, err := runtime.NewRequest(ctx, method, endpoint)
	if err != nil {
		return nil, err
	}
	if body != nil {
		if err := runtime.MarshalAsJSON(request, body); err != nil {
			return nil, err
		}
	}
	response, err := client.client.Pipeline().Do(request)
	if err != nil {
		return response, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, runtime.NewResponseError(response)
	}
	if method == http.MethodGet && response.StatusCode != http.StatusOK {
		return response, fmt.Errorf("fixture resource GET returned HTTP %d instead of 200", response.StatusCode)
	}
	_, err = runtime.Payload(response)
	return response, err
}
