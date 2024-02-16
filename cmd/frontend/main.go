package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Azure/ARO-HCP/pkg/util/version"
)

const ProgramName = "ARO HCP Frontend"

func main() {
	// FIXME Centralize logging options?
	handlerOptions := slog.HandlerOptions{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &handlerOptions))

	logger.Info(fmt.Sprintf("%s (%s) started", ProgramName, version.Version))

	ctx := context.Background()
	stop := make(chan struct{})

	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)

	listener, err := net.Listen("tcp4", ":8443")
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	frontend := NewFrontend(logger, listener)
	go frontend.Run(ctx, stop)

	sig := <-signalChannel
	logger.Info(fmt.Sprintf("caught %s signal", sig))
	close(stop)
	frontend.Join()

	logger.Info(fmt.Sprintf("%s (%s) stopped", ProgramName, version.Version))
}
