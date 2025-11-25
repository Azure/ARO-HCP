package main

import (
	"fmt"
	"os"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

func main() {
	err := framework.CreateIdentitiesPoolStateFile()
	if err != nil {
		fmt.Println("Failed to create MSI pool state file:", err)
		os.Exit(1)
	}
}
