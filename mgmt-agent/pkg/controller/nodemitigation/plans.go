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

package nodemitigation

import (
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

const ActionEvict = "EvictPod"

type Input struct {
	Node      *corev1.Node
	Detection detectors.Detection
	Policy    Config
	Now       time.Time
}

type Decision struct {
	Action string
	Hold   string
}

type Mitigator interface {
	Name() string
	DetectorNames() []string
	Plan(Input) (Decision, error)
}
