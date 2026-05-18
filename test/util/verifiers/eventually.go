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

package verifiers

import (
	"context"
	"time"

	. "github.com/onsi/gomega"

	"github.com/onsi/ginkgo/v2"

	"k8s.io/client-go/rest"
)

// EventuallyVerify polls a HostedClusterVerifier until it succeeds or the
// timeout expires. It uses delta-only logging: failure details are logged only
// when the error message changes between polls, reducing noise from repeated
// identical states. Elapsed duration is logged on both success and failure.
//
// If the verifier implements DiagnosticVerifier and the poll times out,
// DiagnoseFailure is called and its output is logged to provide additional
// context for debugging.
func EventuallyVerify(ctx context.Context, verifier HostedClusterVerifier,
	restConfig *rest.Config, timeout, interval time.Duration, message string) {

	logger := ginkgo.GinkgoLogr
	var previousError string
	startTime := time.Now()

	succeeded := false
	defer func() {
		elapsed := time.Since(startTime)
		if succeeded {
			logger.Info("Verifier succeeded", "name", verifier.Name(), "elapsed", elapsed.String())
		} else {
			logger.Info("Verifier timed out", "name", verifier.Name(), "elapsed", elapsed.String())
			if dv, ok := verifier.(DiagnosticVerifier); ok {
				diagCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				diagnostics := dv.DiagnoseFailure(diagCtx, restConfig)
				if diagnostics != "" {
					logger.Info("Failure diagnostics", "name", verifier.Name(), "details", diagnostics)
				}
			}
		}
	}()

	Eventually(func() error {
		err := verifier.Verify(ctx, restConfig)
		if err != nil {
			currentError := err.Error()
			if currentError != previousError {
				logger.Info("Verifier check", "name", verifier.Name(), "status", "failed", "error", currentError)
				previousError = currentError
			}
		}
		return err
	}, timeout, interval).Should(Succeed(), message)

	succeeded = true
}
