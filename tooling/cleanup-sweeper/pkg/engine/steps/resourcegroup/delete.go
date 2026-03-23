package resourcegroup

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	armhelpers "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/arm"
)

const ResourceType = "Microsoft.Resources/resourceGroups"

type DeleteStep struct {
	runner.DeletionStep
}

type DeleteStepConfig struct {
	ResourceGroupName string
	RGClient          *armresources.ResourceGroupsClient

	Name            string
	Retries         int
	ContinueOnError bool
	Verify          runner.VerifyFn
}

var _ runner.StepOptionsProvider = DeleteStepConfig{}

func (c DeleteStepConfig) StepOptions() runner.StepOptions {
	return runner.StepOptions{
		Name:            c.Name,
		Retries:         c.Retries,
		ContinueOnError: c.ContinueOnError,
		Verify:          c.Verify,
	}
}

func NewDeleteStep(cfg DeleteStepConfig) *DeleteStep {
	stepOptions := cfg.StepOptions()
	if stepOptions.Name == "" {
		stepOptions.Name = "Delete resource group"
	}

	step := &DeleteStep{
		DeletionStep: runner.DeletionStep{
			ResourceType: ResourceType,
			Options:      stepOptions,
		},
	}

	step.DiscoverFn = func(ctx context.Context, _ string) ([]runner.Target, error) {
		if cfg.ResourceGroupName == "" {
			return nil, fmt.Errorf("resource group name is required")
		}
		return []runner.Target{{Name: cfg.ResourceGroupName, Type: ResourceType}}, nil
	}

	step.DeleteFn = func(ctx context.Context, target runner.Target, wait bool) error {
		poller, err := cfg.RGClient.BeginDelete(ctx, target.Name, nil)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
				return nil
			}
			return err
		}
		if wait {
			return armhelpers.PollUntilDone(ctx, poller)
		}
		return nil
	}

	step.VerifyFn = func(ctx context.Context) error {
		if stepOptions.Verify == nil {
			return nil
		}
		return stepOptions.Verify(ctx)
	}

	return step
}
