package helm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
	"helm.sh/helm/v4/pkg/action"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/kube"
	helmrelease "helm.sh/helm/v4/pkg/release"
	helmreleasecommon "helm.sh/helm/v4/pkg/release/common"
	helmreleasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"

	corev1 "k8s.io/api/core/v1"
	kapierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	corev1applyconfigurations "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
)

// ErrNilHelmReleaseResult is returned when Helm reports success without a release object.
var ErrNilHelmReleaseResult = errors.New("helm release result is nil")

func DefaultOptions() *RawOptions {
	return &RawOptions{
		Timeout:            5 * time.Minute,
		StaleLockThreshold: DefaultStaleLockThreshold,
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringArrayVar(&opts.NamespaceFiles, "namespace-file", opts.NamespaceFiles, "Path to a namespace manifest to deploy before the Helm release.")

	cmd.Flags().StringVar(&opts.ReleaseName, "release-name", opts.ReleaseName, "Name of the Helm release being deployed.")
	cmd.Flags().StringVar(&opts.ReleaseNamespace, "release-namespace", opts.ReleaseNamespace, "Namespace in which the Helm release is deployed. Will create a basic namespace manifest unless a more complex namespace configuration is provided as a file.")
	cmd.Flags().StringVar(&opts.ChartDir, "chart-dir", opts.ChartDir, "Path to the directory containing the Helm chart.")
	cmd.Flags().StringVar(&opts.ValuesFile, "values-file", opts.ValuesFile, "Path to the Helm values file.")
	cmd.Flags().StringVar(&opts.Ev2RolloutVersion, "ev2-rollout-version", opts.Ev2RolloutVersion, "Version of the Ev2 rollout deploying this Helm chart.")

	cmd.Flags().StringVar(&opts.KustoDatabase, "kusto-database", opts.KustoDatabase, "Name of the Kusto database in the given cluster to use for diagnostics.")
	cmd.Flags().StringVar(&opts.KustoTable, "kusto-table", opts.KustoTable, "Name of the Kusto table in the given database to use for diagnostics.")
	cmd.Flags().StringVar(&opts.KustoEndpoint, "kusto-endpoint", opts.KustoEndpoint, "URI of the Kusto endpoint to use for diagnostics.")

	cmd.Flags().DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Timeout for waiting on the Helm release.")
	cmd.Flags().DurationVar(&opts.StaleLockThreshold, "stale-lock-threshold", opts.StaleLockThreshold, "Fail fast before deploying if the latest release revision has been stuck in a pending (install/upgrade/rollback) state for longer than this duration. Set to 0 to disable the stale-lock check.")

	cmd.Flags().StringVar(&opts.KubeconfigFile, "kubeconfig", opts.KubeconfigFile, "Path to the kubeconfig.")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "Do not make any changes to the Kubernetes API server.")
	cmd.Flags().BoolVar(&opts.RollbackOnFailure, "rollback-on-failure", opts.RollbackOnFailure, "Rollback the release on deployment failure.")

	return nil
}

// RawOptions holds input values.
type RawOptions struct {
	NamespaceFiles []string

	ReleaseName       string
	ReleaseNamespace  string
	ChartDir          string
	ValuesFile        string
	Ev2RolloutVersion string

	KustoDatabase string
	KustoTable    string
	KustoEndpoint string

	Timeout            time.Duration
	StaleLockThreshold time.Duration

	KubeconfigFile    string
	DryRun            bool
	RollbackOnFailure bool
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
	*cmdutils.ValidatedOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before Config generation can be invoked.
type completedOptions struct {
	Namespaces       []corev1.Namespace
	NamespacesClient corev1client.NamespaceInterface

	DynamicClient *dynamic.DynamicClient
	RESTMapper    meta.RESTMapper

	ActionConfig *action.Configuration

	ReleaseName       string
	ReleaseNamespace  string
	Chart             *chartv2.Chart
	Values            map[string]any
	Ev2RolloutVersion string

	KustoDatabase string
	KustoTable    string
	KustoEndpoint string

	Timeout            time.Duration
	StaleLockThreshold time.Duration
	DryRun             bool
	RollbackOnFailure  bool
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	for _, item := range []struct {
		flag  string
		name  string
		value *string
	}{
		{flag: "release-name", name: "Helm release name", value: &o.ReleaseName},
		{flag: "release-namespace", name: "Helm release namespace", value: &o.ReleaseNamespace},
		{flag: "chart-dir", name: "Helm chart directory", value: &o.ChartDir},
		{flag: "values-file", name: "Helm values file", value: &o.ValuesFile},
		{flag: "kubeconfig", name: "Kubeconfig file", value: &o.KubeconfigFile},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}

	if o.StaleLockThreshold < 0 {
		return nil, fmt.Errorf("the stale-lock threshold must not be negative; use --stale-lock-threshold=0 to disable the check, got %s", o.StaleLockThreshold)
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
		},
	}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	rawConfig, err := clientcmd.LoadFromFile(o.KubeconfigFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	cfg, err := clientcmd.NewNonInteractiveClientConfig(*rawConfig, rawConfig.CurrentContext, nil, nil).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create client config: %w", err)
	}

	// Create the Kubernetes clients
	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	var foundReleaseNamespace bool
	var namespaces []corev1.Namespace
	for _, file := range o.NamespaceFiles {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("failed to read namespace %s: %w", file, err)
		}
		ns := corev1.Namespace{}
		if err := yaml.Unmarshal(raw, &ns); err != nil {
			return nil, fmt.Errorf("failed to unmarshal namespace %s: %w", file, err)
		}
		foundReleaseNamespace = foundReleaseNamespace || ns.Name == o.ReleaseNamespace
		namespaces = append(namespaces, ns)
	}
	if !foundReleaseNamespace {
		// if the user hasn't provided an explicit manifest for the release namespace, let's add a minimal one
		namespaces = append(namespaces, corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: o.ReleaseNamespace}})
	}

	actionCfg := &action.Configuration{}

	cliOpts := &genericclioptions.ConfigFlags{
		KubeConfig: ptr.To(o.KubeconfigFile),
		Namespace:  ptr.To(o.ReleaseNamespace),
	}
	if err := actionCfg.Init(cliOpts, o.ReleaseNamespace, ""); err != nil {
		return nil, err
	}
	restMapper, err := cliOpts.ToRESTMapper()
	if err != nil {
		return nil, fmt.Errorf("failed to create RESTMapper: %w", err)
	}

	chartPath, err := filepath.Abs(o.ChartDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve chart directory %s: %w", o.ChartDir, err)
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load chart %s: %w", o.ChartDir, err)
	}

	rawValues, err := os.ReadFile(o.ValuesFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read values file %s: %w", o.ValuesFile, err)
	}

	values := map[string]interface{}{}
	if err := yaml.Unmarshal(rawValues, &values); err != nil {
		return nil, fmt.Errorf("failed to unmarshal values file %s: %w", o.ValuesFile, err)
	}

	return &Options{
		completedOptions: &completedOptions{
			Namespaces:       namespaces,
			NamespacesClient: clientset.CoreV1().Namespaces(),

			DynamicClient: dynamicClient,
			RESTMapper:    restMapper,

			ActionConfig: actionCfg,

			ReleaseName:      o.ReleaseName,
			ReleaseNamespace: o.ReleaseNamespace,

			Chart:             chart,
			Values:            values,
			Ev2RolloutVersion: o.Ev2RolloutVersion,

			KustoDatabase: o.KustoDatabase,
			KustoTable:    o.KustoTable,
			KustoEndpoint: o.KustoEndpoint,

			Timeout:            o.Timeout,
			StaleLockThreshold: o.StaleLockThreshold,
			DryRun:             o.DryRun,
			RollbackOnFailure:  o.RollbackOnFailure,
		},
	}, nil
}

func (opts *Options) Deploy(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	logger.Info("Resolved input values.", "values", opts.Values)

	logger.Info("Applying namespaces.")
	// Helm does not let us manage namespaces easily, so we need to apply them ourselves, up-front.
	// This is also the first call that reaches the API server, so it is where a transient failure to
	// obtain credentials (e.g. a momentary inability to reach IMDS when the exec credential plugin
	// mints the AKS token) first surfaces - retry those rather than failing the whole rollout step.
	for _, namespace := range opts.Namespaces {
		namespace := namespace
		if err := retryOnTransientCredentialError(ctx, logger, fmt.Sprintf("apply namespace %s", namespace.Name), func(ctx context.Context) error {
			return applyNamespace(ctx, logger, opts.NamespacesClient, namespace, opts.DryRun)
		}); err != nil {
			return err
		}
	}

	// we need to clear out previous `helm` managed field owners - but to do that, we need to know what is in our release;
	// to do *that*, we can run a dry-run first
	if !opts.DryRun {
		historyClient := action.NewHistory(opts.ActionConfig)
		historyClient.Max = 1
		versions, err := historyClient.Run(opts.ReleaseName)
		noReleaseYet := errors.Is(err, driver.ErrReleaseNotFound) || isReleaseUninstalled(logger, versions)

		if !noReleaseYet {
			// fail fast with actionable diagnostics if the latest revision is stuck in a
			// stale pending (install/upgrade/rollback) state, instead of letting Helm emit
			// the opaque "another operation ... is in progress" error during the upgrade.
			if opts.StaleLockThreshold > 0 {
				if err := checkForStaleReleaseLock(logger, opts.StaleLockThreshold, versions); err != nil {
					return err
				}
			}

			// only when a previous release exists, do we need to fixup managed fields
			opts.DryRun = true
			logger.Info("Doing dry-run of Helm release.")
			releaser, releaseErr := runHelmUpgrade(ctx, logger, opts)
			if releaseErr != nil {
				logger.Error(releaseErr, "Failed to dry-run the Helm release.")
				return fmt.Errorf("failed to dry-run Helm release: %w", releaseErr)
			}
			release, err := releaserToV1Release(releaser)
			if err != nil {
				return fmt.Errorf("failed to convert release to v1: %w", err)
			}

			if err := removeOldFieldManager(ctx, logger, opts, release); err != nil {
				return fmt.Errorf("failed to remove old field manager from Helm release: %w", err)
			}
			opts.DryRun = false
		}
	}

	// Start a deployment timer to use for finding relevant logs in runDiagnostics
	deploymentStart := time.Now()

	logger.Info("Rolling out Helm release.", "dryRun", opts.DryRun)
	releaser, releaseErr := runHelmUpgrade(ctx, logger, opts)
	if releaseErr != nil {
		logger.Error(releaseErr, "Failed to roll out the Helm release.")
		return fmt.Errorf("failed to roll out Helm release: %w", releaseErr)
	}
	release, err := releaserToV1Release(releaser)
	if err != nil {
		return fmt.Errorf("failed to convert release to v1: %w", err)
	}
	logger.Info("Finished deploying Helm release.")

	if opts.DryRun {
		logger.Info("Validating Helm release contents for dry-run.")
		if err := validateHelmResources(ctx, logger, opts, release); err != nil {
			return fmt.Errorf("failed to validate Helm release contents for dry-run: %w", err)
		}
		logger.Info("Finished validating Helm release contents.")
	} else {
		logger.Info("Running inline diagnostics.")
		if err := runDiagnostics(ctx, logger, opts, deploymentStart); err != nil {
			logger.Error(err, "Failed to capture diagnostics for Helm release")
		}
	}

	logger.Info("Deployment complete.")
	return releaseErr
}

func applyNamespace(ctx context.Context, logger logr.Logger, client corev1client.NamespaceInterface, namespace corev1.Namespace, dryRun bool) error {
	cfg := corev1applyconfigurations.Namespace(namespace.Name)
	if len(namespace.Labels) > 0 {
		cfg = cfg.WithLabels(namespace.Labels)
	}
	if len(namespace.Annotations) > 0 {
		cfg = cfg.WithAnnotations(namespace.Annotations)
	}
	if len(namespace.Spec.Finalizers) > 0 {
		cfg = cfg.WithSpec(corev1applyconfigurations.NamespaceSpec().WithFinalizers(namespace.Spec.Finalizers...))
	}

	var dryRunOpt []string
	if dryRun {
		dryRunOpt = append(dryRunOpt, "All")
	}
	if _, err := client.Apply(ctx, cfg, metav1.ApplyOptions{FieldManager: getManagedFieldsManager(), DryRun: dryRunOpt}); err != nil {
		return fmt.Errorf("failed to apply namespace %s: %w", namespace.Name, err)
	}
	logger.WithValues("namespace", namespace.Name).Info("Namespace applied successfully.")
	return nil
}

// transientCredentialErrorBackoff controls how often we retry an API-server call that failed with a
// transient credential/connection error. Worst case ~31s of waiting across 5 attempts (1, 2, 4, 8,
// 16s, capped at 30s), which comfortably absorbs a brief IMDS blip without meaningfully delaying a
// genuinely-broken rollout.
//
// It is a var so tests can substitute a fast, no-sleep backoff.
var transientCredentialErrorBackoff = func() wait.Backoff {
	return wait.Backoff{
		Duration: time.Second,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    5,
		Cap:      30 * time.Second,
	}
}

// isTransientCredentialError reports whether err looks like a transient failure to obtain
// credentials or otherwise reach the API server, as opposed to a deterministic Kubernetes API
// response (RBAC denial, invalid request, conflict, ...).
//
// The exec credential plugin (kubelogin, in azurecli mode) shells back to the managed identity /
// IMDS to mint the AKS token. When IMDS is momentarily unreachable the plugin exits non-zero and
// client-go returns a transport-level error with no Kubernetes API status attached, e.g.:
//
//	Patch "https://...azmk8s.io/api/v1/namespaces/acrpull": getting credentials:
//	exec: executable kubelogin failed with exit code 1
//
// Genuine auth/config problems (401 Unauthorized, 403 Forbidden, ...) come back as API status
// errors and must fail fast, so we only treat as retryable either errors that carry no API status
// at all (transport/credential failures) or the small set of API statuses that are themselves
// transient (timeouts, throttling, server-side unavailability).
func isTransientCredentialError(err error) bool {
	if err == nil {
		return false
	}
	// Caller-driven cancellation or timeout is not a transient API failure: retrying would spin
	// uselessly and hide the real reason the operation stopped. Fail fast so the context error
	// propagates to the caller.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiStatus kapierrors.APIStatus
	if !errors.As(err, &apiStatus) {
		// No Kubernetes API status: this is a transport-level / credential-plugin failure.
		return true
	}
	return kapierrors.IsServerTimeout(err) ||
		kapierrors.IsTimeout(err) ||
		kapierrors.IsTooManyRequests(err) ||
		kapierrors.IsInternalError(err) ||
		kapierrors.IsServiceUnavailable(err)
}

// retryOnTransientCredentialError runs fn, retrying with exponential backoff while it fails with a
// transient credential/connection error (see isTransientCredentialError). Deterministic failures are
// returned immediately so genuine auth/config problems still fail fast.
func retryOnTransientCredentialError(ctx context.Context, logger logr.Logger, description string, fn func(context.Context) error) error {
	var lastErr error
	condition := func(ctx context.Context) (bool, error) {
		err := fn(ctx)
		if err == nil {
			return true, nil
		}
		if !isTransientCredentialError(err) {
			return false, err
		}
		lastErr = err
		logger.WithValues("operation", description, "error", err.Error()).Info("Transient error reaching the API server; retrying.")
		return false, nil
	}
	if err := wait.ExponentialBackoffWithContext(ctx, transientCredentialErrorBackoff(), condition); err != nil {
		// Preserve caller cancellation/deadline so the real reason surfaces, rather than masking
		// it as "giving up after transient errors" (wait.Interrupted also matches context errors).
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if wait.Interrupted(err) && lastErr != nil {
			return fmt.Errorf("%s: giving up after transient errors: %w", description, lastErr)
		}
		return err
	}
	return nil
}

const ev2RolloutVersionLabel = "ev2.azure.com/rollout.version"

// runHelmUpgrade effectively re-packages `helm upgrade --install` by mirroring the logic here:
// https://github.com/helm/helm/blob/e2cbc5c0c94e6a12473fb7d1a7a232498aa4cda6/pkg/cmd/upgrade.go#L102
// This helps us over using exec.Command as:
//   - our deployer is self-contained and easy to use
//   - we get to control the version of Helm used to deploy directly, as opposed to being vulnerable to whatever the Ev2
//     or local environment we run in happens to use
func runHelmUpgrade(ctx context.Context, logger logr.Logger, opts *Options) (helmrelease.Releaser, error) {
	logger.Info("Searching for release history...")
	historyClient := action.NewHistory(opts.ActionConfig)
	historyClient.Max = 1
	versions, err := historyClient.Run(opts.ReleaseName)

	// If a release does not exist, install it.
	if err == driver.ErrReleaseNotFound || isReleaseUninstalled(logger, versions) {
		logger.Info("No release history found, running the first release...")
		installClient := action.NewInstall(opts.ActionConfig)
		installClient.ReleaseName = opts.ReleaseName
		installClient.WaitStrategy = kube.StatusWatcherStrategy
		installClient.WaitForJobs = true
		installClient.Namespace = opts.ReleaseNamespace
		installClient.Timeout = opts.Timeout
		installClient.ServerSideApply = true
		installClient.ForceConflicts = true
		installClient.SkipCRDs = false
		installClient.TakeOwnership = true
		installClient.RollbackOnFailure = opts.RollbackOnFailure

		if opts.DryRun {
			installClient.DryRunStrategy = "server"
			installClient.HideSecret = true
		}

		if opts.Ev2RolloutVersion != "" {
			installClient.Labels = map[string]string{
				ev2RolloutVersionLabel: opts.Ev2RolloutVersion,
			}
		}

		return installClient.RunWithContext(ctx, opts.Chart, opts.Values)
	}
	logger.Info("Found release history, upgrading...", "numVersions", len(versions))

	upgradeClient := action.NewUpgrade(opts.ActionConfig)
	upgradeClient.WaitStrategy = kube.StatusWatcherStrategy
	upgradeClient.WaitForJobs = true
	upgradeClient.Namespace = opts.ReleaseNamespace
	upgradeClient.Timeout = opts.Timeout
	upgradeClient.Install = true
	upgradeClient.ServerSideApply = "true"
	upgradeClient.ForceConflicts = true
	upgradeClient.TakeOwnership = true
	upgradeClient.RollbackOnFailure = opts.RollbackOnFailure

	if opts.DryRun {
		upgradeClient.DryRunStrategy = "server"
		upgradeClient.HideSecret = true
	}

	if opts.Ev2RolloutVersion != "" {
		upgradeClient.Labels = map[string]string{
			ev2RolloutVersionLabel: opts.Ev2RolloutVersion,
		}
	}

	return upgradeClient.RunWithContext(ctx, opts.ReleaseName, opts.Chart, opts.Values)
}

// https://github.com/helm/helm/blob/f4c5220d99723ca63dd0acb7302fe5b0971899f2/pkg/cmd/upgrade.go#L322
func isReleaseUninstalled(logger logr.Logger, versionsi []helmrelease.Releaser) bool {
	versions, err := releaseListToV1List(versionsi)
	if err != nil {
		logger.Error(err, "cannot convert release list to v1 release list")
		return false
	}
	return len(versions) > 0 && versions[len(versions)-1].Info.Status == helmreleasecommon.StatusUninstalled
}

func runDiagnostics(ctx context.Context, logger logr.Logger, opts *Options, deploymentStart time.Time) error {
	deploymentEnd := time.Now()

	statusClient := action.NewStatus(opts.ActionConfig)
	releaser, err := statusClient.Run(opts.ReleaseName)
	if err != nil {
		return fmt.Errorf("failed to get status for release %s: %w", opts.ReleaseName, err)
	}
	release, err := releaserToV1Release(releaser)
	if err != nil {
		return fmt.Errorf("failed to convert releaser to v1 release: %w", err)
	}

	hasInfo := release.Info != nil
	var status any = "<missing>"
	description := "<missing>"
	if hasInfo {
		status = release.Info.Status
		description = release.Info.Description
	}
	logger.Info(
		"Determined release status.",
		"release", release.Name,
		"namespace", release.Namespace,
		"hasInfo", hasInfo,
		"status", status,
		"description", description,
		"values", release.Config,
	)

	resourceCount := 0
	if release.Info != nil {
		resourceCount = len(release.Info.Resources)
	}
	if release.Info == nil || resourceCount == 0 {
		logger.Info("No resource info available in release, skipping resource diagnostics.",
			"infoNil", release.Info == nil,
			"resourceCount", resourceCount,
		)
		if release.Namespace != "" {
			nsLink, err := getNamespaceQuery(opts, release.Namespace, deploymentStart, deploymentEnd)
			if err != nil {
				logger.Error(err, "Failed to create Kusto deep link for namespace")
			} else if nsLink != "" {
				logger.Info("Kusto deep link for namespace-level troubleshooting", "kustoLinkNamespace", nsLink)
			}
		}
		return nil
	}

	ownerRefs := make(map[string][]OwnerRefInfo)
	var resources []ResourceInfo
	var foundPods []PodInfo

	// Process all resources in the release
	for _, resourceList := range release.Info.Resources {
		newOwners, newResources, newFoundPods, err := evaluateResources(logger, resourceList)
		if err != nil {
			logger.Error(err, "Failed to evaluate resources")
			continue
		}
		resources = append(resources, newResources...)
		foundPods = append(foundPods, newFoundPods...)
		for _, owner := range newOwners {
			addOwnerNoDuplicates(ownerRefs, owner)
		}
	}

	if len(resources) > 0 {
		logger.V(4).Info("Found resources in release:", "resources", resources)
		resourcesQuery, err := getKubeEventsQuery(opts, resources, deploymentStart, deploymentEnd)
		if err != nil {
			logger.Error(err, "Failed to log resources")
		} else if resourcesQuery != "" {
			logger.Info("Kube-events kusto link for troubleshooting:", "url", resourcesQuery)
		}
	} else {
		logger.V(4).Info("No resources found in release.")
	}

	// Initialize deep link variables for comprehensive troubleshooting
	var allPodsDeepLink, allOwnersDeepLink, namespaceDeepLink string

	if len(foundPods) > 0 {
		podQueries, podLink, err := getPodsQuery(logger, opts, foundPods, deploymentStart, deploymentEnd)
		if err != nil {
			logger.Error(err, "Failed to get pod details in the release")
		} else if len(podQueries) > 0 {
			logger.V(4).Info("Found pod details in the release", "Pods", podQueries)
			allPodsDeepLink = podLink
		}
	} else {
		logger.V(4).Info("No pods found in release.")
	}

	if len(ownerRefs) > 0 {
		ownerLink, err := getWorkloadResourcePodsLink(opts, ownerRefs, deploymentStart, deploymentEnd)
		if err != nil {
			logger.Error(err, "Failed to log owner references")
		} else {
			allOwnersDeepLink = ownerLink
		}
	} else {
		logger.V(4).Info("No owner references found in release.")
	}

	if release.Namespace != "" {
		nsLink, err := getNamespaceQuery(opts, release.Namespace, deploymentStart, deploymentEnd)
		if err != nil {
			logger.Error(err, "Failed to create Kusto deep link for namespace")
		} else {
			namespaceDeepLink = nsLink
		}
	}

	// Log all troubleshooting deep links together if any are available
	if allPodsDeepLink != "" || allOwnersDeepLink != "" || namespaceDeepLink != "" {
		logger.V(4).Info("Kusto deep links for comprehensive troubleshooting",
			"kustoLinkAllPods", allPodsDeepLink,
			"kustoLinkNamespace", namespaceDeepLink,
			"kustoLinkWorkloadResources", allOwnersDeepLink)
	}

	return nil
}

func validateHelmResources(ctx context.Context, logger logr.Logger, opts *Options, release *helmreleasev1.Release) error {
	logger.Info("Validating objects in release manifest.")
	inputDecoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewBuffer([]byte(release.Manifest)), 4096)
	failed := false
	for {
		ext := runtime.RawExtension{}
		if err := inputDecoder.Decode(&ext); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to parse release manifest: %v", err)
		}
		ext.Raw = bytes.TrimSpace(ext.Raw)
		if len(ext.Raw) == 0 || bytes.Equal(ext.Raw, []byte("null")) {
			continue
		}

		obj := &unstructured.Unstructured{}
		if err := yaml.Unmarshal(ext.Raw, obj); err != nil {
			return fmt.Errorf("failed to unmarshal release manifest: %v", err)
		}

		objLogger := logger.WithValues("gvk", obj.GroupVersionKind().String(), "namespace", obj.GetNamespace(), "name", obj.GetName())
		objLogger.Info("Decoded resource from manifests.")

		mapping, err := opts.RESTMapper.RESTMapping(obj.GroupVersionKind().GroupKind(), obj.GroupVersionKind().Version)
		if err != nil {
			return fmt.Errorf("unable to determine GVR mapping for GVK %s: %v", obj.GroupVersionKind(), err)
		}
		// for whatever reason, in dry-run mode Helm does not make the `{{ .Release }}` object available, so we need to set namespaces manually
		// for any resources that used the template `{{ .Release.namespace }}` - we can approximate this by looking for namespaces resources
		// that have no resource set
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace && obj.GetNamespace() == "" {
			obj.SetNamespace(opts.ReleaseNamespace)
		}

		if _, err := opts.DynamicClient.Resource(mapping.Resource).Namespace(obj.GetNamespace()).Apply(ctx, obj.GetName(), obj, metav1.ApplyOptions{
			FieldManager: getManagedFieldsManager(), DryRun: []string{"All"}, Force: true,
		}); err != nil {
			failed = true
			objLogger.Error(err, "Failed to validate resource using server-side dry-run.")

			current, err := opts.DynamicClient.Resource(mapping.Resource).Namespace(obj.GetNamespace()).Get(ctx, obj.GetName(), metav1.GetOptions{})
			if err != nil {
				objLogger.Error(err, "Failed to fetch current resource state for diffing.")
			}
			objLogger.Info("Printing diff between live object and intended manifest on disk.")
			fmt.Println(cmp.Diff(current, obj))
		} else {
			objLogger.Info("Validated resource using server-side dry-run.")
		}
	}
	if failed {
		return errors.New("failed validating release manifests")
	}
	return nil
}

// getManagedFieldsManager follows the (bizarre) mechanism that Helm uses to figure out the field manager
// see: https://github.com/helm/helm/blob/0adfe83ff8a46630164388c71620818e11253ece/pkg/kube/client.go#L838-L846
func getManagedFieldsManager() string {
	// When no calling application can be found it is unknown
	if len(os.Args[0]) == 0 {
		return "unknown"
	}

	// When there is an application that can be determined and no set manager
	// use the base name. This is one of the ways Kubernetes libs handle figuring
	// names out.
	return filepath.Base(os.Args[0])
}

// https://github.com/helm/helm/blob/f4c5220d99723ca63dd0acb7302fe5b0971899f2/pkg/action/get_values.go#L71C1-L84C2
func releaserToV1Release(rel helmrelease.Releaser) (*helmreleasev1.Release, error) {
	switch r := rel.(type) {
	case helmreleasev1.Release:
		return &r, nil
	case *helmreleasev1.Release:
		return r, nil
	case nil:
		return nil, ErrNilHelmReleaseResult
	default:
		return nil, fmt.Errorf("unsupported release type: %T", rel)
	}
}

// https://github.com/helm/helm/blob/f4c5220d99723ca63dd0acb7302fe5b0971899f2/pkg/cmd/root.go#L485
func releaseListToV1List(ls []helmrelease.Releaser) ([]*helmreleasev1.Release, error) {
	rls := make([]*helmreleasev1.Release, 0, len(ls))
	for _, val := range ls {
		rel, err := releaserToV1Release(val)
		if err != nil {
			return nil, err
		}
		rls = append(rls, rel)
	}

	return rls, nil
}

// Removes legacy Helm ownership without overwriting concurrent resource updates.
func removeOldFieldManager(ctx context.Context, logger logr.Logger, opts *Options, release *helmreleasev1.Release) error {
	logger.Info("Removing old field manager from objects in release manifest.")
	inputDecoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewBuffer([]byte(release.Manifest)), 4096)
	for {
		ext := runtime.RawExtension{}
		if err := inputDecoder.Decode(&ext); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to parse release manifest: %v", err)
		}
		ext.Raw = bytes.TrimSpace(ext.Raw)
		if len(ext.Raw) == 0 || bytes.Equal(ext.Raw, []byte("null")) {
			continue
		}

		obj := &unstructured.Unstructured{}
		if err := yaml.Unmarshal(ext.Raw, obj); err != nil {
			return fmt.Errorf("failed to unmarshal release manifest: %v", err)
		}

		objLogger := logger.WithValues("gvk", obj.GroupVersionKind().String(), "namespace", obj.GetNamespace(), "name", obj.GetName())
		objLogger.Info("Decoded resource from manifests.")

		mapping, err := opts.RESTMapper.RESTMapping(obj.GroupVersionKind().GroupKind(), obj.GroupVersionKind().Version)
		if err != nil {
			return fmt.Errorf("unable to determine GVR mapping for GVK %s: %v", obj.GroupVersionKind(), err)
		}
		// for whatever reason, in dry-run mode Helm does not make the `{{ .Release }}` object available, so we need to set namespaces manually
		// for any resources that used the template `{{ .Release.namespace }}` - we can approximate this by looking for namespaces resources
		// that have no resource set
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace && obj.GetNamespace() == "" {
			obj.SetNamespace(opts.ReleaseNamespace)
		}

		resource := opts.DynamicClient.Resource(mapping.Resource).Namespace(obj.GetNamespace())
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current, err := resource.Get(ctx, obj.GetName(), metav1.GetOptions{})
			if kapierrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			fields := current.GetManagedFields()
			toKeep := fields[:0]
			for _, field := range fields {
				if field.Manager != "helm" {
					toKeep = append(toKeep, field)
				}
			}
			if len(toKeep) == len(fields) {
				return nil
			}
			current.SetManagedFields(toKeep)
			if _, err := resource.Update(ctx, current, metav1.UpdateOptions{}); err != nil {
				return err
			}
			objLogger.Info("Removed old field manager from release manifest.")
			return nil
		}); err != nil {
			return fmt.Errorf("failed to remove old field manager from release manifest: %w", err)
		}
	}
	return nil
}
