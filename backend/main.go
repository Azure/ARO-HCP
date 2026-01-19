package main

import (
	"os"

	"github.com/Azure/ARO-HCP/backend/cmd"
)

func main() {
	cmdRoot := cmd.NewCmdRoot()
	if err := cmdRoot.Execute(); err != nil {
		cmdRoot.PrintErrln(cmdRoot.ErrPrefix(), err.Error())
		os.Exit(1)
	}
}
