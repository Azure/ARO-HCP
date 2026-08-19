package controlplaneversion

import (
	"context"
	"fmt"
	"net/url"

	configv1 "github.com/openshift/api/config/v1"
)

// ARO-HCP specific user agent; put whatever you like in here to identify yourself.
var userAgent = "AROHCPFixme/0.1"

// SelectControlPlaneVersion retrieves a release from a chosen
// OpenShift update service channel with a chosen offset.  For
// example, the most recent release in the fast-4.20 channel.  Or the
// penultimate release in the stable-4.22 channel.
func SelectControlPlaneVersion(ctx context.Context, roundTripper RoundTrip, updateService *url.URL, channel string, offset uint) (*configv1.Release, error) {
	releases, updateService, err := cincinnati(ctx, roundTripper, updateService, userAgent, channel)
	if err != nil {
		return nil, err
	}
	if len(releases) == 0 {
		return nil, fmt.Errorf("no releases found in %s.", updateService)
	}
	if int(offset) >= len(releases) {
		return nil, fmt.Errorf("%d releases found in %s, which is not enough for the requested %d offset.", len(releases), updateService, offset)
	}
	return &releases[offset], nil
}
