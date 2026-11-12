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
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2"
)

var errRegistryInvalid = errors.New("invalid registry target")

var registryRepository = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
var registryTag = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`)
var registryDigest = regexp.MustCompile(`^sha256:[a-fA-F0-9]{64}$`)
var registryService = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.:-]{0,252}$`)
var registryArtifactComponent = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

type registryObservation struct {
	Step          string    `json:"step"`
	Started       time.Time `json:"started_utc"`
	Status        int       `json:"http_status,omitempty"`
	Outcome       string    `json:"outcome"`
	DNSMillis     float64   `json:"dns_ms"`
	ConnectMillis float64   `json:"connect_ms"`
	TLSMillis     float64   `json:"tls_ms"`
	RequestMillis float64   `json:"request_ms"`
}

type registryReport struct {
	Origin         string                `json:"origin"`
	RootCause      string                `json:"root_cause"`
	Interpretation string                `json:"interpretation"`
	Observations   []registryObservation `json:"observations"`
	Challenge      string                `json:"challenge"`
}

// CaptureRegistryFailureDiagnostics records anonymous HTTPS observations from the
// test runner, not the node. A nil failure is a no-op. Call before cleanup and
// assert the original failure afterward; this helper never changes that failure.
// Probe outcomes and collection errors are observations, never a root cause or
// an additional test failure. The report always leaves root cause undetermined.
// Collection has an independent DefaultDiagnoseTimeout budget. Only explicit
// registry/repository image references are supported.
// No credentials, response bodies, raw URLs, headers or errors are persisted.
// The artifact is pod-diagnostics/<namespace>/<podName>/registry.json; absent or
// unwritable artifact directories produce only a short notice, never a dump.
func CaptureRegistryFailureDiagnostics(ctx context.Context, failureErr error, image, namespace, podName, artifactDir string) {
	captureRegistryFailureDiagnostics(ctx, failureErr, image, namespace, podName, artifactDir, nil, DefaultDiagnoseTimeout)
}

func captureRegistryFailureDiagnostics(ctx context.Context, failureErr error, image, namespace, podName, artifactDir string, transport http.RoundTripper, timeout time.Duration) {
	if failureErr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if transport == nil {
		transport = newRegistryTransport(ctx)
	}
	if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
		defer closer.CloseIdleConnections()
	}
	report := collectRegistryDiagnostics(ctx, image, transport)
	if artifactDir == "" || !registryArtifactComponent.MatchString(namespace) || !registryArtifactComponent.MatchString(podName) {
		ginkgo.GinkgoWriter.Println("[registry diagnostics] collection unavailable; verification result unchanged")
		return
	}
	directory := filepath.Join(artifactDir, "pod-diagnostics", namespace, podName)
	data, err := json.MarshalIndent(report, "", "  ")
	if err == nil {
		err = os.MkdirAll(directory, 0755)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(directory, "registry.json"), data, 0600)
	}
	if err != nil {
		ginkgo.GinkgoWriter.Println("[registry diagnostics] collection unavailable; verification result unchanged")
		return
	}
	ginkgo.GinkgoWriter.Printf("[registry diagnostics] observations (cause undetermined): pod-diagnostics/%s/%s/registry.json\n", namespace, podName)
}

func registryTarget(image string) (*url.URL, string, string, error) {
	host, repository, found := strings.Cut(image, "/")
	if !found || (!strings.ContainsAny(host, ".:") && host != "localhost") {
		return nil, "", "", errRegistryInvalid
	}
	reference := "latest"
	if name, digest, ok := strings.Cut(repository, "@"); ok {
		repository, reference = name, digest
		if !registryDigest.MatchString(reference) {
			return nil, "", "", errRegistryInvalid
		}
		if name, tag, ok := strings.Cut(repository, ":"); ok {
			if !registryTag.MatchString(tag) {
				return nil, "", "", errRegistryInvalid
			}
			repository = name
		}
	} else if name, tag, ok := strings.Cut(repository, ":"); ok {
		repository, reference = name, tag
		if !registryTag.MatchString(reference) {
			return nil, "", "", errRegistryInvalid
		}
	}
	target, err := url.Parse("https://" + host + "/v2/")
	if err != nil || !registryRepository.MatchString(repository) || !safeRegistryURL(target) {
		return nil, "", "", errRegistryInvalid
	}
	return target, repository, reference, nil
}

func collectRegistryDiagnostics(ctx context.Context, image string, transport http.RoundTripper) registryReport {
	report := registryReport{Origin: "test-runner", RootCause: "undetermined", Interpretation: "Diagnostic probe observations do not determine the primary verification outcome or its root cause. Anonymous responses are not pull proof or credential validation; failures do not establish a registry-wide outage.", Challenge: "not-discovered"}
	target, repository, reference, err := registryTarget(image)
	if err != nil {
		report.Observations = append(report.Observations, registryObservation{Step: "v2", Started: time.Now().UTC(), Outcome: "skipped-invalid-image"})
		return report
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	observation, headers := registryRequest(ctx, client, target, "v2")
	report.Observations = append(report.Observations, observation)
	if observation.Status == 0 {
		return report
	}
	tokenURL, challenge := registryChallenge(headers, repository)
	if tokenURL == nil || tokenURL.Query().Get("scope") == "" {
		target.Path = "/v2/" + repository + "/manifests/" + reference
		observation, headers = registryRequest(ctx, client, target, "manifest-challenge")
		report.Observations = append(report.Observations, observation)
		discoveredURL, discovered := registryChallenge(headers, repository)
		if discoveredURL != nil {
			tokenURL, challenge = discoveredURL, discovered
		} else if tokenURL == nil && discovered != "not-discovered" {
			challenge = discovered
		}
	}
	report.Challenge = challenge
	if tokenURL != nil {
		observation, _ = registryRequest(ctx, client, tokenURL, "anonymous-token")
		report.Observations = append(report.Observations, observation)
	}
	return report
}

func registryRequest(ctx context.Context, client *http.Client, target *url.URL, step string) (registryObservation, []string) {
	return registryRequestWithClock(ctx, client, target, step, time.Now)
}

func registryRequestWithClock(ctx context.Context, client *http.Client, target *url.URL, step string, now func() time.Time) (registryObservation, []string) {
	started := time.Now()
	observation := registryObservation{Step: step, Started: started.UTC()}
	var mutex sync.Mutex
	var dnsStart, tlsStart time.Time
	type connectAttempt struct{ network, address string }
	connectStarts := make(map[connectAttempt]time.Time)
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { mutex.Lock(); defer mutex.Unlock(); dnsStart = time.Now() },
		DNSDone: func(httptrace.DNSDoneInfo) {
			mutex.Lock()
			defer mutex.Unlock()
			observation.DNSMillis += float64(time.Since(dnsStart).Microseconds()) / 1000
		},
		ConnectStart: func(network, address string) {
			mutex.Lock()
			defer mutex.Unlock()
			connectStarts[connectAttempt{network, address}] = now()
		},
		ConnectDone: func(network, address string, _ error) {
			mutex.Lock()
			defer mutex.Unlock()
			attempt := connectAttempt{network, address}
			if connectStart, found := connectStarts[attempt]; found {
				observation.ConnectMillis += float64(now().Sub(connectStart).Microseconds()) / 1000
				delete(connectStarts, attempt)
			}
		},
		TLSHandshakeStart: func() { mutex.Lock(); defer mutex.Unlock(); tlsStart = time.Now() },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			mutex.Lock()
			defer mutex.Unlock()
			observation.TLSMillis += float64(time.Since(tlsStart).Microseconds()) / 1000
		},
	}
	request, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, target.String(), nil)
	if err != nil {
		observation.Outcome = "invalid-request"
		return observation, nil
	}
	if step == "manifest-challenge" {
		request.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	}
	response, err := client.Do(request)
	mutex.Lock()
	snapshot := observation
	mutex.Unlock()
	snapshot.RequestMillis = float64(time.Since(started).Microseconds()) / 1000
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			snapshot.Outcome = "time-budget-ended"
		default:
			snapshot.Outcome = "transport-error"
		}
		return snapshot, nil
	}
	defer response.Body.Close()
	snapshot.Status = response.StatusCode
	snapshot.Outcome = "http-response"
	return snapshot, response.Header.Values("WWW-Authenticate")
}

func safeRegistryURL(target *url.URL) bool {
	return target != nil && target.Scheme == "https" && target.Hostname() != "" && target.User == nil && target.RawQuery == "" && !target.ForceQuery && target.Fragment == "" && target.Opaque == ""
}

func newRegistryTransport(budget context.Context) *http.Transport {
	deadline, _ := budget.Deadline()
	dialer := &net.Dialer{Timeout: 5 * time.Second, Deadline: deadline}
	return &http.Transport{
		DialContext:         dialer.DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
		MaxResponseHeaderBytes: 16 * 1024, DisableKeepAlives: true, DisableCompression: true,
	}
}

// Split only unquoted commas, then use MIME's quoted-parameter parser. Challenge
// values are used for requests only, never copied into the diagnostic artifact.
func registryChallenge(headers []string, repository string) (*url.URL, string) {
	state := "not-discovered"
	for _, header := range headers {
		var parts []string
		start, quoted, escaped := 0, false, false
		malformed := false
		for index, character := range header {
			if escaped {
				escaped = false
				continue
			}
			if quoted && character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				quoted = !quoted
			}
			if character == ';' && !quoted {
				malformed = true
			}
			if character == ',' && !quoted {
				parts = append(parts, strings.TrimSpace(header[start:index]))
				start = index + 1
			}
		}
		if quoted || escaped || malformed {
			state = "malformed"
			continue
		}
		parts = append(parts, strings.TrimSpace(header[start:]))
		for index := 0; index < len(parts); index++ {
			scheme, firstParameter := parts[index], ""
			separator := strings.IndexAny(scheme, " \t")
			if separator >= 0 {
				scheme, firstParameter = scheme[:separator], strings.TrimSpace(scheme[separator+1:])
			}
			parameters := []string{firstParameter}
			for index+1 < len(parts) && registryAuthParameter(parts[index+1]) {
				index++
				parameters = append(parameters, parts[index])
			}
			if !strings.EqualFold(scheme, "Bearer") {
				if state == "not-discovered" {
					state = "not-applicable"
				}
				continue
			}
			state = "malformed"
			if separator < 0 {
				continue
			}
			values, valid := registryChallengeParameters(parameters)
			if !valid {
				continue
			}
			realm, err := url.Parse(values["realm"])
			if err != nil || !safeRegistryURL(realm) {
				continue
			}
			service, scope := values["service"], values["scope"]
			if (service != "" && !registryService.MatchString(service)) || (scope != "" && scope != "repository:"+repository+":pull") {
				continue
			}
			query := url.Values{}
			if service != "" {
				query.Set("service", service)
			}
			if scope != "" {
				query.Set("scope", scope)
			}
			realm.RawQuery = query.Encode()
			return realm, "bearer"
		}
	}
	return nil, state
}

func registryChallengeParameters(parameters []string) (map[string]string, bool) {
	values := map[string]string{}
	for _, parameter := range parameters {
		key, _, found := strings.Cut(parameter, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		_, parsed, err := mime.ParseMediaType("Bearer;" + parameter)
		value, exactKey := parsed[key]
		_, duplicate := values[key]
		// MIME extensions must not rename HTTP auth parameters (realm* is not realm).
		if !found || err != nil || len(parsed) != 1 || !exactKey || duplicate {
			return nil, false
		}
		values[key] = value
	}
	return values, true
}

func registryAuthParameter(value string) bool {
	key, _, found := strings.Cut(value, "=")
	return found && !strings.ContainsAny(strings.TrimSpace(key), " \t")
}
