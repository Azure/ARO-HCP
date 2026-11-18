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

package cipatterns

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAdmissionClosesBeforeSuccessfulDrain(t *testing.T) {
	c := newTestController(t)
	require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))
	version, allowed := c.tryAdmitNormalReconciliation()
	require.True(t, allowed)
	require.Equal(t, compiledFailurePatternVersion(), version)
	done := make(chan error, 1)
	go func() { done <- c.pauseNormalReconciliationAndWait(t.Context()) }()
	require.Eventually(t, func() bool { _, allowed := c.normalReconciliationVersion(); return !allowed }, time.Second, time.Millisecond)
	_, allowed = c.tryAdmitNormalReconciliation()
	require.False(t, allowed)
	require.Error(t, c.allowNormalReconciliation(compiledFailurePatternVersion()), "cannot reopen while admitted work remains")
	c.finishNormalReconciliation()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("successful drain did not join admitted work")
	}
	require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))
}

func TestExplicitReconciliationCannotRelabelCurrentExtractor(t *testing.T) {
	c := newTestController(t)
	_, err := c.reconcileInputs(t.Context(), failurePatternVersion{extractorVersion: "historical-label", resultSchemaVersion: resultSchemaVersion}, []input{sampleInput()})
	require.ErrorContains(t, err, "executable")
}
