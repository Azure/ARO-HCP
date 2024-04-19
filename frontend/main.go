package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
)

const ProgramName = "ARO HCP Frontend"

var Version = "unknown" // overridden by Makefile

func main() {
	logger := DefaultLogger()

	logger.Info(fmt.Sprintf("%s (%s) started", ProgramName, Version))

	ctx := context.Background()
	stop := make(chan struct{})

	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)

	listener, err := net.Listen("tcp4", ":8443")
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	// Init prometheus emitter
	prometheusEmitter := NewPrometheusEmitter()
	frontend := NewFrontend(logger, listener, prometheusEmitter)
	go frontend.Run(ctx, stop)

	sig := <-signalChannel
	logger.Info(fmt.Sprintf("caught %s signal", sig))
	close(stop)
	frontend.Join()

	logger.Info(fmt.Sprintf("%s (%s) stopped", ProgramName, Version))
}
