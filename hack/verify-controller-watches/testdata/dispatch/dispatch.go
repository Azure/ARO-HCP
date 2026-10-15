// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

// Package dispatch cannot import the storage contracts. Its interface call must
// nevertheless remain reachable when a controller supplies the implementation.
package dispatch

import "context"

const SelectedCallback = "qualified-selected"

type Worker interface{ Work(context.Context) }

func Forward(ctx context.Context, worker Worker) { forward(ctx, worker) }
func forward(ctx context.Context, worker Worker) { worker.Work(ctx) }

func Pure() string { return "no storage dependencies" }
