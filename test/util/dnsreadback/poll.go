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

package dnsreadback

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

var ErrInconsistent = errors.New("inconsistent DNS answer")

func Wait(ctx context.Context, interval, timeout time.Duration, requiredPolls int, readback func(context.Context) error) error {
	goodPolls := 0
	var lastErr error
	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		lastErr = readback(ctx)
		if lastErr == nil {
			goodPolls++
		} else {
			var dnsErr *net.DNSError
			var netErr net.Error
			isDNSError := errors.As(lastErr, &dnsErr)
			inconsistent := errors.Is(lastErr, ErrInconsistent) || (isDNSError && dnsErr.IsNotFound)
			transient := errors.Is(lastErr, context.DeadlineExceeded) || (errors.As(lastErr, &netErr) && netErr.Timeout()) ||
				(isDNSError && (dnsErr.IsTemporary || dnsErr.IsTimeout))
			if inconsistent || !transient {
				goodPolls = 0
			}
		}
		return goodPolls >= requiredPolls, nil
	})
	if err != nil {
		return fmt.Errorf("%w: %d/%d successful DNS polls since last non-transient error; last readback error: %v", err, goodPolls, requiredPolls, lastErr)
	}
	return nil
}
