package testutil

import (
	"os"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
	"gopkg.in/yaml.v2"
)

func shouldRunE2E() bool {
	return os.Getenv("RUN_TEMPLATIZE_E2E") == "true"
}

type E2E interface {
	Pipeline(step pipeline.Step, aksName string) error
	Persist() error
}

type e2eImpl struct {
	config   string
	makefile string
	pipeline string
	schema   string
	tmpdir   string
}

func newE2E(tmpdir string) e2eImpl {
	return e2eImpl{
		tmpdir: tmpdir,
		schema: `{"type": "object"}`,
		config: `
$schema: schema.json
defaults:
    region: {{ .ctx.region }}
    subscription: ARO Hosted Control Planes (EA Subscription 1)
    rg: hcp-templatize
    test_env: test_env
clouds:
    public:
        defaults:
        environments:
            dev:
                defaults:
`}
}

func (e *e2eImpl) SetPipeline(step pipeline.Step, aksName string) error {
	p := pipeline.Pipeline{
		ServiceGroup: "Microsoft.Azure.ARO.Test",
		RolloutName:  "Test Rollout",
		ResourceGroups: []*pipeline.ResourceGroup{
			{
				Name:         "{{ .rg }}",
				Subscription: "{{ .subscription }}",
				Steps: []*pipeline.Step{
					&step,
				},
			},
		},
	}
	if aksName != "" {
		p.ResourceGroups[0].AKSCluster = aksName
	}
	out, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	e.pipeline = string(out)
	return nil
}

func (e *e2eImpl) Persist() error {
	if e.makefile != "" {
		err := os.WriteFile(e.tmpdir+"/Makefile", []byte(e.makefile), 0644)
		if err != nil {
			return err
		}
	}
	err := os.WriteFile(e.tmpdir+"/config.yaml", []byte(e.config), 0644)
	if err != nil {
		return err
	}
	err = os.WriteFile(e.tmpdir+"/schema.json", []byte(e.schema), 0644)
	if err != nil {
		return err
	}
	return os.WriteFile(e.tmpdir+"/pipeline.yaml", []byte(e.pipeline), 0644)
}
