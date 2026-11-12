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

package cincinnati

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

type graphEntry struct{ name, data string }

func graphArchive(t *testing.T, entries ...graphEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: entry.name, Mode: 0600, Size: int64(len(entry.data))}))
		_, err := io.WriteString(tw, entry.data)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buffer.Bytes()
}

func TestParseGraphData(t *testing.T) {
	archive := graphArchive(t,
		graphEntry{"./version", "1.1.0\n"},
		graphEntry{"channels/one.yaml", "name: stable-4.21\nversions: [garbage]\nfeeder: {arbitrary: true}\n"},
		graphEntry{"channels/two.yaml", "name: fast-5.0\n"},
		graphEntry{"channels/three.yaml", "name: candidate-4.20\n"},
		graphEntry{"channels/duplicate.yaml", "name: stable-4.21\n"},
		graphEntry{"channels/retired.yaml", "name: stable-4.19\n"},
		graphEntry{"channels/nightly.yaml", "name: nightly-4.23\n"},
		graphEntry{"channels/eus.yaml", "name: eus-4.20\n"},
		graphEntry{"blocked-edges/irrelevant.yaml", "[not yaml"},
	)
	profiles, err := ParseGraphData(bytes.NewReader(archive))
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]coreapi.VersionProfile{
		{ID: "4.20", ChannelGroup: "candidate"},
		{ID: "5.0", ChannelGroup: "fast"},
		{ID: "4.23", ChannelGroup: "nightly"},
		{ID: "4.19", ChannelGroup: "stable"},
		{ID: "4.21", ChannelGroup: "stable"},
	}, profiles), "channel names determine deduplicated discovery without a version floor")
}

func TestGraphDataChannelValidation(t *testing.T) {
	for _, name := range []string{"eus-4.20", "eus-garbage", "unknown", "stable", "stable-garbage", "fast-4.20.1", "candidate-04.20", "nightly-4.20-rc.1"} {
		t.Run(name, func(t *testing.T) {
			archive := graphArchive(t, graphEntry{"version", "1.1.0"}, graphEntry{"channels/channel.yml", "name: " + name})
			profiles, err := ParseGraphData(bytes.NewReader(archive))
			if strings.HasPrefix(name, "eus-") || name == "unknown" {
				require.NoError(t, err, "unsupported groups must be skipped before parsing the version")
				require.NotNil(t, profiles)
				require.Empty(t, profiles)
			} else {
				require.EqualError(t, err, "y-stream channel must be <stable|fast|candidate|nightly>-<major>.<minor>")
				require.Nil(t, profiles)
			}
		})
	}
}

func TestGraphDataRejectsInvalidArchivesWithoutPartialResults(t *testing.T) {
	base := []graphEntry{{"version", "1.1.0"}, {"channels/good.yaml", "name: stable-4.21"}}
	for _, tc := range []struct {
		name    string
		entries []graphEntry
	}{
		{"missing schema", base[1:]},
		{"missing channels", base[:1]},
		{"new schema", []graphEntry{{"version", "1.2.0"}, base[1]}},
		{"wrong major schema", []graphEntry{{"version", "2.0.0"}, base[1]}},
		{"malformed schema", []graphEntry{{"version", "not semver"}, base[1]}},
		{"malformed yaml", append(append([]graphEntry{}, base...), graphEntry{"channels/bad.yaml", "name: ["})},
		{"missing name", append(append([]graphEntry{}, base...), graphEntry{"channels/bad.yaml", "versions: []"})},
		{"wrong name type", append(append([]graphEntry{}, base...), graphEntry{"channels/bad.yaml", "name: [stable-4.22]"})},
		{"aliased name", append(append([]graphEntry{}, base...), graphEntry{"channels/bad.yaml", "other: &channel stable-4.22\nname: *channel"})},
		{"non-mapping channel", append(append([]graphEntry{}, base...), graphEntry{"channels/bad.yaml", "[stable-4.22]"})},
		{"non-scalar key", append(append([]graphEntry{}, base...), graphEntry{"channels/bad.yaml", "name: stable-4.22\n? [unknown]\n: value"})},
		{"invalid channel", append(append([]graphEntry{}, base...), graphEntry{"channels/bad.yaml", "name: stable-garbage"})},
		{"multiple yaml documents", append(append([]graphEntry{}, base...), graphEntry{"channels/bad.yaml", "name: stable-4.22\n---\nname: fast-4.22"})},
		{"duplicate name key", append(append([]graphEntry{}, base...), graphEntry{"channels/bad.yaml", "name: stable-4.22\nname: fast-4.22"})},
		{"duplicate path", append(append([]graphEntry{}, base...), base[1])},
		{"traversal", append(append([]graphEntry{}, base...), graphEntry{"../escape", "x"})},
		{"oversized entry", append(append([]graphEntry{}, base...), graphEntry{"raw/large", strings.Repeat("x", maxGraphDataEntry+1)})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profiles, err := ParseGraphData(bytes.NewReader(graphArchive(t, tc.entries...)))
			require.Error(t, err)
			require.Nil(t, profiles)
		})
	}
	valid := graphArchive(t, base...)
	for name, data := range map[string][]byte{
		"truncated gzip":   valid[:len(valid)-4],
		"not gzip":         []byte("not gzip"),
		"compressed limit": bytes.Repeat([]byte("x"), maxGraphDataCompressed+1),
	} {
		t.Run(name, func(t *testing.T) {
			profiles, err := ParseGraphData(bytes.NewReader(data))
			require.Error(t, err)
			require.Nil(t, profiles)
		})
	}
	t.Run("bad checksum", func(t *testing.T) {
		data := append([]byte{}, valid...)
		data[len(data)-8] ^= 0xff
		profiles, err := ParseGraphData(bytes.NewReader(data))
		require.Error(t, err)
		require.Nil(t, profiles)
	})
	t.Run("expanded limit", func(t *testing.T) {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		_, err := io.Copy(gz, io.LimitReader(repeatedByteReader{}, maxGraphDataExpanded+1))
		require.NoError(t, err)
		require.NoError(t, gz.Close())
		profiles, err := ParseGraphData(&b)
		require.ErrorContains(t, err, "expanded")
		require.Nil(t, profiles)
	})
	t.Run("entry count limit", func(t *testing.T) {
		entries := make([]graphEntry, maxGraphDataEntries+1)
		for i := range entries {
			entries[i] = graphEntry{name: fmt.Sprintf("raw/%d", i)}
		}
		profiles, err := ParseGraphData(bytes.NewReader(graphArchive(t, entries...)))
		require.ErrorContains(t, err, "entry limit")
		require.Nil(t, profiles)
	})
}

func TestGraphDataRejectsManyDuplicateYAMLKeys(t *testing.T) {
	duplicates := strings.Repeat("unknown: value\n", 10000)
	for _, tc := range []struct {
		name, data, wantError string
	}{
		{"channel", "name: stable-4.22\n" + duplicates, `channel "channels/bad.yaml" has duplicate key "unknown"`},
		{"second document", "name: stable-4.22\n---\n" + duplicates, `channel "channels/bad.yaml" must contain exactly one YAML document`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := graphArchive(t,
				graphEntry{"version", "1.1.0"},
				graphEntry{"channels/good.yaml", "name: stable-4.21"},
				graphEntry{"channels/bad.yaml", tc.data},
			)
			profiles, err := ParseGraphData(bytes.NewReader(archive))
			require.EqualError(t, err, tc.wantError, "duplicate keys must produce one bounded error")
			require.Nil(t, profiles)
		})
	}
}

func TestGraphDataTarValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		typeflag byte
		path     string
		truncate bool
	}{
		{name: "symlink", typeflag: tar.TypeSymlink, path: "channels/link"},
		{name: "absolute path", typeflag: tar.TypeReg, path: "/version"},
		{name: "truncated tar", typeflag: tar.TypeReg, path: "raw/entry", truncate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw bytes.Buffer
			tw := tar.NewWriter(&raw)
			require.NoError(t, tw.WriteHeader(&tar.Header{Name: tc.path, Typeflag: tc.typeflag}))
			require.NoError(t, tw.Close())
			data := raw.Bytes()
			if tc.truncate {
				data = data[:len(data)-1024]
			}
			var compressed bytes.Buffer
			gz := gzip.NewWriter(&compressed)
			_, err := gz.Write(data)
			require.NoError(t, err)
			require.NoError(t, gz.Close())
			profiles, err := ParseGraphData(&compressed)
			require.Error(t, err)
			require.Nil(t, profiles)
		})
	}
}

func TestGraphDataRequiresTarTerminatorAfterPayload(t *testing.T) {
	for _, size := range []int{0, 2048, 2049} {
		for _, removedBlocks := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("payload=%d/removed-blocks=%d", size, removedBlocks), func(t *testing.T) {
				archive := graphArchive(t,
					graphEntry{"version", "1.1.0"},
					graphEntry{"channels/stable.yaml", "name: stable-4.21"},
					graphEntry{"raw/zero-filled", strings.Repeat("\x00", size)},
				)
				reader, err := gzip.NewReader(bytes.NewReader(archive))
				require.NoError(t, err)
				raw, err := io.ReadAll(reader)
				require.NoError(t, err)
				require.NoError(t, reader.Close())
				raw = raw[:len(raw)-removedBlocks*512]
				var compressed bytes.Buffer
				writer := gzip.NewWriter(&compressed)
				_, err = writer.Write(raw)
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				profiles, err := ParseGraphData(&compressed)
				if removedBlocks > 0 {
					require.Error(t, err, "file contents and padding cannot stand in for tar terminators")
					require.Nil(t, profiles, "incomplete archives must not return partial discovery")
				} else {
					require.NoError(t, err)
					require.Empty(t, cmp.Diff([]coreapi.VersionProfile{{ID: "4.21", ChannelGroup: "stable"}}, profiles))
				}
			})
		}
	}
}

type repeatedByteReader struct{}

func (repeatedByteReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestGraphDataHTTP(t *testing.T) {
	archive := graphArchive(t, graphEntry{"version", "1.1.0"}, graphEntry{"channels/stable.yaml", "name: stable-4.20"})
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/upgrades_info/graph-data", r.URL.Path)
				w.WriteHeader(status)
				_, _ = w.Write(archive)
			}))
			defer server.Close()
			client := &GraphDataClient{client: server.Client(), url: server.URL + "/api/upgrades_info/graph-data"}
			profiles, err := client.VersionProfiles(t.Context())
			if status == http.StatusOK {
				require.NoError(t, err)
				require.Len(t, profiles, 1)
			} else {
				require.Error(t, err)
				require.Nil(t, profiles)
			}
		})
	}
	t.Run("context and client timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
		defer server.Close()
		client := &GraphDataClient{client: &http.Client{Timeout: 20 * time.Millisecond}, url: server.URL}
		profiles, err := client.VersionProfiles(t.Context())
		require.Error(t, err)
		require.Nil(t, profiles)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		profiles, err = client.VersionProfiles(ctx)
		require.ErrorIs(t, err, context.Canceled)
		require.Nil(t, profiles)
	})
}

func TestGraphDataHTTPClosesConnections(t *testing.T) {
	archive := graphArchive(t, graphEntry{"version", "1.1.0"}, graphEntry{"channels/stable.yaml", "name: stable-4.20"})
	for _, http2 := range []bool{false, true} {
		for _, cancelAt := range []string{"never", "before headers", "during body"} {
			t.Run(fmt.Sprintf("http2=%t/cancel=%s", http2, cancelAt), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				closed := make(chan struct{}, 1)
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, http2, r.ProtoMajor == 2)
					if cancelAt != "never" {
						if cancelAt == "during body" {
							w.Header().Set("Content-Length", fmt.Sprint(len(archive)))
							w.WriteHeader(http.StatusOK)
							w.(http.Flusher).Flush()
						}
						cancel()
						<-r.Context().Done()
						return
					}
					_, _ = w.Write(archive)
				}))
				server.EnableHTTP2 = http2
				server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
					if state == http.StateClosed {
						closed <- struct{}{}
					}
				}
				server.StartTLS()
				defer server.Close()
				client := NewGraphDataClient()
				client.url = server.URL
				client.client.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig
				_, err := client.VersionProfiles(ctx)
				if cancelAt != "never" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				select {
				case <-closed:
				case <-time.After(5 * time.Second):
					t.Fatal("graph-data connection remained open after request completed")
				}
			})
		}
	}
}
