package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/admin/api/handlers"
	"github.com/Azure/ARO-HCP/admin/api/interrupts"
	"github.com/Azure/ARO-HCP/admin/api/middleware"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		Port:       8443,
		HealthPort: 8444,
	}
}

// RawOptions holds input values.
type RawOptions struct {
	Port       int
	HealthPort int
	Location   string
}

func (opts *RawOptions) BindOptions(cmd *cobra.Command) error {
	cmd.Flags().IntVar(&opts.Port, "port", opts.Port, "Port to serve content on.")
	cmd.Flags().IntVar(&opts.HealthPort, "health-port", opts.HealthPort, "Port to serve health and readiness on.")
	cmd.Flags().StringVar(&opts.Location, "location", opts.Location, "Location to serve content on.")
	return nil
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedOptions struct {
	Port       int
	HealthPort int
	Location   string
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
		},
	}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*Options, error) {

	return &Options{
		completedOptions: &completedOptions{
			Port:       o.Port,
			HealthPort: o.HealthPort,
			Location:   o.Location,
		},
	}, nil
}

func (opts *Options) Run(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	logger.Info("Reporting health.", "port", opts.HealthPort)
	health := NewHealthOnPort(logger, opts.HealthPort)
	health.ServeReady(func() bool {
		// todo: add real readiness checks
		return true
	})

	logger.Info("Running server", "port", opts.Port)
	mux := http.NewServeMux()
	mux.Handle("GET /admin/helloworld", handlers.HelloWorldHandler())
	s := http.Server{
		Addr:    net.JoinHostPort("", strconv.Itoa(opts.Port)),
		Handler: middleware.WithURLPathValue(middleware.WithLogger(logger, mux)),
	}
	interrupts.ListenAndServe(&s, 5*time.Second)
	interrupts.WaitForGracefulShutdown()
	return nil
}
