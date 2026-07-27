// Copyright 2025 Microsoft Corporation
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

package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAzureLog(t *testing.T) {
	t.Run("writes content to azure_sdk_log/azure.log", func(t *testing.T) {
		dir := t.TempDir()
		content := []byte(`{"time":"2025-01-01T00:00:00Z","msg":"Request","method":"PUT"}`)

		if err := WriteAzureLog(dir, &AzureLogFile{Content: content}); err != nil {
			t.Fatalf("WriteAzureLog returned error: %v", err)
		}

		got, err := os.ReadFile(filepath.Join(dir, "azure_sdk_log", "azure.log"))
		if err != nil {
			t.Fatalf("failed to read written azure.log: %v", err)
		}
		if string(got) != string(content) {
			t.Errorf("azure.log content mismatch:\n got: %q\nwant: %q", got, content)
		}
	})

	t.Run("nil log is a no-op", func(t *testing.T) {
		dir := t.TempDir()
		if err := WriteAzureLog(dir, nil); err != nil {
			t.Fatalf("WriteAzureLog(nil) returned error: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "azure_sdk_log")); !os.IsNotExist(err) {
			t.Errorf("expected no azure_sdk_log directory, stat err=%v", err)
		}
	})

	t.Run("empty content is a no-op", func(t *testing.T) {
		dir := t.TempDir()
		if err := WriteAzureLog(dir, &AzureLogFile{Content: []byte{}}); err != nil {
			t.Fatalf("WriteAzureLog(empty) returned error: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "azure_sdk_log")); !os.IsNotExist(err) {
			t.Errorf("expected no azure_sdk_log directory, stat err=%v", err)
		}
	})
}

func TestDirectoryLayoutIncludesAzureSDKLog(t *testing.T) {
	layout := directoryLayout()
	if _, ok := layout["azure_sdk_log"]; !ok {
		t.Errorf("directoryLayout() is missing the azure_sdk_log entry: %v", layout)
	}
}
