// EventGrid MQTT clients pin `authenticationName` immutably. When a maestro
// certificate SAN changes - for example when a regional DNS zone is renamed -
// ARM rejects the update with "The authenticationName property for a client
// cannot be updated" and the rollout stays blocked until an operator deletes
// the client by hand under JIT.
//
// This reconciles that by deleting a client whose authenticationName no longer
// matches the desired SAN, so the ARM step that follows recreates it. It is a
// no-op when the client is absent or already correct.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

type config struct {
	subscriptionID     string
	resourceGroup      string
	namespaceName      string
	clientName         string
	authenticationName string
}

// clientsAPI is the subset of armeventgrid.ClientsClient this needs, so the
// reconcile logic can be tested without an Azure connection.
type clientsAPI interface {
	Get(ctx context.Context, resourceGroupName, namespaceName, clientName string, options *armeventgrid.ClientsClientGetOptions) (armeventgrid.ClientsClientGetResponse, error)
	Delete(ctx context.Context, resourceGroupName, namespaceName, clientName string) error
}

func main() {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid configuration: %v\n", err)
		os.Exit(1)
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to obtain Azure credentials: %v\n", err)
		os.Exit(1)
	}

	factory, err := armeventgrid.NewClientFactory(cfg.subscriptionID, cred, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to construct EventGrid client: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	if err := reconcile(ctx, &sdkClients{inner: factory.NewClientsClient()}, cfg, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// loadConfig accepts the namespace either as a full ARM resource ID or as its
// separate parts, because mgmt-pipeline already carries the ID as a step output
// while svc-pipeline only has the namespace name.
func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		clientName:         getenv("ClientName"),
		authenticationName: getenv("AuthenticationName"),
	}
	if cfg.clientName == "" {
		return config{}, errors.New("ClientName must be set")
	}
	if cfg.authenticationName == "" {
		return config{}, errors.New("AuthenticationName must be set")
	}

	if id := getenv("EventGridNamespaceId"); id != "" {
		parsed, err := azcorearm.ParseResourceID(id)
		if err != nil {
			return config{}, fmt.Errorf("EventGridNamespaceId is not a valid resource ID: %w", err)
		}
		if !strings.EqualFold(parsed.ResourceType.String(), "Microsoft.EventGrid/namespaces") {
			return config{}, fmt.Errorf("EventGridNamespaceId is not an EventGrid namespace: %s", parsed.ResourceType)
		}
		cfg.subscriptionID = parsed.SubscriptionID
		cfg.resourceGroup = parsed.ResourceGroupName
		cfg.namespaceName = parsed.Name
		return cfg, nil
	}

	cfg.subscriptionID = getenv("EventGridSubscriptionId")
	cfg.resourceGroup = getenv("EventGridResourceGroup")
	cfg.namespaceName = getenv("EventGridNamespaceName")
	for name, value := range map[string]string{
		"EventGridSubscriptionId": cfg.subscriptionID,
		"EventGridResourceGroup":  cfg.resourceGroup,
		"EventGridNamespaceName":  cfg.namespaceName,
	} {
		if value == "" {
			return config{}, fmt.Errorf("%s must be set when EventGridNamespaceId is not", name)
		}
	}
	return cfg, nil
}

// reconcile deletes the client when its authenticationName has drifted from the
// desired SAN. A read failure is returned rather than treated as an absent
// client: silently skipping reconciliation on a permissions error would
// reintroduce the immutable-authenticationName failure with no signal.
func reconcile(ctx context.Context, clients clientsAPI, cfg config, out *os.File) error {
	logf := func(format string, args ...any) {
		fmt.Fprintf(out, format+"\n", args...)
	}
	logf("Reconciling EventGrid MQTT client %s in namespace %s", cfg.clientName, cfg.namespaceName)

	resp, err := clients.Get(ctx, cfg.resourceGroup, cfg.namespaceName, cfg.clientName, nil)
	if err != nil {
		if isNotFound(err) {
			logf("Client %s does not exist; the ARM deployment will create it.", cfg.clientName)
			return nil
		}
		return fmt.Errorf("failed to read EventGrid client %s: %w", cfg.clientName, err)
	}

	var current string
	if resp.Properties != nil && resp.Properties.AuthenticationName != nil {
		current = *resp.Properties.AuthenticationName
	}
	if current == cfg.authenticationName {
		logf("Client %s already authenticates as %s; nothing to reconcile.", cfg.clientName, cfg.authenticationName)
		return nil
	}

	logf("authenticationName mismatch on %s:", cfg.clientName)
	logf("  current: %s", current)
	logf("  desired: %s", cfg.authenticationName)
	logf("authenticationName is immutable; deleting the client so the ARM deployment recreates it.")

	if err := clients.Delete(ctx, cfg.resourceGroup, cfg.namespaceName, cfg.clientName); err != nil {
		return fmt.Errorf("failed to delete EventGrid client %s: %w", cfg.clientName, err)
	}
	logf("Client %s deleted.", cfg.clientName)
	return nil
}

func isNotFound(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound
}

type sdkClients struct {
	inner *armeventgrid.ClientsClient
}

func (c *sdkClients) Get(ctx context.Context, resourceGroupName, namespaceName, clientName string, options *armeventgrid.ClientsClientGetOptions) (armeventgrid.ClientsClientGetResponse, error) {
	return c.inner.Get(ctx, resourceGroupName, namespaceName, clientName, options)
}

// Delete blocks until the deletion completes so the ARM step that follows does
// not race a pending delete.
func (c *sdkClients) Delete(ctx context.Context, resourceGroupName, namespaceName, clientName string) error {
	poller, err := c.inner.BeginDelete(ctx, resourceGroupName, namespaceName, clientName, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}
