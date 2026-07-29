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

package api_test

import (
	"math/rand"
	"testing"

	"github.com/Azure/ARO-HCP/internal/api"
	apitesting "github.com/Azure/ARO-HCP/internal/api/testing"
)

func TestDeepCopyHCPOpenShiftCluster(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := apitesting.DeepCopyFuzzerFor(rand.NewSource(seed))

	for i := 0; i < 200; i++ {
		original := &api.HCPOpenShiftCluster{}
		fuzzer.Fill(original)
		apitesting.DoDeepCopyTest(t, original, fuzzer)
	}
}

func TestDeepCopyHCPOpenShiftClusterNodePool(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := apitesting.DeepCopyFuzzerFor(rand.NewSource(seed))

	for i := 0; i < 200; i++ {
		original := &api.HCPOpenShiftClusterNodePool{}
		fuzzer.Fill(original)
		apitesting.DoDeepCopyTest(t, original, fuzzer)
	}
}

func TestDeepCopyOperation(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)

	fuzzer := apitesting.DeepCopyFuzzerFor(rand.NewSource(seed))

	for i := 0; i < 200; i++ {
		original := &api.Operation{}
		fuzzer.Fill(original)
		apitesting.DoDeepCopyTest(t, original, fuzzer)
	}
}
