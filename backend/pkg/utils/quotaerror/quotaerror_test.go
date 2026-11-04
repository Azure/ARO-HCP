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

package quotaerror

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The fixtures follow the formats produced by azcore ResponseError.Error(), the
// CAPZ condition message and Hypershift's aggregateMachineReasonsAndMessages.
// The Azure message text is illustrative.
// TODO: replace the Azure text with a message captured from a real failing node pool.
const (
	sdkSeparator = "--------------------------------------------------------------------------------"

	quotaText = "Operation could not be completed as it results in exceeding approved Total Regional Cores quota."

	// hypershiftMessageLimit and hypershiftTruncated mirror maxMessageLength and
	// endOfGlobalMessage in hypershift nodepool_controller.go.
	hypershiftMessageLimit = 1000
	hypershiftTruncated    = "... message truncated\n"
)

// sdkError mirrors ResponseError.Error() for a failed ARM request.
func sdkError(code, message string) string {
	return fmt.Sprintf("PUT https://management.azure.com/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm-a\n%s\nRESPONSE 409: 409 Conflict\nERROR CODE: %s\n%s\n{\n  \"error\": {\n    \"code\": \"%s\",\n    \"message\": \"%s\"\n  }\n}\n%s\n",
		sdkSeparator, code, sdkSeparator, code, message, sdkSeparator)
}

// machineEntry mirrors the entry Hypershift writes for a machine whose
// InfrastructureReady condition CAPZ set to reason Failed.
func machineEntry(name, sdk string) string {
	return fmt.Sprintf("Machine %s: Failed: virtualmachine failed to create or update. err: %s\n", name, sdk)
}

func allMachinesReady(notReady, total int, body string) string {
	return fmt.Sprintf("condition AllMachinesReady is False: %d of %d machines are not ready\n%s", notReady, total, body)
}

// updateMessage builds an update message shaped like the one the state
// calculation produces: replicas, then AllNodesHealthy, then AllMachinesReady
// (always last), joined with "; ".
func updateMessage(entries ...string) string {
	var b strings.Builder
	b.WriteString("hypershift NodePool status replicas is 0, want 2; ")
	// The AllNodesHealthy section also contains "Machine X:" entries. They are
	// symptoms of the same failure and must be ignored.
	b.WriteString("hypershift NodePool condition AllNodesHealthy is False: 2 of 2 machines are not healthy\n")
	b.WriteString("Machine pool-a: NodeProvisioning: Waiting for a node with matching ProviderID to exist\n; ")
	b.WriteString("hypershift NodePool " + allMachinesReady(len(entries), len(entries), strings.Join(entries, "")))
	return b.String()
}

func createMessage(entries ...string) string {
	return "node pool " + allMachinesReady(len(entries), len(entries), strings.Join(entries, ""))
}

func truncateLikeHypershift(entry string) string {
	return entry[:hypershiftMessageLimit-len(hypershiftTruncated)] + hypershiftTruncated
}

func TestDetect(t *testing.T) {
	quotaA := machineEntry("pool-a", sdkError("OperationNotAllowed", quotaText))
	quotaB := machineEntry("pool-b", sdkError("OperationNotAllowed", quotaText))
	resourceQuota := machineEntry("pool-a", sdkError("ResourceQuotaExceeded", "Resource quota exceeded."))
	quotaExceeded := machineEntry("pool-a", sdkError("QuotaExceeded", "Quota exceeded."))
	platform := machineEntry("pool-b", sdkError("InternalServerError", "Internal server error."))
	notAllowed := machineEntry("pool-a", sdkError("OperationNotAllowed", "Operation not allowed on a VM that is being deleted."))
	sku := machineEntry("pool-a", sdkError("SkuNotAvailable", "The requested VM size is not available."))
	// Quota words in the body of an unrelated error must not match.
	bodyOnly := machineEntry("pool-a", sdkError("InternalServerError", "QuotaExceeded was mentioned here."))
	noCode := "Machine pool-a: Failed: virtualmachine failed to create or update. err: QuotaExceeded in plain text\n"

	tests := []struct {
		name    string
		message string
		want    bool
	}{
		{"update, one machine over quota", updateMessage(quotaA), true},
		{"update, all machines over quota", updateMessage(quotaA, quotaB), true},
		{"create, one machine over quota", createMessage(quotaA), true},
		{"resource quota exceeded code", updateMessage(resourceQuota), true},
		{"quota exceeded code", updateMessage(quotaExceeded), true},

		// Anything not fully explained by quota is left alone.
		{"quota on one machine, platform failure on another", updateMessage(quotaA, platform), false},
		{"platform failure only", updateMessage(platform), false},
		{"operation not allowed without quota", updateMessage(notAllowed), false},
		{"a different customer cause is out of scope", updateMessage(sku), false},
		{"quota words in the body of another error code", updateMessage(bodyOnly), false},
		{"no SDK error code line", updateMessage(noCode), false},
		{"no machine entries", "hypershift NodePool condition AllMachinesReady is False: No Machines are created", false},
		{"no AllMachinesReady section", "hypershift NodePool status replicas is 0, want 2", false},
		{"empty message", "", false},
		{"no not ready header", "hypershift NodePool condition AllMachinesReady is False: \n" + quotaA, false},

		// Hypershift states how many machines are not ready. Fewer visible entries
		// means machines were dropped by truncation and may be unseen.
		{"header counts a machine that is not shown", "hypershift NodePool " + allMachinesReady(2, 2, quotaA), false},
		{"too many similar errors drops later machines", "hypershift NodePool " + allMachinesReady(3, 3, quotaA+"... too many similar errors\n"), false},
		{"message truncated drops a reason block", "hypershift NodePool " + allMachinesReady(2, 2, quotaA+"... message truncated\n"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := Detect(tt.message)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestDetect_OversizedSingleEntryCutByHypershift(t *testing.T) {
	long := machineEntry("pool-a", sdkError("OperationNotAllowed", quotaText+" "+strings.Repeat("Additional details. ", 60)))
	require.Greater(t, len(long), hypershiftMessageLimit, "fixture must exceed the Hypershift message limit")
	cut := truncateLikeHypershift(long)
	require.Contains(t, cut, hypershiftTruncated)

	// The only not ready machine is visible, so nothing is unseen.
	detail, ok := Detect("hypershift NodePool " + allMachinesReady(1, 1, cut))
	require.True(t, ok)
	require.True(t, strings.HasPrefix(detail, "Operation could not be completed as it results in exceeding approved Total Regional Cores quota."), detail)
	require.NotContains(t, detail, "... message truncated")

	// With a second not ready machine the cut dropped it, so no match.
	_, ok = Detect("hypershift NodePool " + allMachinesReady(2, 2, cut))
	require.False(t, ok)
}

func TestDetect_IgnoresSymptomSectionsBeforeAllMachinesReady(t *testing.T) {
	// A machine entry in the AllNodesHealthy section that is not a quota error
	// must not defeat an otherwise fully quota caused AllMachinesReady section.
	msg := updateMessage(machineEntry("pool-a", sdkError("OperationNotAllowed", quotaText)))
	require.Contains(t, msg, "Machine pool-a: NodeProvisioning:")
	_, ok := Detect(msg)
	require.True(t, ok)
}

func TestDetect_DetailIsAzureMessageOnly(t *testing.T) {
	detail, ok := Detect(updateMessage(machineEntry("pool-a", sdkError("OperationNotAllowed", quotaText))))
	require.True(t, ok)
	require.Equal(t, quotaText, detail)
	for _, internal := range []string{"management.azure.com", "ERROR CODE", "RESPONSE 409", "virtualMachines", sdkSeparator, "\n"} {
		require.NotContains(t, detail, internal)
	}
}

func TestDetect_DetailUnescapesJSON(t *testing.T) {
	detail, ok := Detect(createMessage(machineEntry("pool-a", sdkError("OperationNotAllowed", `exceeding \"quota\" for the family`))))
	require.True(t, ok)
	require.Equal(t, `exceeding "quota" for the family`, detail)
}

func TestDetect_DetailIsCapped(t *testing.T) {
	detail, ok := Detect(createMessage(machineEntry("pool-a", sdkError("OperationNotAllowed", "quota "+strings.Repeat("x", 2000)))))
	require.True(t, ok)
	require.LessOrEqual(t, len([]rune(detail)), maxDetailRunes+len("..."))
}

func TestDetect_QuotaCodeWithoutExtractableMessageHasEmptyDetail(t *testing.T) {
	entry := "Machine pool-a: Failed: virtualmachine failed to create or update. err: ERROR CODE: QuotaExceeded\n"
	detail, ok := Detect(createMessage(entry))
	require.True(t, ok)
	require.Empty(t, detail)
}
