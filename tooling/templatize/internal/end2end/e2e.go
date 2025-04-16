package testutil

import (
	"context"
	"fmt"
	"os"

	"math/rand/v2"

	"gopkg.in/yaml.v2"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-Tools/pkg/config"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/azauth"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

var defaultRgName = "hcp-templatize"

func shouldRunE2E() bool {
	return os.Getenv("RUN_TEMPLATIZE_E2E") == "true"
}

type E2E interface {
	SetConfig(updates config.Configuration)
	UseRandomRG() func() error
	AddBicepTemplate(template, templateFileName, paramfile, paramfileName string)
	AddStep(step pipeline.Step, rg int)
	SetOSArgs()
	EnableDryRun()
	Persist() error
}

type bicepTemplate struct {
	bicepFile     string
	bicepFileName string
	paramFile     string
	paramFileName string
}

type e2eImpl struct {
	config   config.Configuration
	makefile string
	pipeline pipeline.Pipeline
	biceps   []bicepTemplate
	schema   string
	tmpdir   string
	rgName   string
}

var _ E2E = &e2eImpl{}

func newE2E(tmpdir string) e2eImpl {
	imp := e2eImpl{
		tmpdir: tmpdir,
		schema: `{"type": "object"}`,
		config: config.Configuration{
			"$schema": "schema.json",
			"defaults": config.Configuration{
				"region":       "westus3",
				"subscription": "ARO Hosted Control Planes (EA Subscription 1)",
				"rg":           defaultRgName,
			},
			"clouds": config.Configuration{
				"public": config.Configuration{
					"defaults": config.Configuration{},
					"environments": config.Configuration{
						"dev": config.Configuration{
							"defaults": config.Configuration{},
						},
					},
				},
			},
		},
		pipeline: pipeline.Pipeline{
			ServiceGroup: "Microsoft.Azure.ARO.Test",
			RolloutName:  "Test Rollout",
			ResourceGroups: []*pipeline.ResourceGroup{
				{
					Name:         "{{ .rg }}",
					Subscription: "{{ .subscription }}",
				},
			},
		},
		rgName: defaultRgName,
		biceps: []bicepTemplate{},
	}

	imp.SetOSArgs()
	return imp
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
	e.SetConfig(config.Configuration{"defaults": config.Configuration{"rg": e.rgName}})

	return func() error {
		subsriptionID, err := pipeline.LookupSubscriptionID(context.Background(), "ARO Hosted Control Planes (EA Subscription 1)")
		if err != nil {
			return err
		}
		cred, err := azauth.GetAzureTokenCredentials()
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

func (e *e2eImpl) SetOSArgs() {
	os.Args = []string{"test",
		"--cloud", "public",
		"--pipeline-file", e.tmpdir + "/pipeline.yaml",
		"--config-file", e.tmpdir + "/config.yaml",
		"--deploy-env", "dev",
		"--no-persist-tag",
		"--region", "westus3",
	}
}

func (e *e2eImpl) EnableDryRun() {
	os.Args = append(os.Args, "--dry-run")
}

func (e *e2eImpl) AddResourceGroup() {
	numRgs := len(e.pipeline.ResourceGroups)
	e.pipeline.ResourceGroups = append(e.pipeline.ResourceGroups, &pipeline.ResourceGroup{
		Name:         fmt.Sprintf("{{ .rg }}-%d", numRgs+1),
		Subscription: "{{ .subscription }}",
	},
	)
}

func (e *e2eImpl) SetAKSName(aksName string) {
	e.pipeline.ResourceGroups[0].AKSCluster = aksName
}

func (e *e2eImpl) AddStep(step pipeline.Step, rg int) {
	e.pipeline.ResourceGroups[rg].Steps = append(e.pipeline.ResourceGroups[rg].Steps, step)
}

func (e *e2eImpl) SetConfig(updates config.Configuration) {
	config.MergeConfiguration(e.config, updates)
}

func (e *e2eImpl) AddBicepTemplate(template, templateFileName, paramfile, paramfileName string) {
	e.biceps = append(e.biceps, bicepTemplate{
		bicepFile:     template,
		bicepFileName: templateFileName,
		paramFile:     paramfile,
		paramFileName: paramfileName,
	})
}

func (e *e2eImpl) Persist() error {
	if len(e.biceps) != 0 {
		for _, b := range e.biceps {

			err := os.WriteFile(e.tmpdir+"/"+b.bicepFileName, []byte(b.bicepFile), 0644)
			if err != nil {
				return err
			}

			err = os.WriteFile(e.tmpdir+"/"+b.paramFileName, []byte(b.paramFile), 0644)
			if err != nil {
				return err
			}
		}
	}

	if e.makefile != "" {
		err := os.WriteFile(e.tmpdir+"/Makefile", []byte(e.makefile), 0644)
		if err != nil {
			return err
		}
	}

	configBytes, err := yaml.Marshal(e.config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	err = os.WriteFile(e.tmpdir+"/config.yaml", configBytes, 0644)
	if err != nil {
		return err
	}

	err = os.WriteFile(e.tmpdir+"/schema.json", []byte(e.schema), 0644)
	if err != nil {
		return err
	}

	pipelineBytes, err := yaml.Marshal(e.pipeline)
	if err != nil {
		return fmt.Errorf("failed to marshal pipeline: %w", err)
	}
	return os.WriteFile(e.tmpdir+"/pipeline.yaml", []byte(pipelineBytes), 0644)
}
