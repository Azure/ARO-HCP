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
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

type verifyConsoleLogin struct {
	fixture framework.ConsoleFixture
	timeout time.Duration
}

// VerifyConsoleLogin exercises a fresh password login through Dex and reads a
// known resource through Console's authenticated proxy. It does not render UI.
func VerifyConsoleLogin(fixture framework.ConsoleFixture, timeout time.Duration) HostedClusterVerifier {
	return &verifyConsoleLogin{fixture: fixture, timeout: timeout}
}

func (*verifyConsoleLogin) Name() string { return "VerifyConsoleLogin" }

func (v *verifyConsoleLogin) Verify(ctx context.Context, config *rest.Config) error {
	if v.timeout <= 0 {
		return fmt.Errorf("console login timeout must be > 0")
	}
	transport, err := newConsoleTransport(v.fixture)
	if err != nil {
		return err
	}
	defer transport.close()
	diagnose := func(ctx context.Context, config *rest.Config) string {
		return framework.ConsoleFixtureDiagnostics(ctx, config, v.fixture.Namespace)
	}
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, config, DefaultDiagnoseTimeout, diagnose, func(ctx context.Context) error {
		return v.check(ctx, transport)
	})
}

// Keep issuer trust separate from Console trust: a test issuer CA must not make
// an incorrectly provisioned Console serving certificate pass verification.
type consoleTransport struct {
	console, issuer *url.URL
	transports      map[string]*http.Transport
}

func newConsoleTransport(f framework.ConsoleFixture) (*consoleTransport, error) {
	t := &consoleTransport{transports: map[string]*http.Transport{}}
	for i, endpoint := range []struct {
		url string
		ca  []byte
	}{{f.ConsoleURL, f.ConsoleCA}, {f.IssuerURL, f.IssuerCA}} {
		u, err := url.Parse(endpoint.url)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("console and Dex URLs must be HTTPS origins without credentials, query, or fragment")
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("load system certificate roots: %w", err)
		}
		if len(endpoint.ca) > 0 && !roots.AppendCertsFromPEM(endpoint.ca) {
			return nil, fmt.Errorf("invalid certificate trust bundle for %s", u.Host)
		}
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
		t.transports[u.Host] = tr
		if i == 0 {
			t.console = u
		} else {
			t.issuer = u
		}
	}
	if t.console.Host == t.issuer.Host {
		return nil, fmt.Errorf("console and Dex must have distinct origins")
	}
	if f.Username == "" || f.Password == "" || f.Marker == "" || len(validation.IsDNS1123Label(f.Namespace)) > 0 || len(validation.IsDNS1123Subdomain(f.ConfigMapName)) > 0 {
		return nil, fmt.Errorf("console fixture requires credentials, marker, and valid resource names")
	}
	return t, nil
}

func (t *consoleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr, allowed := t.transports[req.URL.Host]
	if !allowed || req.URL.Scheme != "https" || req.URL.User != nil {
		return nil, consoleFlowError("request outside the configured Console and Dex HTTPS origins")
	}
	return tr.RoundTrip(req)
}

func (t *consoleTransport) close() {
	for _, tr := range t.transports {
		tr.CloseIdleConnections()
	}
}

func (v *verifyConsoleLogin) check(ctx context.Context, transport *consoleTransport) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return fmt.Errorf("create Console cookie jar: %w", err)
	}
	callbackSeen := false
	client := &http.Client{Transport: transport, Jar: jar, Timeout: 30 * time.Second}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 15 {
			return consoleFlowError("login exceeded 15 redirects")
		}
		if req.URL.Scheme != "https" || req.URL.User != nil || (req.URL.Host != transport.console.Host && req.URL.Host != transport.issuer.Host) {
			return consoleFlowError("login redirected outside the configured Console and Dex HTTPS origins")
		}
		if req.Method != http.MethodGet {
			return consoleFlowError("login redirect attempted to forward a credential-bearing request")
		}
		if req.URL.Host == transport.console.Host && req.URL.Path == "/auth/callback" {
			if req.URL.Query().Get("code") == "" || req.URL.Query().Get("state") == "" {
				return consoleFlowError("Console callback lacks authorization code or state")
			}
			callbackSeen = true
		}
		return nil
	}
	resp, err := consoleRequest(ctx, client, http.MethodGet, transport.console.ResolveReference(&url.URL{Path: "/auth/login"}), nil)
	if err != nil {
		return fmt.Errorf("console login redirect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Host != transport.issuer.Host || callbackSeen {
		return fmt.Errorf("console login expected Dex password page, observed HTTP %d from host %s", resp.StatusCode, resp.Request.URL.Host)
	}
	action, values, err := consolePasswordForm(resp.Body, resp.Request.URL)
	if err != nil {
		return fmt.Errorf("dex password page: %w", err)
	}
	if action.Scheme != "https" || action.Host != transport.issuer.Host || action.User != nil || action.Fragment != "" {
		return fmt.Errorf("dex password form action is outside the configured issuer")
	}
	values.Set("login", v.fixture.Username)
	values.Set("password", v.fixture.Password)
	resp.Body.Close()
	resp, err = consoleRequest(ctx, client, http.MethodPost, action, values)
	if err != nil {
		return fmt.Errorf("dex login / Console callback: %w", err)
	}
	defer resp.Body.Close()
	if !callbackSeen || resp.Request.URL.Host != transport.console.Host || resp.StatusCode != http.StatusOK || resp.Request.URL.Query().Get("error") != "" {
		return fmt.Errorf("expected successful Console callback, observed callback=%t HTTP %d from host %s", callbackSeen, resp.StatusCode, resp.Request.URL.Host)
	}
	// The callback sets the session cookies before serving tokener.html. No
	// JavaScript execution or synthetic session/token injection is needed.
	resp.Body.Close()
	resourceURL := transport.console.ResolveReference(&url.URL{Path: "/api/kubernetes/api/v1/namespaces/" + v.fixture.Namespace + "/configmaps/" + v.fixture.ConfigMapName})
	resp, err = consoleRequest(ctx, client, http.MethodGet, resourceURL, nil)
	if err != nil {
		return fmt.Errorf("console authenticated resource read: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Request.URL.String() != resourceURL.String() {
		return fmt.Errorf("console proxy expected HTTP 200 without redirect, observed HTTP %d", resp.StatusCode)
	}
	var cm corev1.ConfigMap
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&cm); err != nil {
		return fmt.Errorf("console proxy did not return a ConfigMap JSON document")
	}
	if cm.Kind != "ConfigMap" || cm.APIVersion != "v1" || cm.Name != v.fixture.ConfigMapName || cm.Namespace != v.fixture.Namespace || cm.Data["marker"] != v.fixture.Marker {
		return fmt.Errorf("console proxy returned a resource that does not match the expected ConfigMap and marker")
	}
	resp.Body.Close()
	// A transport failure or server error is not evidence of authentication
	// enforcement. Require an explicit denial from a fresh, unauthenticated client.
	anonymous := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = consoleRequest(ctx, anonymous, http.MethodGet, resourceURL, nil)
	if err != nil {
		return fmt.Errorf("console unauthenticated resource read: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("console proxy expected unauthenticated HTTP 401, observed HTTP %d", resp.StatusCode)
	}
	return nil
}

func consoleRequest(ctx context.Context, client *http.Client, method string, target *url.URL, form url.Values) (*http.Response, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("could not construct login request")
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := client.Do(req)
	if err != nil {
		// url.Error includes the URL, which can contain authorization codes and
		// state. Neither response bodies nor redirect URLs belong in diagnostics.
		var urlErr *url.Error
		for errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, sanitizedConsoleRequestError(err)
	}
	return resp, nil
}

type consoleFlowError string

func (e consoleFlowError) Error() string { return string(e) }

func sanitizedConsoleRequestError(err error) error {
	var flow consoleFlowError
	var certificate *tls.CertificateVerificationError
	switch {
	case errors.As(err, &flow):
		return flow
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.As(err, &certificate):
		return fmt.Errorf("TLS certificate verification failed (%T)", certificate.Err)
	default:
		// net/http can embed an entire malformed Location header in an error,
		// even after url.Error is unwrapped. Discard it rather than wrapping it:
		// Gomega recursively formats error fields, not just Error().
		return fmt.Errorf("HTTP request failed (%T)", err)
	}
}

func consolePasswordForm(body io.Reader, page *url.URL) (*url.URL, url.Values, error) {
	doc, err := html.Parse(io.LimitReader(body, 1<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("could not parse password form")
	}
	attr := func(n *html.Node, key string) string {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
		return ""
	}
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode || n.Data != "form" || !strings.EqualFold(attr(n, "method"), "post") {
			continue
		}
		values := url.Values{}
		password, login := false, false
		for input := range n.Descendants() {
			if input.Type != html.ElementNode || input.Data != "input" {
				continue
			}
			name := attr(input, "name")
			password = password || (name == "password" && attr(input, "type") == "password")
			login = login || name == "login"
			if name != "" && attr(input, "type") == "hidden" {
				values.Add(name, attr(input, "value"))
			}
		}
		if !password || !login {
			continue
		}
		action, err := page.Parse(attr(n, "action"))
		if err != nil {
			return nil, nil, fmt.Errorf("invalid password form action")
		}
		return action, values, nil
	}
	return nil, nil, fmt.Errorf("expected a POST form with login and password fields")
}
