// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package controllerconfig

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSingleWorker(t *testing.T) {
	controller := &pollingController{}
	SingleWorker{Controller: controller}.Run(t.Context(), 4)
	require.Equal(t, 1, controller.calls, "adapter must not multiply the controller's own workers")
	require.Equal(t, t.Context(), controller.ctx)
}

type pollingController struct {
	calls int
	ctx   context.Context
}

func (c *pollingController) Run(ctx context.Context) {
	c.calls++
	c.ctx = ctx
}
