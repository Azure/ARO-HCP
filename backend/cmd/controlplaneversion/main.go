package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"

	"github.com/blang/semver/v4"

	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controlplaneversion"
)

func main() {
	klog.InitFlags(flag.CommandLine)
	flag.Parse()
	args := flag.Args()

	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s CHANNEL_STABILITY X.Y [UPSTREAM_UPDATE_SERVICE]\n", os.Args[0])
		os.Exit(1)
	}
	channelStability := args[0]
	desiredXYVersion, err := semver.Parse(fmt.Sprintf("%s.0", args[1]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "expected X.Y version, but failed to parse: %v\n", err)
		os.Exit(1)
	}
	var upstreamUpdateService string
	if len(args) >= 3 {
		upstreamUpdateService = args[2]
	} else {
		upstreamUpdateService = controlplaneversion.DefaultUpstreamUpdateService
	}

	updateService, err := url.Parse(upstreamUpdateService)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
		os.Exit(1)
	}

	version, err := controlplaneversion.SelectControlPlaneVersion(context.Background(), channelStability, desiredXYVersion, nil, updateService, nil)
	fmt.Printf("%v (%v)\n", version, err)
}
