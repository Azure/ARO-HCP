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

package lease

import (
	"errors"
	"path/filepath"
)

const (
	defaultEnvFileName       = "aro-hcp-slot.env"
	defaultSlotStateFileName = "aro-hcp-slot-state.yaml"
)

func SharedStateDir(sharedDir string) (string, error) {
	if sharedDir == "" {
		return "", errors.New("SHARED_DIR is empty")
	}
	return sharedDir, nil
}

func EnvFile(sharedDir string) (string, error) {
	stateDir, err := SharedStateDir(sharedDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, defaultEnvFileName), nil
}

func SlotStateFile(sharedDir string) (string, error) {
	stateDir, err := SharedStateDir(sharedDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, defaultSlotStateFileName), nil
}
