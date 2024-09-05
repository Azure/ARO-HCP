package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/Azure/ARO-HCP/admin/pkg/admin"
)

func main() {

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	listener, _ := net.Listen("tcp", ":8446")

	// Initialize the Admin server
	admin := admin.NewAdmin(logger, listener, "us-east")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(10 * time.Second)
		cancel()
	}()

	admin.Run(ctx, nil)
}
