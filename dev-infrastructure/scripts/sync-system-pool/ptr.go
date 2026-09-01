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

package main

import (
	armcs "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

func strPtr(v string) *string { return &v }
func boolPtr(v bool) *bool    { return &v }

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func int32Deref(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func boolDeref(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

func toPtrSlice(v []string) []*string {
	if v == nil {
		return nil
	}
	out := make([]*string, len(v))
	for i := range v {
		out[i] = &v[i]
	}
	return out
}

func ptrSliceToStrings(v []*string) []string {
	if len(v) == 0 {
		return nil
	}
	out := make([]string, 0, len(v))
	for _, p := range v {
		if p != nil {
			out = append(out, *p)
		}
	}
	return out
}

func strPtrMapToStrings(m map[string]*string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = strDeref(v)
	}
	return out
}

func strDerefOSType(p *armcs.OSType) armcs.OSType {
	if p == nil {
		return ""
	}
	return *p
}

func strDerefOSSKU(p *armcs.OSSKU) armcs.OSSKU {
	if p == nil {
		return ""
	}
	return *p
}

func strDerefMode(p *armcs.AgentPoolMode) armcs.AgentPoolMode {
	if p == nil {
		return ""
	}
	return *p
}

func strDerefKubeletDiskType(p *armcs.KubeletDiskType) armcs.KubeletDiskType {
	if p == nil {
		return ""
	}
	return *p
}

func strDerefOSDiskType(p *armcs.OSDiskType) armcs.OSDiskType {
	if p == nil {
		return ""
	}
	return *p
}

func strDerefPoolType(p *armcs.AgentPoolType) armcs.AgentPoolType {
	if p == nil {
		return ""
	}
	return *p
}
