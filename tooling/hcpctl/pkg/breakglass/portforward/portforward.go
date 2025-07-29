// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package portforward

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/cmd/portforward"
	"k8s.io/kubectl/pkg/cmd/util"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/utils/kubeclient"
)

// FindFreePort finds an available local port by binding to a random port
// and immediately releasing it. returns the port number that was allocated.
func FindFreePort() (int, error) {
	// listen on localhost with port 0 to get an OS-assigned free port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to find free port: %w", err)
	}
	defer listener.Close()

	// extract the port number from the assigned address
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("listener address is not TCP: %T", listener.Addr())
	}

	port := tcpAddr.Port
	if port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port number: %d", port)
	}

	return port, nil
}

// ForwardToService establishes port forwarding from a local port to a kubernetes service
// using kubectl's proven port-forward implementation. this function handles service-to-pod
// resolution automatically and provides reliable port forwarding for HCP breakglass access.
//
// parameters:
//   - ctx: context for cancellation and timeout control
//   - restConfig: kubernetes REST client configuration
//   - namespace: kubernetes namespace containing the target service
//   - serviceName: name of the service to forward to (without "service/" prefix)
//   - localPort: local port to bind for forwarding (1-65535)
//   - remotePort: target port on the service (1-65535)
//   - stopCh: channel to signal when port forwarding should stop
//   - readyCh: channel that gets closed when port forwarding is ready
func ForwardToService(ctx context.Context, restConfig *rest.Config, namespace, serviceName string, localPort, remotePort int, stopCh chan struct{}, readyCh chan struct{}) error {
	// create a child context that gets cancelled when our stopCh is closed
	stopCtx, stopCancel := context.WithCancel(ctx)
	defer stopCancel()

	// monitor our caller's stop channel and cancel context when signaled
	go func() {
		<-stopCh
		stopCancel()
	}()

	// create kubectl port forward options
	opts := portforward.NewDefaultPortForwardOptions(genericclioptions.IOStreams{})
	opts.Namespace = namespace
	opts.Address = []string{"127.0.0.1"}

	// create a minimal cobra command to satisfy kubectl's requirements
	// kubectl's Complete method needs this to extract timeout flags
	cmd := &cobra.Command{
		Use:   "port-forward",
		Short: "Forward local ports to a pod",
	}
	// add the pod-running-timeout flag that kubectl expects
	cmd.Flags().Duration("pod-running-timeout", 60*time.Second, "The timeout for waiting for pod to be running")

	// complete kubectl options with service target and ports
	// args format: ["service/serviceName", "localPort:remotePort"]
	args := []string{fmt.Sprintf("service/%s", serviceName), fmt.Sprintf("%d:%d", localPort, remotePort)}
	factory := util.NewFactory(kubeclient.NewRESTClientGetter(restConfig, namespace))
	if err := opts.Complete(factory, cmd, args); err != nil {
		return fmt.Errorf("failed to complete port forward options for service %s in namespace %s: %w", serviceName, namespace, err)
	}
	if err := opts.Validate(); err != nil {
		return fmt.Errorf("failed to validate port forward options for service %s: %w", serviceName, err)
	}

	// forward kubectl's ready signal to our caller's ready channel
	go func() {
		<-opts.ReadyChannel
		close(readyCh)
	}()

	// execute port forwarding using kubectl's implementation with context
	// RunPortForwardContext is blocking, so we need to run it in a goroutine
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		if err := opts.RunPortForwardContext(stopCtx); err != nil {
			errCh <- fmt.Errorf("port forwarding failed for service %s: %w", serviceName, err)
		}
	}()

	select {
	case <-readyCh:
		// port forwarding is ready, now wait for stop signal or error
		select {
		case err := <-errCh:
			// port forwarding ended with an error
			if err != nil {
				return err
			}
			// errCh was closed without error, meaning kubectl completed normally
			return nil
		case <-stopCh:
			// caller requested stop
			return nil
		case <-ctx.Done():
			// context was cancelled
			return ctx.Err()
		}
	case err := <-errCh:
		// port forwarding failed before becoming ready
		if err != nil {
			return err
		}
		// errCh was closed without error, meaning RunPortForwardContext completed normally
		// this shouldn't happen unless context was cancelled
		return fmt.Errorf("port forwarding completed unexpectedly")
	case <-ctx.Done():
		// context was cancelled before port forwarding became ready
		return fmt.Errorf("context cancelled before port forwarding became ready: %w", ctx.Err())
	}
}
