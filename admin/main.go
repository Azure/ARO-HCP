package main

import (
	"fmt"
	"log"
	"os"

	"github.com/Azure/ARO-HCP/admin/cmd"
	"github.com/Azure/ARO-HCP/admin/pkg/admin"
)

func main() {

	if err := cmd.NewRootCmd().Execute(); err != nil {
		log.Println(fmt.Errorf("%s error: %v", admin.ProgramName, err))
		os.Exit(1)
	}
}
