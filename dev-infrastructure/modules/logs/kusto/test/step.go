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

package kustotest

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type stepID struct {
	index    int
	stepType string
	stepName string
}

func (s stepID) String() string {
	return fmt.Sprintf("%02d-%s-%s", s.index, s.stepType, s.stepName)
}

type kustoTestStep interface {
	StepID() stepID
	Run(ctx context.Context, t *testing.T, clients map[string]*emulatorClient)
}

type byIndex []kustoTestStep

func (s byIndex) Len() int           { return len(s) }
func (s byIndex) Less(i, j int) bool { return s[i].StepID().index < s[j].StepID().index }
func (s byIndex) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// parseStepDirName parses the "NN-<type>-<name>" step directory convention
// shared by readSteps and discoverTestTables.
func parseStepDirName(name string) (index int, stepType, stepName string, ok bool) {
	parts := strings.SplitN(name, "-", 3)
	if len(parts) < 3 {
		return 0, "", "", false
	}
	index, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", "", false
	}
	return index, parts[1], parts[2], true
}

func readSteps(testDir fs.FS) ([]kustoTestStep, error) {
	entries, err := fs.ReadDir(testDir, ".")
	if err != nil {
		return nil, fmt.Errorf("failed to read test directory: %w", err)
	}

	var steps []kustoTestStep
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		index, stepType, stepName, ok := parseStepDirName(entry.Name())
		if !ok {
			return nil, fmt.Errorf("step name %q must follow NN-<type>-<name> convention", entry.Name())
		}
		id := stepID{index: index, stepType: stepType, stepName: stepName}

		stepDir, err := fs.Sub(testDir, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("step %q: %w", entry.Name(), err)
		}

		step, err := newStep(id, stepDir)
		if err != nil {
			return nil, fmt.Errorf("step %q: %w", entry.Name(), err)
		}
		steps = append(steps, step)
	}

	sort.Sort(byIndex(steps))
	return steps, nil
}

func newStep(id stepID, stepDir fs.FS) (kustoTestStep, error) {
	switch id.stepType {
	case "ingestRows":
		return newIngestRowsStep(id, stepDir)
	case "invokeFunction":
		return newInvokeFunctionStep(id, stepDir)
	case "invokeQuery":
		return newInvokeQueryStep(id, stepDir)
	case "invokeMgmt":
		return newInvokeMgmtStep(id, stepDir)
	default:
		return nil, fmt.Errorf("unknown step type: %s", id.stepType)
	}
}
