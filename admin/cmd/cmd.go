package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/admin/pkg/admin"
)

type AdminOpts struct {
	location string
	port     int
}

func NewRootCmd() *cobra.Command {
	opts := &AdminOpts{}
	rootCmd := &cobra.Command{
		Use:     "aro-hcp-admin",
		Version: version(),
		Args:    cobra.NoArgs,
		Short:   "Serve the ARO HCP Admin",
		Long: `Serve the ARO HCP Admin
	This command runs the ARO HCP Admin. 
	# Run ARO HCP Admin locally 
	./aro-hcp-admin --location ${LOCATION} 
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run()
		},
	}

	rootCmd.Flags().StringVar(&opts.location, "location", os.Getenv("LOCATION"), "Azure location")
	rootCmd.Flags().IntVar(&opts.port, "port", 8443, "port to listen on")

	return rootCmd
}

func (opts *AdminOpts) Run() error {
	logger := DefaultLogger()
	logger.Info(fmt.Sprintf("%s (%s) started", admin.ProgramName, version()))

	listener, err := net.Listen("tcp4", fmt.Sprintf(":%d", opts.port))
	if err != nil {
		return err
	}

	if len(opts.location) == 0 {
		return errors.New("location is required")
	}
	logger.Info(fmt.Sprintf("Application running in %s", opts.location))

	adm := admin.NewAdmin(logger, listener, opts.location)

	stop := make(chan struct{})
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)
	go adm.Run(context.Background(), stop)

	sig := <-signalChannel
	logger.Info(fmt.Sprintf("caught %s signal", sig))
	close(stop)

	adm.Join()
	logger.Info(fmt.Sprintf("%s (%s) stopped", admin.ProgramName, version()))

	return nil
}

func version() string {
	version := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				version = setting.Value
				break
			}
		}
	}

	return version
}

func DefaultLogger() *slog.Logger {
	handlerOptions := slog.HandlerOptions{}
	handler := slog.NewJSONHandler(os.Stdout, &handlerOptions)
	logger := slog.New(handler)
	return logger
}
