package pipeline

import (
	"context"
	"testing"
)

func TestResourceGroupRun(t *testing.T) {

	rg := ResourceGroup{
		Name:  "test-rg",
		Steps: []*Step{},
	}

	rg.run(context.Background(), &PipelineRunOptions{})
}
