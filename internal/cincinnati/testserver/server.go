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

package testserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"

	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/internal/cincinnati"
)

// Server is a fake Cincinnati update service backed by httptest.Server.
// It dispatches graph responses based on the "channel" query parameter.
type Server struct {
	httpServer *httptest.Server
	channels   map[string]*Graph
}

// NewServer creates a test Cincinnati server that serves the given per-channel
// graphs. The server is automatically closed when t finishes.
//
// Example:
//
//	server := testserver.NewServer(t, map[string]*testserver.Graph{
//	    "stable-4.19": testserver.NewGraph().
//	        Edges("4.19.10", "4.19.15", "4.19.22").
//	        Edges("4.19.15", "4.19.22"),
//	    "stable-4.20": testserver.NewGraph().
//	        Edges("4.19.22", "4.20.5").
//	        Edges("4.20.0", "4.20.5"),
//	})
//	client := server.NewClient()
func NewServer(t *testing.T, channels map[string]*Graph) *Server {
	s := &Server{channels: channels}
	s.httpServer = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.httpServer.Close)
	return s
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		http.Error(w, "missing channel parameter", http.StatusBadRequest)
		return
	}

	graph, ok := s.channels[channel]
	if !ok {
		http.Error(w, "channel not found: "+channel, http.StatusNotFound)
		return
	}

	data, err := graph.marshal()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// URI returns the test server's base URL, suitable for passing to
// cincinnati.Client.GetUpdates. Each call returns a fresh *url.URL
// because the CVO client mutates the URL by appending query parameters.
func (s *Server) URI() *url.URL {
	u, _ := url.Parse(s.httpServer.URL)
	return u
}

// NewClient creates a real CVO Cincinnati client pointed at this test server.
func (s *Server) NewClient() cvocincinnati.Client {
	return cvocincinnati.NewClient(
		uuid.New(),
		nil,
		"test-harness",
		cincinnati.NewAlwaysConditionRegistry(),
	)
}
