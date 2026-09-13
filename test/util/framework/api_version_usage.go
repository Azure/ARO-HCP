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

package framework

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azcorepolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azcoreruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// hcpAPIVersionUsage records logical calls made through the HCP ARM SDK
// pipelines. It deliberately runs as a per-call policy, before the Azure SDK
// retry policy, so a transport retry does not inflate operation usage.
type hcpAPIVersionUsage struct {
	mu     sync.Mutex
	counts map[apiVersionOperation]int
}

type apiVersionOperation struct {
	version   string
	operation string
}

func newHCPAPIVersionUsage() *hcpAPIVersionUsage {
	return &hcpAPIVersionUsage{counts: make(map[apiVersionOperation]int)}
}

func (u *hcpAPIVersionUsage) Do(req *azcorepolicy.Request) (*http.Response, error) {
	u.record(req)
	return req.Next()
}

func (u *hcpAPIVersionUsage) record(req *azcorepolicy.Request) {
	raw := req.Raw()
	version := raw.URL.Query().Get("api-version")
	if version == "" {
		return
	}

	operation, _ := raw.Context().Value(azcoreruntime.CtxAPINameKey{}).(string)
	if operation == "" {
		operation = fallbackOperationName(raw.Method, raw.URL.Path)
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	u.counts[apiVersionOperation{version: version, operation: operation}]++
}

func fallbackOperationName(method, requestPath string) string {
	resourceID, err := azcorearm.ParseResourceID(requestPath)
	if err == nil && resourceID.ResourceType.String() != "" {
		return fmt.Sprintf("%s %s", method, resourceID.ResourceType)
	}
	return fmt.Sprintf("%s %s", method, requestPath)
}

// WriteHCPAPIVersionUsage writes a deterministic API-version-by-operation
// table. It is safe to call while E2E specs are executing.
func (u *hcpAPIVersionUsage) WriteHCPAPIVersionUsage(w io.Writer) {
	u.mu.Lock()
	entries := make([]apiVersionOperation, 0, len(u.counts))
	counts := make(map[apiVersionOperation]int, len(u.counts))
	for entry, count := range u.counts {
		entries = append(entries, entry)
		counts[entry] = count
	}
	u.mu.Unlock()

	if len(entries) == 0 {
		fmt.Fprintln(w, "HCP API version usage: no HCP API calls were made")
		return
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].version != entries[j].version {
			return entries[i].version < entries[j].version
		}
		return entries[i].operation < entries[j].operation
	})

	versionWidth, operationWidth := len("API version"), len("Operation")
	for _, entry := range entries {
		versionWidth = max(versionWidth, len(entry.version))
		operationWidth = max(operationWidth, len(entry.operation))
	}

	fmt.Fprintln(w, "HCP API version usage (logical SDK calls; transport retries excluded):")
	fmt.Fprintf(w, "%-*s  %-*s  %s\n", versionWidth, "API version", operationWidth, "Operation", "Count")
	fmt.Fprintf(w, "%s  %s  %s\n", strings.Repeat("-", versionWidth), strings.Repeat("-", operationWidth), "-----")
	for _, entry := range entries {
		fmt.Fprintf(w, "%-*s  %-*s  %d\n", versionWidth, entry.version, operationWidth, entry.operation, counts[entry])
	}
}

var suiteHCPAPIVersionUsage = newHCPAPIVersionUsage()

// WriteHCPAPIVersionUsageReport writes usage accumulated by all HCP client
// factories created by the E2E framework during this process.
func WriteHCPAPIVersionUsageReport(w io.Writer) {
	suiteHCPAPIVersionUsage.WriteHCPAPIVersionUsage(w)
}
