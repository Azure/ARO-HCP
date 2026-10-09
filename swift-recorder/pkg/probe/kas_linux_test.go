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

package probe

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestKASLiveness(t *testing.T) {
	root, ignition := newTestCA(t), newTestCA(t)
	pools := loadTrustBundles(map[string]string{TrustRoot: root.pem, TrustIgnition: ignition.pem})
	for _, tc := range []struct {
		name       string
		status     int
		liveStatus int
		disabled   bool
		wantError  string
		follow     bool
	}{
		{"ready", 200, 200, false, "", false},
		{"not ready but live", 500, 200, false, "KAS health check failed: HTTP status 500", true},
		{"unavailable", 503, 200, false, "KAS health check failed: HTTP status 503", true},
		{"not live", 500, 500, false, "KAS health check failed: HTTP status 500", true},
		{"unauthorized", 401, 200, false, "KAS authentication/authorization denied: HTTP status 401", false},
		{"forbidden", 403, 200, false, "KAS authentication/authorization denied: HTTP status 403", false},
		{"redirect", 302, 200, false, "unexpected HTTP status 302 (expected 200)", false},
		{"disabled", 500, 200, true, "KAS health check failed: HTTP status 500", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits, handshakes atomic.Int32
			server := newTLSServer(t, &tls.Config{Certificates: []tls.Certificate{root.issue(t, "router.test", false)}, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
				handshakes.Add(1)
				return nil, nil
			}}, func(w http.ResponseWriter, r *http.Request) {
				n := hits.Add(1)
				wantPath := "/readyz"
				status := tc.status
				if n == 2 {
					wantPath, status = "/livez?exclude=etcd", tc.liveStatus
				}
				if n > 2 || r.RequestURI != wantPath || r.Host != "router.test" || r.TLS.ServerName != "router.test" || r.Method != http.MethodGet || !r.Close || !strings.HasPrefix(r.RemoteAddr, "127.0.0.2:") {
					t.Error("incorrect KAS request, source, Host or SNI")
				}
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || len(r.TLS.PeerCertificates) != 0 {
					t.Error("probe sent credentials")
				}
				w.Header().Set("Set-Cookie", "PRIVATE_COOKIE")
				w.Header().Set("Location", "https://elsewhere.test/?token=PRIVATE_TOKEN")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "PRIVATE_RESPONSE_BODY")
			})
			target := targetFor(t, server.Listener.Addr().String(), false)
			target.Role, target.Path, target.TrustBundle, target.SourceIP = "kas-builtin", "/readyz", TrustRoot, "127.0.0.2"
			target.LivenessOnFailure = !tc.disabled
			req := Request{Targets: []Target{target}, SwiftIPs: []string{target.SourceIP}}
			if err := validateRequest(req); err != nil {
				t.Fatal(err)
			}
			result := probeTarget(target, pools[target.TrustBundle], time.Second)
			if result.Target != target || result.Stage != "complete" || result.Error != tc.wantError || result.HTTPStatus != tc.status || result.Expected200 != (tc.status == 200) || !result.TLSVerified || !result.TLSVerify || (result.Liveness != nil) != tc.follow {
				t.Fatalf("incorrect readiness result: %+v", result)
			}
			wantHits := int32(1)
			if tc.follow {
				wantHits++
				live := result.Liveness
				wantTarget := target
				wantTarget.Path, wantTarget.LivenessOnFailure = "/livez?exclude=etcd", false
				if live.Target != wantTarget || live.Liveness != nil || live.Stage != "complete" || !live.TLSVerified || !live.TLSVerify || live.HTTPStatus != tc.liveStatus || live.Expected200 != (tc.liveStatus == 200) || (live.Error == "") != (tc.liveStatus == 200) {
					t.Fatalf("incorrect liveness result: %+v", live)
				}
				if live.Source == result.Source || !strings.HasPrefix(live.Source, "127.0.0.2:") || live.Destination != result.Destination || !live.StartedAt.After(result.StartedAt) || live.Timings["tls"] <= 0 {
					t.Fatal("liveness did not use a fresh connection to the same destination and source IP")
				}
			}
			if hits.Load() != wantHits || handshakes.Load() != wantHits {
				t.Fatalf("requests=%d handshakes=%d want=%d", hits.Load(), handshakes.Load(), wantHits)
			}
			var output bytes.Buffer
			if err := writeResult(&output, Result{Targets: []Observation{result}}); err != nil {
				t.Fatal(err)
			}
			for _, marker := range []string{"PRIVATE", "BEGIN CERTIFICATE", "trustBundles", strings.Split(root.pem, "\n")[1], strings.Split(ignition.pem, "\n")[1]} {
				if strings.Contains(output.String(), marker) {
					t.Fatal("credentials, certificates, response headers or body leaked into output")
				}
			}
			var decoded Result
			if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Targets) != 1 || decoded.Targets[0].Target != target || (decoded.Targets[0].Liveness != nil) != tc.follow || !strings.Contains(output.String(), fmt.Sprintf(`"livenessOnFailure":%t`, target.LivenessOnFailure)) {
				t.Fatal("nested liveness changed the main target count or lost API fields")
			}
		})
	}
}

func TestKASLivenessTrustFailures(t *testing.T) {
	root, ignition := newTestCA(t), newTestCA(t)
	for _, mode := range []string{"wrong CA", "missing roots", "missing SNI", "connect", "follow-up wrong CA"} {
		t.Run(mode, func(t *testing.T) {
			var hits, handshakes atomic.Int32
			good, bad := root.issue(t, "router.test", false), ignition.issue(t, "router.test", false)
			server := newTLSServer(t, &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				n := handshakes.Add(1)
				if mode == "wrong CA" || mode == "follow-up wrong CA" && n == 2 {
					return &bad, nil
				}
				return &good, nil
			}}, func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(500)
			})
			target := targetFor(t, server.Listener.Addr().String(), false)
			target.Role, target.Path, target.TrustBundle, target.LivenessOnFailure = "kas-service", "/readyz", TrustRoot, true
			pools := loadTrustBundles(map[string]string{TrustRoot: root.pem, TrustIgnition: ignition.pem})
			wantStage, wantHits, wantHandshakes := "tls", int32(0), int32(1)
			switch mode {
			case "missing roots":
				delete(pools, TrustRoot)
				wantStage, wantHandshakes = "trust", 0
			case "missing SNI":
				target.ServerName = ""
				wantStage, wantHandshakes = "trust", 0
			case "connect":
				server.Close()
				wantStage, wantHandshakes = "connect", 0
			case "follow-up wrong CA":
				wantStage, wantHits, wantHandshakes = "complete", 1, 2
			}
			if err := validateRequest(Request{Targets: []Target{target}}); err != nil {
				t.Fatalf("per-target trust failure rejected request: %v", err)
			}
			result := probeTarget(target, pools[target.TrustBundle], time.Second)
			if result.Stage != wantStage || result.Error == "" || result.Expected200 || hits.Load() != wantHits || handshakes.Load() != wantHandshakes {
				t.Fatalf("incorrect failure or extra requests: %+v hits=%d handshakes=%d", result, hits.Load(), handshakes.Load())
			}
			if mode == "follow-up wrong CA" {
				if result.Liveness == nil || result.Liveness.TLSVerified || result.Liveness.Stage != "tls" || result.Liveness.Error != "TLS verification failed: unknown authority" {
					t.Fatal("follow-up skipped fresh TLS verification or used ignition trust")
				}
			} else if result.Liveness != nil || result.TLSVerified {
				t.Fatal("failed readiness connection triggered liveness")
			}
			if wantStage == "trust" && result.Source != "" {
				t.Fatal("missing trust opened a socket")
			}
		})
	}
}

func TestKASQueryAllowlist(t *testing.T) {
	base := Target{Address: "127.0.0.1", Port: 9444, Role: "kas-builtin", ServerName: "router.test", TrustBundle: TrustRoot, Path: "/livez?exclude=etcd"}
	for _, tc := range []struct {
		name   string
		mutate func(*Target)
		valid  bool
	}{
		{"direct liveness", func(*Target) {}, true},
		{"readiness follow-up", func(t *Target) { t.Path, t.LivenessOnFailure = "/readyz", true }, true},
		{"missing SNI per-target failure", func(t *Target) { t.ServerName = "" }, true},
		{"non-KAS", func(t *Target) { t.Role = "ignition" }, false},
		{"not role prefix", func(t *Target) { t.Role = "other-kas-builtin" }, false},
		{"plain HTTP", func(t *Target) { t.PlainHTTP = true }, false},
		{"TCP only", func(t *Target) { t.TCPOnly = true }, false},
		{"wrong trust", func(t *Target) { t.TrustBundle = TrustIgnition }, false},
		{"no trust", func(t *Target) { t.TrustBundle = "" }, false},
		{"recursive liveness", func(t *Target) { t.LivenessOnFailure = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := base
			tc.mutate(&target)
			if err := validateRequest(Request{Targets: []Target{target}}); (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if !tc.valid {
				target.Path, target.LivenessOnFailure = "/readyz", true
				if tc.name != "recursive liveness" && validateRequest(Request{Targets: []Target{target}}) == nil {
					t.Fatal("invalid follow-up configuration accepted")
				}
			}
		})
	}
	for _, path := range []string{"/readyz?exclude=etcd", "/livez?", "/livez?exclude=other", "/livez?exclude=etcd&token=PRIVATE", "/livez?token=PRIVATE&exclude=etcd", "/livez?exclude=etcd&exclude=etcd", "/livez?exclude=%65tcd", "/%6civez?exclude=etcd", "/livez?exclude=etcd#fragment", "https://router.test/livez?exclude=etcd", "//router.test/livez?exclude=etcd"} {
		t.Run(path, func(t *testing.T) {
			target := base
			target.Path = path
			err := validateRequest(Request{Targets: []Target{target}})
			if err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("unapproved query accepted or echoed")
			}
		})
	}
}

func TestKASLivenessRequiresCompleteResponse(t *testing.T) {
	ca := newTestCA(t)
	roots := loadTrustBundles(map[string]string{TrustRoot: ca.pem})[TrustRoot]
	for _, tc := range []struct {
		name, body, stage, reason string
		length                    int
	}{
		{"short", "short", "httpBody", "", 100},
		{"oversized", strings.Repeat("x", maxBodyBytes+1), "complete", "decodedBodyLimit", maxBodyBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			server := newTLSServer(t, &tls.Config{Certificates: []tls.Certificate{ca.issue(t, "router.test", false)}}, func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Length", fmt.Sprint(tc.length))
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, tc.body)
			})
			target := targetFor(t, server.Listener.Addr().String(), false)
			target.Role, target.Path, target.TrustBundle, target.LivenessOnFailure = "kas-builtin", "/readyz", TrustRoot, true
			result := probeTarget(target, roots, time.Second)
			if !result.TLSVerified || result.HTTPStatus != 500 || result.Stage != tc.stage || result.Error == "" || result.Expected200 || result.Liveness != nil || hits.Load() != 1 {
				t.Fatalf("incomplete readiness response lost evidence or triggered follow-up: %+v", result)
			}
			if result.BodyTruncated != (tc.reason != "") || result.BodyTruncationReason != tc.reason || result.BodyBytesDiscarded != int64(min(len(tc.body), maxBodyBytes)) {
				t.Fatalf("incorrect readiness body evidence: %+v", result)
			}
			if tc.reason != "" && result.Error != "KAS health check failed: HTTP status 500" {
				t.Fatalf("truncated response lost health check failure: %+v", result)
			}
		})
	}
}

func TestKASLivenessIndependentDeadline(t *testing.T) {
	ca := newTestCA(t)
	roots := loadTrustBundles(map[string]string{TrustRoot: ca.pem})[TrustRoot]
	var handshakes atomic.Int32
	server := newTLSServer(t, &tls.Config{Certificates: []tls.Certificate{ca.issue(t, "router.test", false)}, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		handshakes.Add(1)
		return nil, nil
	}}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(500)
			return
		}
		<-r.Context().Done()
	})
	target := targetFor(t, server.Listener.Addr().String(), false)
	target.Role, target.Path, target.TrustBundle, target.LivenessOnFailure = "kas-builtin", "/readyz", TrustRoot, true
	result := probeTarget(target, roots, 500*time.Millisecond)
	if result.Stage != "complete" || result.HTTPStatus != 500 || result.Liveness == nil {
		t.Fatalf("readiness failed before follow-up: %+v", result)
	}
	live := result.Liveness
	if live.Stage != "httpHeaders" || !strings.Contains(live.Error, "deadline exceeded") || live.DurationMS < 450 || live.DurationMS > 1000 || result.DurationMS >= live.DurationMS || handshakes.Load() != 2 {
		t.Fatalf("follow-up did not get its own bounded deadline: %+v", live)
	}
	if _, _, err := net.SplitHostPort(live.Source); err != nil {
		t.Fatal("missing follow-up connection evidence")
	}
}
