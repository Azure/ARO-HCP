// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

const billingCacheComplete = "complete"

// consumeCachedBillingWindow serves a subscription-window report from cacheDir,
// generating it at most once across concurrent collector processes. A file
// lock serializes generation; processes that wait on it then read the stored
// report parts. A failed generation leaves no complete marker, so the next
// process retries. Cached reports hold negotiated pricing and stay private.
func consumeCachedBillingWindow(ctx context.Context, client *http.Client, credential azcore.TokenCredential, sub string, window billingWindow, cacheDir string, consume func(io.Reader) error) error {
	if !discoveryUUID.MatchString(sub) {
		return errors.New("billing cache requires a subscription UUID")
	}
	dir := filepath.Join(cacheDir, strings.ToLower(sub), window.Start+"_"+window.End)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("billing cache directory unavailable")
	}
	parts, err := lockedBillingCacheFill(ctx, client, credential, sub, window, dir)
	if err != nil {
		return err
	}
	for i := range parts {
		f, err := os.Open(filepath.Join(dir, billingCachePart(i)))
		if err != nil {
			return errors.New("billing cache part unreadable")
		}
		err = consume(f)
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// lockedBillingCacheFill returns the number of cached report parts, generating
// and storing them first when no complete report is cached.
func lockedBillingCacheFill(ctx context.Context, client *http.Client, credential azcore.TokenCredential, sub string, window billingWindow, dir string) (int, error) {
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 0, errors.New("billing cache lock unavailable")
	}
	defer lock.Close()
	// Released by the kernel if this process dies, so a crashed generator never
	// blocks the others; they find no complete marker and generate again.
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return 0, errors.New("billing cache lock failed")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck // closing the file also releases the lock
	if marker, err := os.ReadFile(filepath.Join(dir, billingCacheComplete)); err == nil {
		if parts, err := strconv.Atoi(strings.TrimSpace(string(marker))); err == nil && parts >= 0 {
			return parts, nil
		}
	}
	parts := 0
	err = consumeBillingWindow(ctx, client, credential, sub, window, func(body io.Reader) error {
		name := filepath.Join(dir, billingCachePart(parts))
		tmp, err := os.CreateTemp(dir, "part-*.tmp")
		if err != nil {
			return errors.New("billing cache write failed")
		}
		_, err = io.Copy(tmp, body)
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(tmp.Name(), name)
		}
		if err != nil {
			os.Remove(tmp.Name())
			// Reading the body can fail mid-download; keep the generic billing wording.
			return errors.New("billing blob download or cache write failed (details omitted)")
		}
		parts++
		return nil
	})
	if err != nil {
		for i := 0; i < parts; i++ {
			os.Remove(filepath.Join(dir, billingCachePart(i)))
		}
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, billingCacheComplete), []byte(strconv.Itoa(parts)), 0o600); err != nil {
		return 0, errors.New("billing cache write failed")
	}
	return parts, nil
}

func billingCachePart(i int) string {
	return fmt.Sprintf("part-%03d.csv", i)
}
