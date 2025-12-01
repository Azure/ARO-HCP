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

package fpa

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileCertificateReaderReadCertificate(t *testing.T) {
	dir := t.TempDir()
	bundleFileName := "bundle.crt"
	testFile := filepath.Join(dir, bundleFileName)

	var serialNumber int64 = 20
	atomicUpdateCert(t, dir, bundleFileName, serialNumber)

	reader := NewFileCertificateReader(testFile)

	certs, key, err := reader.ReadCertificate()
	require.NoError(t, err)
	assert.NotNil(t, certs)
	assert.NotNil(t, key)
	assert.Len(t, certs, 1)
	assert.Equal(t, serialNumber, certs[0].SerialNumber.Int64())
}

func TestFileCertificateReaderReadMissingFile(t *testing.T) {
	reader := NewFileCertificateReader("/nonexistent/path/file.pem")

	_, _, err := reader.ReadCertificate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read certificate file")
}

func TestFileCertificateReaderParseError(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "invalid.pem")

	err := os.WriteFile(testFile, []byte("invalid certificate data"), 0644)
	require.NoError(t, err)

	reader := NewFileCertificateReader(testFile)

	_, _, err = reader.ReadCertificate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse certificate")
}
