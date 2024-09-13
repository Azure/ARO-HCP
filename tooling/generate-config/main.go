package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Azure/ARO-HCP/tooling/generate-config/cmd/common"
	"github.com/Azure/ARO-HCP/tooling/generate-config/cmd/maestro"
	"github.com/spf13/cobra"
)

func main() {
	cmd := &cobra.Command{
		Use:              "generate-config",
		SilenceUsage:     true,
		TraverseChildren: true,

		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
			os.Exit(1)
		},
	}

	opts := common.DefaultPrimitiveOptions()
	common.BindPrimitiveOptions(opts, cmd.PersistentFlags())

	cmd.AddCommand(maestro.NewCommand(opts))

	cmdCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT)
	defer func() {
		cancel()
	}()
	go func() {
		<-cmdCtx.Done()
		_, _ = fmt.Fprintln(os.Stderr, "\nAborted...")
	}()

	if err := cmd.ExecuteContext(cmdCtx); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
