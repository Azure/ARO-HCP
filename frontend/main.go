package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"log"
	"os"

	"github.com/Azure/ARO-HCP/frontend/cmd"
	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
)

func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		log.Println(fmt.Errorf("%s error: %v", frontend.ProgramName, err))
		os.Exit(1)
	}
}
