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

package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFindGNUMake(t *testing.T) {
	const (
		gnu4  = "GNU Make 4.4.1\nBuilt for aarch64-apple-darwin"
		apple = "GNU Make 3.81\nCopyright (C) 2006"
	)
	for _, tt := range []struct {
		name      string
		installed map[string]string // name -> version output
		want      string
		wantError string
	}{
		{"prefers gmake over system make", map[string]string{"gmake": gnu4, "make": apple}, "/bin/gmake", ""},
		{"uses make when it is GNU Make 4", map[string]string{"make": gnu4}, "/bin/make", ""},
		{"rejects macOS make 3.81", map[string]string{"make": apple}, "", "GNU Make 3.81"},
		{"rejects non-GNU make", map[string]string{"make": "bmake 20240711"}, "", "bmake"},
		{"falls back when gmake is too old", map[string]string{"gmake": apple, "make": gnu4}, "/bin/make", ""},
		{"nothing installed", map[string]string{}, "", "neither gmake nor make"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lookPath := func(name string) (string, error) {
				if _, ok := tt.installed[name]; ok {
					return "/bin/" + name, nil
				}
				return "", errors.New("not found")
			}
			version := func(_ context.Context, path string) (string, error) {
				return tt.installed[strings.TrimPrefix(path, "/bin/")], nil
			}
			got, err := findGNUMake(context.Background(), lookPath, version)
			if got != tt.want || (tt.wantError == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.wantError)) {
				t.Fatalf("got %q, %v; want %q, error containing %q", got, err, tt.want, tt.wantError)
			}
		})
	}
}
