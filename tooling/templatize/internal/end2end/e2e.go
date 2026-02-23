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

package testutil

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/pipelines/topology"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	cmdopts "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
	pipelineopts "github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/options"
	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/run"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

var defaultRgName = "hcp-templatize"

func shouldRunE2E() bool {
	return os.Getenv("RUN_TEMPLATIZE_E2E") == "true"
}

type E2E interface {
	UseRandomRG() func() error
	AddBicepTemplate(template, templateFileName, paramfile, paramfileName string)
	EnableDryRun()
	Persist() (opts *run.RawRunOptions, err error)
}

type bicepTemplate struct {
	bicepFile     string
	bicepFileName string
	paramFile     string
	paramFileName string
}

type e2eImpl struct {
	config   map[string]any
	makefile string
	pipeline types.Pipeline
	biceps   []bicepTemplate
	schema   string
	tmpdir   string
	rgName   string
	dryRun   bool
}

var _ E2E = &e2eImpl{}

func newE2E(tmpdir string, pipelineFilePath string) (*e2eImpl, error) {
	imp := e2eImpl{
		tmpdir: tmpdir,
		schema: `{"type": "object"}`,
		config: map[string]any{
			"$schema": "schema.json",
			"defaults": map[string]any{
				"region":       "westus3",
				"subscription": "ARO Hosted Control Planes (EA Subscription 1)",
				"rg":           defaultRgName,
			},
			"clouds": map[string]any{
				"public": map[string]any{
					"defaults": map[string]any{},
					"environments": map[string]any{
						"dev": map[string]any{
							"defaults": map[string]any{},
						},
					},
				},
			},
		},
		rgName: defaultRgName,
		biceps: []bicepTemplate{},
	}

	pipelineBytes, err := os.ReadFile(pipelineFilePath)
	if err != nil {
		return nil, fmt.Errorf("error reading test pipeline %s, %v", pipelineFilePath, err)
	}
	if err := yaml.Unmarshal(pipelineBytes, &imp.pipeline); err != nil {
		return nil, fmt.Errorf("error loading pipeline %v", err)
	}
	return &imp, nil
}

func GenerateRandomRGName() string {
	rgSuffx := ""
	if jobID := os.Getenv("JOB_ID"); jobID != "" {
		rgSuffx = jobID
	}
	chars := []rune("abcdefghijklmnopqrstuvwxyz0123456789")
	for i := 0; i < 3; i++ {
		rgSuffx += string(chars[rand.IntN(len(chars))])
	}
	return "templatize-e2e-" + rgSuffx
}

func (e *e2eImpl) UseRandomRG() func() error {
	e.rgName = GenerateRandomRGName()
	defaults, ok := e.config["defaults"]
	if !ok {
		panic("defaults not set")
	}
	asMap, ok := defaults.(map[string]any)
	if !ok {
		panic(fmt.Sprintf("defaults not a map[string]any: %T", defaults))
	}
	asMap["rg"] = e.rgName
	e.config["defaults"] = asMap

	return func() error {
		subsriptionID, err := pipeline.LookupSubscriptionID(map[string]string{})(context.Background(), "ARO Hosted Control Planes (EA Subscription 1)")
		if err != nil {
			return err
		}
		cred, err := cmdutils.GetAzureTokenCredentials()
		if err != nil {
			return err
		}
		rgClient, err := armresources.NewResourceGroupsClient(subsriptionID, cred, nil)
		if err != nil {
			return err
		}
		_, err = rgClient.BeginDelete(context.Background(), e.rgName, nil)
		return err
	}
}

func (e *e2eImpl) EnableDryRun() {
	e.dryRun = true
}

func (e *e2eImpl) AddBicepTemplate(template, templateFileName, paramfile, paramfileName string) {
	e.biceps = append(e.biceps, bicepTemplate{
		bicepFile:     template,
		bicepFileName: templateFileName,
		paramFile:     paramfile,
		paramFileName: paramfileName,
	})
}

func (e *e2eImpl) Persist() (*run.RawRunOptions, error) {
	if len(e.biceps) != 0 {
		for _, b := range e.biceps {

			err := os.WriteFile(e.tmpdir+"/"+b.bicepFileName, []byte(b.bicepFile), 0644)
			if err != nil {
				return nil, err
			}

			if (len(b.paramFile) != 0) || (len(b.paramFileName) != 0) {
				err = os.WriteFile(e.tmpdir+"/"+b.paramFileName, []byte(b.paramFile), 0644)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	if e.makefile != "" {
		err := os.WriteFile(e.tmpdir+"/Makefile", []byte(e.makefile), 0644)
		if err != nil {
			return nil, err
		}
	}

	configBytes, err := yaml.Marshal(e.config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	configFile := filepath.Join(e.tmpdir, "config.yaml")
	err = os.WriteFile(configFile, configBytes, 0644)
	if err != nil {
		return nil, err
	}

	schemaFile := filepath.Join(e.tmpdir, "schema.json")
	err = os.WriteFile(schemaFile, []byte(e.schema), 0644)
	if err != nil {
		return nil, err
	}

	pipelineBytes, err := yaml.Marshal(e.pipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pipeline: %w", err)
	}

	pipelineFile := filepath.Join(e.tmpdir, "pipeline.yaml")
	if err := os.WriteFile(pipelineFile, pipelineBytes, 0644); err != nil {
		return nil, err
	}

	topo := topology.Topology{
		Services: []topology.Service{{
			ServiceGroup: e.pipeline.ServiceGroup,
			Purpose:      "Test pipeline.",
			PipelinePath: "pipeline.yaml",
		}},
	}
	rawTopo, err := yaml.Marshal(topo)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal topology: %w", err)
	}
	topologyFile := filepath.Join(e.tmpdir, "topology.yaml")
	if err := os.WriteFile(topologyFile, rawTopo, 0644); err != nil {
		return nil, err
	}

	return &run.RawRunOptions{
		PipelineOptions: &pipelineopts.RawPipelineOptions{
			RolloutOptions: &cmdopts.RawRolloutOptions{
				Region: "westus3",
				BaseOptions: &cmdopts.RawOptions{
					ConfigFile: configFile,
					Cloud:      "public",
					DeployEnv:  "dev",
				},
			},
			ServiceGroup: e.pipeline.ServiceGroup,
			TopologyFile: topologyFile,
		},
		DryRun:                   e.dryRun,
		NoPersist:                true,
		DeploymentTimeoutSeconds: 120,
	}, nil
}
