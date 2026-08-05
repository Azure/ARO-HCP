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
	"sort"
	"testing"
)

// Server is a fake Cincinnati update service backed by httptest.Server.
// It dispatches graph responses based on the "channel" query parameter
// and enriches each node with its cross-channel membership metadata,
// mirroring what a real Cincinnati server returns.
type Server struct {
	httpServer        *httptest.Server
	channels          map[string]*Graph
	channelMembership map[string][]string
}

// NewServer creates a test Cincinnati server that serves the given per-channel
// graphs. The server is automatically closed when t finishes.
func NewServer(t *testing.T, channels map[string]*Graph) *Server {
	s := &Server{
		channels:          channels,
		channelMembership: computeChannelMembership(channels),
	}
	s.httpServer = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.httpServer.Close)
	return s
}

// computeChannelMembership builds a map from version string to the sorted
// list of channels that version appears in.
func computeChannelMembership(channels map[string]*Graph) map[string][]string {
	membership := make(map[string][]string)
	for channelName, graph := range channels {
		for _, ver := range graph.NodeVersions() {
			membership[ver] = append(membership[ver], channelName)
		}
	}
	for _, channels := range membership {
		sort.Strings(channels)
	}
	return membership
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		http.Error(w, "missing channel parameter", http.StatusBadRequest)
		return
	}

	graph, ok := s.channels[channel]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nodes":[],"edges":[]}`))
		return
	}

	data, err := graph.marshal(s.channelMembership)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// URI returns the test server's base URL.
func (s *Server) URI() *url.URL {
	u, _ := url.Parse(s.httpServer.URL)
	return u
}
