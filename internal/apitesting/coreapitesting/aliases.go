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

package coreapitesting

import (
	cosmosclientcoreapitesting "github.com/Azure/ARO-HCP/arohcp-cosmos-client/apitesting/coreapitesting"
)

// The fuzzing helpers live in arohcp-cosmos-client/apitesting/coreapitesting,
// next to the API types they fuzz. These aliases keep existing callers of this
// package working.
var (
	FuzzArmResourceID        = cosmosclientcoreapitesting.FuzzArmResourceID
	FuzzInternalID           = cosmosclientcoreapitesting.FuzzInternalID
	GenName                  = cosmosclientcoreapitesting.GenName
	FuzzerFor                = cosmosclientcoreapitesting.FuzzerFor
	DeepCopyFuzzerFor        = cosmosclientcoreapitesting.DeepCopyFuzzerFor
	DoDeepCopyTest           = cosmosclientcoreapitesting.DoDeepCopyTest
	CommonRoundTripFuzzFuncs = cosmosclientcoreapitesting.CommonRoundTripFuzzFuncs
	CommonDeepCopyFuzzFuncs  = cosmosclientcoreapitesting.CommonDeepCopyFuzzFuncs
)
