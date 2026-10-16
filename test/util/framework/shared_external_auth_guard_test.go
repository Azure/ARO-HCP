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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gofrs/flock"
)

const invalidIssuerResponse = `{"error":{"code":"InvalidRequestContent","target":"properties.issuer.url","message":"Invalid value: invalid-url: must be https URL"}}`

func externalAuthTestResponse(status int, body string) (*http.Response, error) {
	response, err := jobIDResponse(body)
	response.StatusCode = status
	return response, err
}

func TestGuardedExternalAuthContract(t *testing.T) {
	for _, scenario := range []string{"rejected", "wrong code", "wrong target", "wrong message", "wrong status", "transport failure", "delayed creation", "delayed accepted", "accepted", "partial creation", "parent changed", "untyped absence", "wrong absence code"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				manifest := publishedTestManifest(t)
				childID := manifest.ClusterResourceID + "/externalAuths/invalid-test"
				writes, deletes, parentReads, childReads := 0, 0, 0, 0
				present := false
				putStarted := time.Now()
				var lastPresent time.Time
				client := testGuardedClusterClient(t, manifest, &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
					if request.URL.Query().Get("api-version") != "2024-06-10-preview" {
						t.Errorf("wrong API version: %s", request.URL)
					}
					if request.URL.Path == manifest.ClusterResourceID && request.Method == http.MethodGet {
						parentReads++
						state := "Succeeded"
						if scenario == "parent changed" && parentReads > 1 {
							state = "Updating"
						}
						return jobIDResponse(fmt.Sprintf(`{"id":%q,"properties":{"provisioningState":%q}}`, manifest.ClusterResourceID, state))
					}
					if request.URL.Path != childID {
						t.Fatalf("request escaped owned child: %s %s", request.Method, request.URL)
					}
					switch request.Method {
					case http.MethodPut:
						writes++
						putStarted = time.Now()
						var body map[string]any
						if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						if body["properties"].(map[string]any)["issuer"].(map[string]any)["url"] != "invalid-url" {
							t.Fatalf("wrong invalid issuer body: %v", body)
						}
						responseBody := invalidIssuerResponse
						status := http.StatusBadRequest
						switch scenario {
						case "wrong code":
							responseBody = strings.ReplaceAll(responseBody, "InvalidRequestContent", "Forbidden")
						case "wrong target":
							responseBody = strings.ReplaceAll(responseBody, "properties.issuer.url", "name")
						case "wrong message":
							responseBody = strings.ReplaceAll(responseBody, "must be https URL", "other error")
						case "wrong status":
							status = http.StatusInternalServerError
						case "transport failure", "delayed creation":
							return nil, errors.New("connection interrupted")
						case "delayed accepted":
							status = http.StatusAccepted
						case "accepted":
							present = true
							status = http.StatusCreated
						case "partial creation":
							present = true
						}
						return externalAuthTestResponse(status, responseBody)
					case http.MethodDelete:
						deletes++
						lastPresent = time.Now()
						present = false
						return externalAuthTestResponse(http.StatusAccepted, `{}`)
					case http.MethodGet:
						childReads++
						if deletes == 0 && time.Since(putStarted) >= ExternalAuthCreationTimeout-externalAuthCleanupPollInterval && (scenario == "delayed creation" || scenario == "delayed accepted") {
							present = true
						}
						if present {
							return jobIDResponse(fmt.Sprintf(`{"id":%q}`, childID))
						}
						if writes > 0 && childReads == 2 {
							if scenario == "untyped absence" {
								return nil, errors.New("404 ResourceNotFound")
							}
							if scenario == "wrong absence code" {
								return externalAuthTestResponse(http.StatusNotFound, `{"error":{"code":"SubscriptionNotFound"}}`)
							}
						}
						return externalAuthTestResponse(http.StatusNotFound, `{"error":{"code":"ResourceNotFound"}}`)
					default:
						t.Fatalf("unexpected method: %s", request.Method)
						return nil, errors.New("unexpected method")
					}
				}})
				started := time.Now()
				err := client.RejectExternalAuthIssuer20240610(t.Context(), "invalid-test", "invalid-url")
				wantError := scenario != "rejected"
				if (err != nil) != wantError || writes != 1 || parentReads != 2 || childReads < 2 || present {
					t.Fatalf("contract violated: err=%v writes=%d parentReads=%d childReads=%d present=%v", err, writes, parentReads, childReads, present)
				}
				if (scenario == "accepted" || scenario == "partial creation" || scenario == "delayed creation" || scenario == "delayed accepted") != (deletes == 1) {
					t.Fatalf("unexpected partial-child cleanup: %d deletes", deletes)
				}
				if scenario == "rejected" && childReads != 2 {
					t.Fatalf("typed rejection must retain the immediate before/after checks: %d reads", childReads)
				}
				if scenario == "transport failure" && (childReads < 3 || time.Since(started) < externalAuthAbsenceStabilityWindow) {
					t.Fatalf("uncertain PUT must observe the full absence window: %d reads in %v", childReads, time.Since(started))
				}
				if (scenario == "delayed creation" || scenario == "delayed accepted") && time.Since(lastPresent) < externalAuthAbsenceStabilityWindow {
					t.Fatalf("delayed child deletion must be followed by stable absence: %v", time.Since(lastPresent))
				}
				if _, err := ReadSharedClusterManifest(); (err != nil) != wantError {
					t.Fatalf("fixture quarantine mismatch: %v", err)
				}
			})
		})
	}
}

func TestGuardedExternalAuthPreconditions(t *testing.T) {
	for _, scenario := range []string{"locked", "missing manifest", "changed manifest", "existing child", "invalid name"} {
		t.Run(scenario, func(t *testing.T) {
			manifest := publishedTestManifest(t)
			writes := 0
			client := testGuardedClusterClient(t, manifest, &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet {
					writes++
				}
				return jobIDResponse(fmt.Sprintf(`{"id":%q}`, manifest.ClusterResourceID))
			}})
			name := "invalid-test"
			switch scenario {
			case "locked":
				lock := flock.New(client.manifestPath + ".guarded-write.lock")
				if err := lock.Lock(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := lock.Unlock(); err != nil {
						t.Error(err)
					}
				})
			case "missing manifest":
				if err := os.Remove(client.manifestPath); err != nil {
					t.Fatal(err)
				}
			case "changed manifest":
				manifest.ExpiresAt = manifest.ExpiresAt.Add(time.Minute)
				if err := WriteSharedClusterManifest(client.manifestPath, manifest); err != nil {
					t.Fatal(err)
				}
			case "invalid name":
				name = "../other"
			}
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			if err := client.RejectExternalAuthIssuer20240610(ctx, name, "invalid-url"); err == nil || writes != 0 {
				t.Fatalf("unsafe precondition allowed write: err=%v writes=%d", err, writes)
			}
		})
	}
}

func TestGuardedExternalAuthAbsenceWindow(t *testing.T) {
	for _, scenario := range []string{"interrupted absence", "never stable"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				manifest := publishedTestManifest(t)
				reads := 0
				client := testGuardedClusterClient(t, manifest, &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
					if request.Method != http.MethodGet {
						t.Fatalf("absence checks must not write: %s", request.Method)
					}
					reads++
					if reads == 3 || (scenario == "never stable" && reads%2 == 0) {
						return nil, errors.New("transient failure")
					}
					return externalAuthTestResponse(http.StatusNotFound, `{"error":{"code":"ResourceNotFound"}}`)
				}})
				ctx, cancel := context.WithTimeout(t.Context(), externalAuthCleanupTimeout)
				defer cancel()
				started := time.Now()
				err := client.cleanupGuardedExternalAuth20240610(ctx, manifest.ClusterResourceID+"/externalAuths/invalid-test", externalAuthAbsenceStabilityWindow)
				if scenario == "never stable" {
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("intermittent absence must time out: %v", err)
					}
				} else if err != nil || time.Since(started) < externalAuthAbsenceStabilityWindow+3*externalAuthCleanupPollInterval {
					t.Fatalf("transient errors must restart the absence window: elapsed=%v err=%v", time.Since(started), err)
				}
			})
		})
	}
}

func TestGuardedExternalAuthCleanupTimeout(t *testing.T) {
	manifest := publishedTestManifest(t)
	client := testGuardedClusterClient(t, manifest, &fakeTransport{do: func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transient failure")
	}})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err := client.cleanupGuardedExternalAuth20240610(ctx, manifest.ClusterResourceID+"/externalAuths/invalid-test", externalAuthAbsenceStabilityWindow)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "transient failure") {
		t.Fatalf("cleanup must be bounded and report last observation: %v", err)
	}
}
