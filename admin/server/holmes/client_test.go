package holmes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/client-go/rest"
)

func TestAskHolmes(t *testing.T) {
	tests := []struct {
		name           string
		handler        http.HandlerFunc
		wantErr        bool
		wantErrContain string
		wantBody       string
	}{
		{
			name: "extracts analysis from JSON response",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, `{"analysis":"all good","conversation_history":[{"role":"system","content":"long system prompt"}]}`)
			},
			wantBody: "all good",
		},
		{
			name: "returns raw body when not valid JSON",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, "plain text response")
			},
			wantBody: "plain text response",
		},
		{
			name: "returns raw body when analysis is empty",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, `{"analysis":"","conversation_history":[]}`)
			},
			wantBody: `{"analysis":"","conversation_history":[]}`,
		},
		{
			name: "non-200 response",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, "internal error")
			},
			wantErr:        true,
			wantErrContain: "Holmes service returned HTTP 500",
		},
		{
			name: "large error body is truncated",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				fmt.Fprint(w, strings.Repeat("x", 2048))
			},
			wantErr:        true,
			wantErrContain: "Holmes service returned HTTP 502",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			w := httptest.NewRecorder()
			err := AskHolmes(context.Background(), server.URL, "test question", "test-model", w)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrContain)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := w.Body.String(); got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
		})
	}
}

func TestAskHolmes_LargeErrorBodyIsCapped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, strings.Repeat("x", 4096))
	}))
	defer server.Close()

	w := httptest.NewRecorder()
	err := AskHolmes(context.Background(), server.URL, "q", "m", w)
	if err == nil {
		t.Fatal("expected error")
	}

	// Error body should be capped at 1024 bytes
	errMsg := err.Error()
	xCount := strings.Count(errMsg, "x")
	if xCount > 1024 {
		t.Errorf("error body has %d 'x' chars, expected at most 1024", xCount)
	}
}

func TestAskHolmes_RequestFormat(t *testing.T) {
	var receivedBody string
	var receivedContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		receivedBody = string(buf[:n])
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	w := httptest.NewRecorder()
	_ = AskHolmes(context.Background(), server.URL, "test question", "azure/gpt-5.2", w)

	if receivedContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", receivedContentType)
	}

	if !strings.Contains(receivedBody, `"ask":"test question"`) {
		t.Errorf("body does not contain ask field: %s", receivedBody)
	}

	if !strings.Contains(receivedBody, `"model":"azure/gpt-5.2"`) {
		t.Errorf("body does not contain model field: %s", receivedBody)
	}
}

func TestAskHolmes_URLTrailingSlash(t *testing.T) {
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w := httptest.NewRecorder()
	_ = AskHolmes(context.Background(), server.URL+"/", "q", "m", w)

	if receivedPath != "/api/chat" {
		t.Errorf("path = %q, want /api/chat", receivedPath)
	}
}

func TestAskHolmes_UnreachableEndpoint(t *testing.T) {
	w := httptest.NewRecorder()
	err := AskHolmes(context.Background(), "http://127.0.0.1:1", "q", "m", w)
	if err == nil {
		t.Fatal("expected error for unreachable endpoint")
	}
	if !strings.Contains(err.Error(), "failed to call Holmes service") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestServiceProxyURL(t *testing.T) {
	restCfg := rest.Config{Host: "https://api.example.com"}
	got := ServiceProxyURL(&restCfg, "aro-holmesgpt", "holmesgpt-svc")
	want := "https://api.example.com/api/v1/namespaces/aro-holmesgpt/services/holmesgpt-svc:80/proxy"
	if got != want {
		t.Errorf("ServiceProxyURL = %q, want %q", got, want)
	}
}

func TestServiceProxyURL_TrailingSlash(t *testing.T) {
	restCfg := rest.Config{Host: "https://api.example.com/"}
	got := ServiceProxyURL(&restCfg, "ns", "svc")
	want := "https://api.example.com/api/v1/namespaces/ns/services/svc:80/proxy"
	if got != want {
		t.Errorf("ServiceProxyURL = %q, want %q", got, want)
	}
}

func TestAskHolmesWithClient_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "response")
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w := httptest.NewRecorder()
	err := AskHolmesWithClient(ctx, http.DefaultClient, server.URL, "q", "m", w)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
