package main

import (
	"context"
	"log/slog"
	"net"
	"os"

	"github.com/Azure/ARO-HCP/admin/pkg/admin"
)

func main() {

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	listener, err := net.Listen("tcp", ":8443")

	if err != nil {
		panic(err)
	}

	// Initialize the Admin server
	admin := admin.NewAdmin(logger, listener, "us-east")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	admin.Run(ctx, nil)
}
