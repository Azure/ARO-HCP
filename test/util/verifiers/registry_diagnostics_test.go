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

package verifiers

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
)

func TestRegistryAndPodDiagnosticsPreserveImageVerification(t *testing.T) {
	for _, scenario := range []string{"success", "observations", "collection errors", "write errors"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			apiFailure := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "pod", errors.New("access denied"))
			config := &rest.Config{Host: "https://api.example", Transport: registryRoundTripper(func(*http.Request) (*http.Response, error) {
				if scenario != "success" {
					cancel()
					return nil, apiFailure
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"apiVersion":"v1","kind":"PodList","items":[{"status":{"containerStatuses":[{"image":"registry.example/image","imageID":"sha256:observed"}]}}]}`))}, nil
			})}
			primary := VerifyImagePulled("namespace", "registry.example", "image", time.Second).Verify(ctx, config)
			client := fake.NewClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "namespace", UID: "pod-uid"}})
			if scenario == "collection errors" {
				client.PrependReactor("*", "*", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("collection unavailable")
				})
			}
			directory := t.TempDir()
			if scenario == "write errors" {
				require.NoError(t, os.WriteFile(filepath.Join(directory, "pod-diagnostics"), nil, 0600))
			}
			requests := 0
			transport := registryRoundTripper(func(request *http.Request) (*http.Response, error) {
				requests++
				require.NoError(t, request.Context().Err())
				if scenario == "collection errors" {
					return nil, errors.New("probe unavailable")
				}
				return &http.Response{StatusCode: 503, Header: http.Header{}, Body: &registryUnreadBody{testing: t}}, nil
			})
			var console bytes.Buffer
			ginkgo.GinkgoWriter.TeeTo(&console)
			defer ginkgo.GinkgoWriter.ClearTeeWriters()
			original := primary
			require.NotPanics(t, func() {
				CapturePodFailureDiagnostics(ctx, primary, client, "namespace", "pod", directory)
				captureRegistryFailureDiagnostics(ctx, primary, "registry.example/image", "namespace", "pod", directory, transport, time.Second)
			})
			require.Equal(t, original, primary)
			if scenario == "success" {
				require.NoError(t, primary)
				require.Empty(t, client.Actions())
				require.Zero(t, requests)
				require.Empty(t, console.String())
				return
			}
			require.ErrorIs(t, primary, apiFailure)
			var statusError *apierrors.StatusError
			require.ErrorAs(t, primary, &statusError)
			require.Same(t, apiFailure, statusError)
			require.Equal(t, original.Error(), primary.Error())
			t.Logf("preserved verification output: %s", primary)
			require.NotEmpty(t, client.Actions())
			require.Positive(t, requests)
			require.Equal(t, 1, strings.Count(console.String(), "[registry diagnostics]"))
			if scenario == "observations" {
				for _, name := range []string{"pod.yaml", "events.yaml", "registry.json"} {
					require.FileExists(t, filepath.Join(directory, "pod-diagnostics", "namespace", "pod", name))
				}
			}
		})
	}
}

type registryRoundTripper func(*http.Request) (*http.Response, error)

func (transport registryRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestRegistryServiceIdentifier(t *testing.T) {
	for _, service := range []string{"container_registry", "_registry", "registry.example:443"} {
		t.Run(service, func(t *testing.T) {
			calls := 0
			transport := registryRoundTripper(func(request *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{StatusCode: 401, Header: http.Header{"Www-Authenticate": {fmt.Sprintf(`Bearer realm="https://auth.example/token", service="%s", scope="repository:team/image:pull"`, service)}}, Body: http.NoBody}, nil
				}
				require.Equal(t, "/token", request.URL.Path)
				require.Equal(t, service, request.URL.Query().Get("service"))
				require.Equal(t, url.Values{"service": {service}, "scope": {"repository:team/image:pull"}}.Encode(), request.URL.RawQuery)
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: http.NoBody}, nil
			})
			report := collectRegistryDiagnostics(context.Background(), "registry.example/team/image", transport)
			require.Equal(t, 2, calls)
			require.Equal(t, "bearer", report.Challenge)
			data, err := json.Marshal(report)
			require.NoError(t, err)
			require.NotContains(t, string(data), service)
		})
	}
	for _, service := range []string{"container_registry&account=secret", "container_registry?token=secret", "container_registry/user", "container registry"} {
		target, _ := registryChallenge([]string{fmt.Sprintf(`Bearer realm="https://auth.example/token", service="%s"`, service)}, "team/image")
		require.Nil(t, target)
	}
}

func TestRegistryObservationsPreserveVerificationFailure(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		status     int
		requestErr error
		wait       bool
		writeError bool
		outcome    string
	}{
		{name: "unavailable response", status: 503, outcome: "http-response"},
		{name: "anonymous rejection", status: 401, outcome: "http-response"},
		{name: "anonymous forbidden", status: 403, outcome: "http-response"},
		{name: "transport", requestErr: errors.New("transport detail not for logging"), outcome: "transport-error"},
		{name: "probe timeout", wait: true, outcome: "time-budget-ended"},
		{name: "artifact write", status: 503, writeError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var console bytes.Buffer
			ginkgo.GinkgoWriter.TeeTo(&console)
			defer ginkgo.GinkgoWriter.ClearTeeWriters()
			directory := t.TempDir()
			if scenario.writeError {
				require.NoError(t, os.WriteFile(filepath.Join(directory, "pod-diagnostics"), nil, 0600))
			}
			observation := errors.New("image not observed")
			primary := fmt.Errorf("image pull not confirmed; cause undetermined; observation: %w", observation)
			before := primary.Error()
			calls := 0
			transport := registryRoundTripper(func(request *http.Request) (*http.Response, error) {
				calls++
				if scenario.wait {
					<-request.Context().Done()
					return nil, request.Context().Err()
				}
				if scenario.requestErr != nil {
					return nil, scenario.requestErr
				}
				return &http.Response{StatusCode: scenario.status, Header: http.Header{}, Body: &registryUnreadBody{testing: t}}, nil
			})
			require.NotPanics(t, func() {
				captureRegistryFailureDiagnostics(context.Background(), primary, "registry.example/team/image", "namespace", "pod", directory, transport, 10*time.Millisecond)
			})
			require.Equal(t, before, primary.Error())
			require.ErrorIs(t, primary, observation)
			require.Positive(t, calls)
			require.Equal(t, 1, strings.Count(console.String(), "\n"))
			require.NotContains(t, console.String(), before)
			if scenario.writeError {
				require.Equal(t, "[registry diagnostics] collection unavailable; verification result unchanged\n", console.String())
				return
			}
			data, err := os.ReadFile(filepath.Join(directory, "pod-diagnostics", "namespace", "pod", "registry.json"))
			require.NoError(t, err)
			var report map[string]any
			require.NoError(t, json.Unmarshal(data, &report))
			require.Equal(t, "undetermined", report["root_cause"])
			require.Contains(t, report["interpretation"], "do not determine the primary verification outcome or its root cause")
			require.Contains(t, string(data), scenario.outcome)
			require.NotContains(t, string(data), before)
			require.Contains(t, console.String(), "observations (cause undetermined): pod-diagnostics/")
		})
	}
}

type registryUnreadBody struct {
	testing *testing.T
	closed  bool
}

func (body *registryUnreadBody) Read([]byte) (int, error) {
	body.testing.Error("response body must not be read")
	return 0, io.EOF
}
func (body *registryUnreadBody) Close() error { body.closed = true; return nil }

type registryTraceCloseBody struct {
	*registryUnreadBody
	closeTrace func()
}

func (body *registryTraceCloseBody) Close() error {
	body.closeTrace()
	return body.registryUnreadBody.Close()
}

func TestRegistryRequestConnectTiming(t *testing.T) {
	type event struct {
		at               int
		network, address string
		start            bool
	}
	for _, scenario := range []struct {
		name       string
		events     []event
		wantMillis float64
	}{
		{"overlapping addresses", []event{
			{0, "tcp", "first:443", true},
			{20, "tcp", "second:443", true},
			{40, "tcp", "first:443", false},
			{60, "tcp", "second:443", false},
		}, 80},
		{"same address different networks", []event{
			{0, "tcp4", "same:443", true},
			{20, "tcp6", "same:443", true},
			{40, "tcp4", "same:443", false},
			{60, "tcp6", "same:443", false},
		}, 80},
		{"sequential attempts", []event{
			{0, "tcp", "same:443", true},
			{10, "tcp", "same:443", false},
			{20, "tcp", "same:443", true},
			{45, "tcp", "same:443", false},
		}, 35},
		{"unmatched completion", []event{{10, "tcp", "unknown:443", false}}, 0},
		{"completed start removed", []event{
			{0, "tcp", "same:443", true},
			{10, "tcp", "same:443", false},
			{20, "tcp", "same:443", false},
		}, 10},
		{"unfinished attempt", []event{{0, "tcp", "same:443", true}}, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			instant := time.Unix(100, 0)
			body := &registryUnreadBody{testing: t}
			client := &http.Client{Transport: registryRoundTripper(func(request *http.Request) (*http.Response, error) {
				trace := httptrace.ContextClientTrace(request.Context())
				for _, callback := range scenario.events {
					instant = time.Unix(100, 0).Add(time.Duration(callback.at) * time.Millisecond)
					if callback.start {
						trace.ConnectStart(callback.network, callback.address)
					} else {
						trace.ConnectDone(callback.network, callback.address, errors.New("attempt failed"))
					}
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body}, nil
			})}
			target, err := url.Parse("https://registry.example/v2/")
			require.NoError(t, err)
			observation, _ := registryRequestWithClock(context.Background(), client, target, "v2", func() time.Time { return instant })
			require.Equal(t, scenario.wantMillis, observation.ConnectMillis)
			require.Equal(t, "http-response", observation.Outcome)
			require.True(t, body.closed)
		})
	}
}

func TestRegistryRequestConnectTimingConcurrent(t *testing.T) {
	instant := time.Unix(100, 0)
	body := &registryUnreadBody{testing: t}
	client := &http.Client{Transport: registryRoundTripper(func(request *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(request.Context())
		started := make(chan struct{}, 2)
		finish := make(chan struct{})
		done := make(chan struct{}, 2)
		for _, address := range []string{"first:443", "second:443"} {
			go func() {
				trace.ConnectStart("tcp", address)
				started <- struct{}{}
				<-finish
				trace.ConnectDone("tcp", address, nil)
				done <- struct{}{}
			}()
		}
		<-started
		<-started
		close(finish)
		<-done
		<-done
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body}, nil
	})}
	target, err := url.Parse("https://registry.example/v2/")
	require.NoError(t, err)
	observation, _ := registryRequestWithClock(context.Background(), client, target, "v2", func() time.Time {
		instant = instant.Add(10 * time.Millisecond)
		return instant
	})
	require.Equal(t, float64(40), observation.ConnectMillis)
	require.True(t, body.closed)
}

func TestRegistryRequestTraceCleanup(t *testing.T) {
	var trace *httptrace.ClientTrace
	callbackDone := make(chan struct{})
	cleanupCompleted := false
	body := &registryTraceCloseBody{registryUnreadBody: &registryUnreadBody{testing: t}}
	body.closeTrace = func() {
		go func() {
			defer close(callbackDone)
			trace.ConnectDone("tcp", "unused", nil)
		}()
		select {
		case <-callbackDone:
			cleanupCompleted = true
		case <-time.After(time.Second):
		}
	}
	client := &http.Client{Transport: registryRoundTripper(func(request *http.Request) (*http.Response, error) {
		trace = httptrace.ContextClientTrace(request.Context())
		trace.ConnectStart("tcp", "unused")
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: body}, nil
	})}
	target, err := url.Parse("https://registry.example/v2/")
	require.NoError(t, err)
	observation, _ := registryRequest(context.Background(), client, target, "v2")
	select {
	case <-callbackDone:
	case <-time.After(time.Second):
		t.Fatal("trace callback did not finish after cleanup")
	}
	require.True(t, cleanupCompleted, "body cleanup could not complete a trace callback before its deadline")
	require.True(t, body.closed)
	require.Equal(t, 503, observation.Status)
	before, err := json.Marshal(observation)
	require.NoError(t, err)
	lateDone := make(chan struct{})
	go func() {
		defer close(lateDone)
		for attempt := 0; attempt < 100; attempt++ {
			trace.DNSStart(httptrace.DNSStartInfo{})
			trace.DNSDone(httptrace.DNSDoneInfo{})
			trace.ConnectStart("tcp", "unused")
			trace.ConnectDone("tcp", "unused", nil)
			trace.TLSHandshakeStart()
			trace.TLSHandshakeDone(tls.ConnectionState{}, nil)
		}
	}()
	for attempt := 0; attempt < 100; attempt++ {
		after, marshalErr := json.Marshal(observation)
		require.NoError(t, marshalErr)
		require.Equal(t, before, after)
	}
	select {
	case <-lateDone:
	case <-time.After(time.Second):
		t.Fatal("late trace callbacks did not finish")
	}
}

func TestRegistryDiagnosticsResponses(t *testing.T) {
	for _, host := range []string{"registry.example", "another.example:443"} {
		for _, status := range []int{200, 401, 403, 503} {
			t.Run(fmt.Sprintf("%s/%d", host, status), func(t *testing.T) {
				var paths []string
				var bodies []*registryUnreadBody
				transport := registryRoundTripper(func(request *http.Request) (*http.Response, error) {
					paths = append(paths, request.URL.Path)
					require.Empty(t, request.Header.Get("Authorization"))
					require.Empty(t, request.Header.Get("Cookie"))
					require.Equal(t, "https", request.URL.Scheme)
					header := http.Header{"Set-Cookie": {"cookie-secret"}, "X-Secret": {"header-secret"}}
					if request.URL.Path == "/v2/" {
						require.Equal(t, host, request.URL.Host)
						require.Empty(t, request.Header.Get("Accept"))
					} else if strings.Contains(request.URL.Path, "/manifests/") {
						require.Equal(t, "/v2/team/image/manifests/tag", request.URL.Path)
						require.Contains(t, request.Header.Get("Accept"), "application/vnd.oci.image.manifest.v1+json")
						header.Set("WWW-Authenticate", `Basic realm="login, please", Bearer realm="https://auth.example/token", service="registry.example", scope="repository:team/image:pull"`)
					} else {
						require.Equal(t, "auth.example", request.URL.Host)
						require.Empty(t, request.Header.Get("Accept"))
						require.Equal(t, url.Values{"service": {"registry.example"}, "scope": {"repository:team/image:pull"}}, request.URL.Query())
					}
					body := &registryUnreadBody{testing: t}
					bodies = append(bodies, body)
					return &http.Response{StatusCode: status, Header: header, Body: body}, nil
				})
				report := collectRegistryDiagnostics(context.Background(), host+"/team/image:tag", transport)
				require.Equal(t, []string{"/v2/", "/v2/team/image/manifests/tag", "/token"}, paths)
				require.Equal(t, "test-runner", report.Origin)
				require.Equal(t, "bearer", report.Challenge)
				for _, observation := range report.Observations {
					require.Equal(t, status, observation.Status)
					require.Equal(t, "http-response", observation.Outcome)
					require.Equal(t, time.UTC, observation.Started.Location())
					require.GreaterOrEqual(t, observation.RequestMillis, float64(0))
				}
				for _, body := range bodies {
					require.True(t, body.closed)
				}
			})
		}
	}
}

func TestRegistryChallenges(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		headers []string
		state   string
		query   string
	}{
		{"absent", nil, "not-discovered", ""},
		{"basic", []string{`Basic realm="login"`}, "not-applicable", ""},
		{"malformed-before-basic", []string{`Bearer`, `Basic realm="login"`}, "malformed", ""},
		{"basic-before-malformed", []string{`Basic realm="login"`, `Bearer`}, "malformed", ""},
		{"malformed-before-unsupported", []string{`Bearer`, `Digest realm="login"`}, "malformed", ""},
		{"unsupported-before-malformed", []string{`Digest realm="login"`, `Bearer`}, "malformed", ""},
		{"malformed-before-valid", []string{`Bearer`, `Bearer realm="https://auth.example/token"`}, "bearer", ""},
		{"valid-before-malformed", []string{`Bearer realm="https://auth.example/token"`, `Bearer`}, "bearer", ""},
		{"malformed-and-basic-one-header", []string{`Bearer, Basic realm="login"`}, "malformed", ""},
		{"unsupported", []string{`Digest realm="login"`}, "not-applicable", ""},
		{"bare", []string{`Bearer`}, "malformed", ""},
		{"unclosed", []string{`Bearer realm="https://auth.example/token`}, "malformed", ""},
		{"duplicate", []string{`Bearer realm="https://auth.example/token", realm="https://other.example/token"`}, "malformed", ""},
		{"http", []string{`Bearer realm="http://auth.example/token"`}, "malformed", ""},
		{"userinfo", []string{`Bearer realm="https://user:secret@auth.example/token"`}, "malformed", ""},
		{"query", []string{`Bearer realm="https://auth.example/token?secret=marker"`}, "malformed", ""},
		{"wrong-scope", []string{`Bearer realm="https://auth.example/token", scope="repository:other:push"`}, "malformed", ""},
		{"service-url", []string{`Bearer realm="https://auth.example/token", service="https://user:secret@host"`}, "malformed", ""},
		{"escaped-and-multiple", []string{`Basic realm="a\"b,c", Bearer realm="https://auth.example/token", service="other.example", scope="repository:team/image:pull", Basic realm="last"`}, "bearer", "scope=repository%3Ateam%2Fimage%3Apull&service=other.example"},
		{"multiple-headers", []string{`Basic realm="first"`, `bEaReR realm="https://auth.example/token"`}, "bearer", ""},
		{"tab", []string{"Bearer\trealm=\"https://auth.example/token\""}, "bearer", ""},
		{"extended-realm", []string{`Bearer realm*=UTF-8''https%3A%2F%2Fauth.example%2Ftoken`}, "malformed", ""},
		{"extended-scope", []string{`Bearer realm="https://auth.example/token", scope*=UTF-8''repository%3Ateam%2Fimage%3Apull`}, "malformed", ""},
		{"semicolon-parameters", []string{`Bearer realm="https://auth.example/token"; scope="repository:team/image:pull"`}, "malformed", ""},
		{"same-duplicate", []string{`Bearer realm="https://auth.example/token", realm="https://auth.example/token"`}, "malformed", ""},
		{"quoted-semicolon", []string{`Bearer realm="https://auth.example/token;parameter"`}, "bearer", ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			target, state := registryChallenge(scenario.headers, "team/image")
			require.Equal(t, scenario.state, state)
			if state == "bearer" {
				require.NotNil(t, target)
				require.Equal(t, scenario.query, target.RawQuery)
			} else {
				require.Nil(t, target)
			}
		})
	}
}

func TestRegistryFailureArtifacts(t *testing.T) {
	var console bytes.Buffer
	ginkgo.GinkgoWriter.TeeTo(&console)
	defer ginkgo.GinkgoWriter.ClearTeeWriters()
	directory := t.TempDir()
	podDirectory := filepath.Join(directory, "pod-diagnostics", "namespace", "pod")
	require.NoError(t, os.MkdirAll(podDirectory, 0755))
	for _, name := range []string{"pod.yaml", "events.yaml"} {
		require.NoError(t, os.WriteFile(filepath.Join(podDirectory, name), []byte("unchanged"), 0600))
	}
	calls := 0
	transport := registryRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		require.NoError(t, request.Context().Err())
		return nil, errors.New("https://user:password@host/path?token=private-marker")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	captureRegistryFailureDiagnostics(ctx, nil, "registry.example/team/image", "namespace", "pod", directory, transport, time.Second)
	require.Zero(t, calls)
	require.Empty(t, console.String())
	failure := errors.New("original-marker")
	captureRegistryFailureDiagnostics(ctx, failure, "registry.example/team/image", "namespace", "pod", directory, transport, time.Second)
	require.Equal(t, "original-marker", failure.Error())
	require.Equal(t, 1, calls)
	data, err := os.ReadFile(filepath.Join(podDirectory, "registry.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), "transport-error")
	for _, secret := range []string{"password", "private-marker", "original-marker", "registry.example", "Authorization"} {
		require.NotContains(t, string(data), secret)
		require.NotContains(t, console.String(), secret)
	}
	require.Equal(t, 1, strings.Count(console.String(), "\n"))
	for _, name := range []string{"pod.yaml", "events.yaml"} {
		data, err := os.ReadFile(filepath.Join(podDirectory, name))
		require.NoError(t, err)
		require.Equal(t, "unchanged", string(data))
	}
	console.Reset()
	captureRegistryFailureDiagnostics(ctx, failure, "invalid", "../escape", "pod", directory, transport, time.Second)
	require.Equal(t, "[registry diagnostics] collection unavailable; verification result unchanged\n", console.String())
}

func TestRegistryBudget(t *testing.T) {
	transport := registryRoundTripper(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	report := collectRegistryDiagnostics(ctx, "registry.example/team/image", transport)
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, "time-budget-ended", report.Observations[0].Outcome)
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	directory := t.TempDir()
	start = time.Now()
	captureRegistryFailureDiagnostics(parent, errors.New("original"), "registry.example/image", "namespace", "pod", directory, transport, 10*time.Millisecond)
	require.Less(t, time.Since(start), time.Second)
	data, err := os.ReadFile(filepath.Join(directory, "pod-diagnostics", "namespace", "pod", "registry.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), "time-budget-ended")
}

func TestRegistryScopedDiscoveryAndSanitization(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(fmt.Sprint(scoped), func(t *testing.T) {
			var steps []string
			transport := registryRoundTripper(func(request *http.Request) (*http.Response, error) {
				steps = append(steps, request.URL.Path)
				challenge := `Bearer realm="https://auth.example/token", service="registry.example"`
				if scoped || request.URL.Path != "/v2/" {
					challenge += `, scope="repository:team/image:pull"`
				}
				if request.URL.Path == "/token" {
					require.Equal(t, "repository:team/image:pull", request.URL.Query().Get("scope"))
				}
				header := http.Header{"Set-Cookie": {"cookie-marker"}}
				header.Set("WWW-Authenticate", challenge)
				return &http.Response{StatusCode: 401, Header: header, Body: &registryUnreadBody{testing: t}}, nil
			})
			report := collectRegistryDiagnostics(context.Background(), "registry.example/team/image:tag", transport)
			if scoped {
				require.Equal(t, []string{"/v2/", "/token"}, steps)
			} else {
				require.Equal(t, []string{"/v2/", "/v2/team/image/manifests/tag", "/token"}, steps)
			}
			data, err := json.Marshal(report)
			require.NoError(t, err)
			for _, secret := range []string{"cookie-marker", "auth.example", "team/image", "WWW-Authenticate"} {
				require.NotContains(t, string(data), secret)
			}
		})
	}
}

func TestRegistryURLValidation(t *testing.T) {
	for _, target := range []string{"https://localhost/v2/", "https://10.0.0.1/v2/", "https://[::1]/v2/", "https://[fe80::1%25eth0]/v2/", "https://registry.internal/v2/"} {
		parsed, err := url.Parse(target)
		require.NoError(t, err)
		require.True(t, safeRegistryURL(parsed))
	}
	for _, target := range []string{"http://public.example/", "https://user:secret@public.example/", "https://public.example/?token=secret", "https://public.example/#secret", "https://public.example/?"} {
		parsed, err := url.Parse(target)
		require.NoError(t, err)
		require.False(t, safeRegistryURL(parsed))
	}
	for _, image := range []string{"ubuntu:latest", "https://registry.example/image", "user:secret@registry.example/image", "registry.example/../image", "registry.example/image?token=secret", "registry.example/image@bad"} {
		_, _, _, err := registryTarget(image)
		require.Error(t, err)
	}
	_, repository, reference, err := registryTarget("registry.example:443/team/image@sha256:" + strings.Repeat("a", 64))
	require.NoError(t, err)
	require.Equal(t, "team/image", repository)
	require.Equal(t, "sha256:"+strings.Repeat("a", 64), reference)
}

func TestRegistryRedirectAndTLS(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	calls := 0
	transport := registryRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"http://127.0.0.1/secret"}}, Body: http.NoBody}, nil
	})
	report := collectRegistryDiagnostics(context.Background(), "registry.example/team/image", transport)
	require.Equal(t, 2, calls)
	for _, observation := range report.Observations {
		require.Equal(t, 302, observation.Status)
	}
	production := newRegistryTransport(context.Background())
	require.Nil(t, production.Proxy)
	require.Equal(t, int64(16*1024), production.MaxResponseHeaderBytes)
	require.False(t, production.TLSClientConfig.InsecureSkipVerify)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(200) }))
	defer server.Close()
	t.Cleanup(production.CloseIdleConnections)
	client := &http.Client{Transport: production}
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	observation, _ := registryRequest(context.Background(), client, target, "v2")
	require.Equal(t, "transport-error", observation.Outcome)
	require.Zero(t, observation.Status)
	production.TLSClientConfig.RootCAs = x509.NewCertPool()
	production.TLSClientConfig.RootCAs.AddCert(server.Certificate())
	observation, _ = registryRequest(context.Background(), client, target, "v2")
	require.Equal(t, 200, observation.Status)
	require.Equal(t, "http-response", observation.Outcome)
	require.Greater(t, observation.TLSMillis, float64(0))
	require.Greater(t, observation.ConnectMillis, float64(0))
	host, port, err := net.SplitHostPort(target.Host)
	require.NoError(t, err)
	production.TLSClientConfig.ServerName = host
	target.Host = net.JoinHostPort("localhost", port)
	observation, _ = registryRequest(context.Background(), client, target, "v2")
	require.Equal(t, 200, observation.Status)
	require.Greater(t, observation.DNSMillis, float64(0))
	require.Greater(t, observation.ConnectMillis, float64(0))
	require.Greater(t, observation.TLSMillis, float64(0))
}

func TestRegistryLocalChallengeFlow(t *testing.T) {
	requests := make(chan *http.Request, 3)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request
		writer.Header().Set("Set-Cookie", "session=unused")
		if request.URL.Path == "/v2/team/image/manifests/latest" {
			writer.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="https://%s/token", service="container_registry", scope="repository:team/image:pull"`, request.Host))
		}
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	transport := newRegistryTransport(ctx)
	t.Cleanup(transport.CloseIdleConnections)
	transport.TLSClientConfig.RootCAs = x509.NewCertPool()
	transport.TLSClientConfig.RootCAs.AddCert(server.Certificate())
	report := collectRegistryDiagnostics(ctx, strings.TrimPrefix(server.URL, "https://")+"/team/image", transport)
	require.Equal(t, "undetermined", report.RootCause)
	require.Equal(t, "bearer", report.Challenge)
	require.Len(t, report.Observations, 3)
	require.Len(t, requests, 3)
	for _, path := range []string{"/v2/", "/v2/team/image/manifests/latest", "/token"} {
		request := <-requests
		require.Equal(t, path, request.URL.Path)
		require.Empty(t, request.Header.Get("Authorization"))
		require.Empty(t, request.Header.Get("Proxy-Authorization"))
		require.Empty(t, request.Header.Get("Cookie"))
		if path == "/token" {
			require.Equal(t, "container_registry", request.URL.Query().Get("service"))
			require.Equal(t, "repository:team/image:pull", request.URL.Query().Get("scope"))
		}
	}
}

func TestRegistryTransportBudget(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	transport := newRegistryTransport(ctx)
	t.Cleanup(transport.CloseIdleConnections)
	transport.TLSClientConfig.RootCAs = x509.NewCertPool()
	transport.TLSClientConfig.RootCAs.AddCert(server.Certificate())
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	started := time.Now()
	observation, _ := registryRequest(ctx, &http.Client{Transport: transport}, target, "v2")
	require.Equal(t, "time-budget-ended", observation.Outcome)
	require.Less(t, time.Since(started), time.Second)
	connection, err := transport.DialContext(context.Background(), "tcp", target.Host)
	if connection != nil {
		require.NoError(t, connection.Close())
	}
	require.Error(t, err, "dialing must not outlive the collection deadline even with a detached context")
}

func TestRegistryWriteFailure(t *testing.T) {
	transport := newRegistryTransport(context.Background())
	t.Cleanup(transport.CloseIdleConnections)
	var console bytes.Buffer
	ginkgo.GinkgoWriter.TeeTo(&console)
	defer ginkgo.GinkgoWriter.ClearTeeWriters()
	file := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(file, nil, 0600))
	captureRegistryFailureDiagnostics(context.Background(), errors.New("original"), "invalid", "namespace", "pod", file, transport, time.Second)
	require.Equal(t, "[registry diagnostics] collection unavailable; verification result unchanged\n", console.String())
}

func TestRegistryStageRedirects(t *testing.T) {
	for _, redirected := range []string{"/v2/", "/v2/team/image/manifests/latest", "/token"} {
		t.Run(redirected, func(t *testing.T) {
			var paths []string
			var bodies []*registryUnreadBody
			transport := registryRoundTripper(func(request *http.Request) (*http.Response, error) {
				require.Contains(t, []string{"registry.example", "auth.example"}, request.URL.Host)
				paths = append(paths, request.URL.Path)
				header := http.Header{}
				status := http.StatusUnauthorized
				if request.URL.Path == redirected {
					status = http.StatusTemporaryRedirect
					header.Set("Location", "https://127.0.0.1/private?token=marker")
				} else if strings.Contains(request.URL.Path, "/manifests/") {
					header.Set("WWW-Authenticate", `Bearer realm="https://auth.example/token", scope="repository:team/image:pull"`)
				}
				body := &registryUnreadBody{testing: t}
				bodies = append(bodies, body)
				return &http.Response{StatusCode: status, Header: header, Body: body}, nil
			})
			report := collectRegistryDiagnostics(context.Background(), "registry.example/team/image", transport)
			require.Contains(t, paths, redirected)
			require.Len(t, report.Observations, len(paths))
			require.LessOrEqual(t, len(paths), 3)
			for _, body := range bodies {
				require.True(t, body.closed)
			}
		})
	}
}

func TestRegistryArtifactIsolation(t *testing.T) {
	root := t.TempDir()
	failure := errors.New("original")
	transport := registryRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: http.NoBody}, nil
	})
	for _, location := range []struct{ test, namespace, pod string }{
		{"first", "namespace", "pod"}, {"second", "namespace", "pod"}, {"first", "other", "pod"}, {"first", "namespace", "other"},
	} {
		captureRegistryFailureDiagnostics(context.Background(), failure, "registry.example/image", location.namespace, location.pod, filepath.Join(root, location.test), transport, time.Second)
		require.Equal(t, "original", failure.Error())
		data, err := os.ReadFile(filepath.Join(root, location.test, "pod-diagnostics", location.namespace, location.pod, "registry.json"))
		require.NoError(t, err)
		var report registryReport
		require.NoError(t, json.Unmarshal(data, &report))
		require.Equal(t, 503, report.Observations[0].Status)
		require.Equal(t, "not-discovered", report.Challenge)
	}
}

func TestRegistryImageReferences(t *testing.T) {
	for _, suffix := range []string{"", ":release", "@sha256:" + strings.Repeat("a", 64), ":release@sha256:" + strings.Repeat("a", 64)} {
		target, repository, reference, err := registryTarget("registry.example:8443/team/image" + suffix)
		require.NoError(t, err)
		require.Equal(t, "registry.example:8443", target.Host)
		require.Equal(t, "team/image", repository)
		switch {
		case strings.Contains(suffix, "@"):
			require.Equal(t, "sha256:"+strings.Repeat("a", 64), reference)
		case suffix == "":
			require.Equal(t, "latest", reference)
		default:
			require.Equal(t, "release", reference)
		}
	}
}
