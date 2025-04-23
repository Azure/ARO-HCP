package main


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
