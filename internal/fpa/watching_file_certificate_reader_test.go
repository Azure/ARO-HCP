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
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestWatchingFileCertificateReaderLoadsInitialCertificate(t *testing.T) {
	dir := t.TempDir()
	bundleFileName := "bundle.crt"
	certFile := filepath.Join(dir, bundleFileName)

	// Create initial certificate
	var serialNumber int64 = 20
	atomicUpdateCert(t, dir, bundleFileName, serialNumber)

	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
	reader, err := NewWatchingFileCertificateReader(ctx, certFile, 50*time.Millisecond)
	require.NoError(t, err)

	certs, key, err := reader.ReadCertificate()
	require.NoError(t, err)
	assert.NotNil(t, certs)
	assert.NotNil(t, key)
	assert.Len(t, certs, 1)
	assert.Equal(t, serialNumber, certs[0].SerialNumber.Int64())
}

func TestWatchingFileCertificateReaderReloadsOnFileChange(t *testing.T) {
	dir := t.TempDir()
	bundleFileName := "bundle.crt"
	bundlePath := filepath.Join(dir, bundleFileName)

	var serialNumber int64 = 20
	atomicUpdateCert(t, dir, bundleFileName, serialNumber)

	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
	reader, err := NewWatchingFileCertificateReader(ctx, bundlePath, 50*time.Millisecond)
	require.NoError(t, err)

	certs1, _, err := reader.ReadCertificate()
	require.NoError(t, err)
	assert.Equal(t, serialNumber, certs1[0].SerialNumber.Int64())

	newSerialNumber := serialNumber + 1
	atomicUpdateCert(t, dir, bundleFileName, newSerialNumber)

	assert.Eventually(t, func() bool {
		certs2, _, err := reader.ReadCertificate()
		if err != nil {
			return false
		}
		return certs2[0].SerialNumber.Int64() == newSerialNumber
	}, 2*time.Second, 100*time.Millisecond, "certificate should be reloaded after file change")
}

func TestWatchingFileCertificateReaderCachesCertificate(t *testing.T) {
	dir := t.TempDir()
	bundleFileName := "bundle.crt"
	certFile := filepath.Join(dir, bundleFileName)

	atomicUpdateCert(t, dir, bundleFileName, 20)

	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
	reader, err := NewWatchingFileCertificateReader(ctx, certFile, 50*time.Millisecond)
	require.NoError(t, err)

	certs1, key1, err1 := reader.ReadCertificate()
	require.NoError(t, err1)

	certs2, key2, err2 := reader.ReadCertificate()
	require.NoError(t, err2)

	assert.Equal(t, certs1, certs2)
	assert.Equal(t, key1, key2)
}

func TestWatchingFileCertificateReaderStopsWatchingOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	bundleFileName := "bundle.crt"
	certFile := filepath.Join(dir, bundleFileName)

	var serialNumber int64 = 20
	atomicUpdateCert(t, dir, bundleFileName, serialNumber)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = utils.ContextWithLogger(ctx, testr.New(t))

	reader, err := NewWatchingFileCertificateReader(ctx, certFile, 50*time.Millisecond)
	require.NoError(t, err)

	certs1, _, err := reader.ReadCertificate()
	require.NoError(t, err)
	assert.Equal(t, serialNumber, certs1[0].SerialNumber.Int64())

	// Cancel the context to stop watching
	cancel()

	// Wait a bit for the watcher to stop
	time.Sleep(200 * time.Millisecond)

	// Update the certificate file
	var newSerialNumber = serialNumber + 1
	atomicUpdateCert(t, dir, bundleFileName, newSerialNumber)

	// Wait longer than the check interval to ensure watcher would have detected the change if still running
	time.Sleep(300 * time.Millisecond)

	// Verify that the certificate was NOT reloaded (still returns old cert)
	certs2, _, err := reader.ReadCertificate()
	require.NoError(t, err)
	assert.Equal(t, serialNumber, certs2[0].SerialNumber.Int64(), "certificate should not be reloaded after context cancellation")
}
