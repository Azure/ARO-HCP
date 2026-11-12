package helm

import (
	"fmt"
	"time"

	"github.com/go-logr/logr"
	helmrelease "helm.sh/helm/v4/pkg/release"
	helmreleasev1 "helm.sh/helm/v4/pkg/release/v1"
)

// DefaultStaleLockThreshold is how old a pending Helm release revision must be
// before helmdeploy treats it as a stale lock and fails fast instead of letting
// Helm return the opaque "another operation (install/upgrade/rollback) is in
// progress" error.
const DefaultStaleLockThreshold = 15 * time.Minute

// StaleReleaseLockError is returned when the latest release revision is stuck in
// a pending state (pending-install, pending-upgrade, or pending-rollback) and is
// older than the configured staleness threshold. It carries enough context for
// an operator to identify and remediate the stale lock.
type StaleReleaseLockError struct {
	ReleaseName string
	Namespace   string
	Revision    int
	Status      string
	Age         time.Duration
	Threshold   time.Duration
	SecretName  string
}

func (e *StaleReleaseLockError) Error() string {
	return fmt.Sprintf(
		"helm release %q in namespace %q is stuck in pending state %q at revision %d "+
			"(age %s exceeds staleness threshold %s); a previous Helm operation most likely "+
			"crashed or timed out and left a stale release lock.\n"+
			"To recover, back up and delete the stale Helm release secret, then retry the deployment:\n"+
			"  kubectl --namespace %s get secret %s -o yaml > %s.backup.yaml\n"+
			"  kubectl --namespace %s delete secret %s\n"+
			"Only delete the secret after confirming no Helm operation is genuinely still running.",
		e.ReleaseName, e.Namespace, e.Status, e.Revision,
		e.Age.Round(time.Second), e.Threshold,
		e.Namespace, e.SecretName, e.SecretName,
		e.Namespace, e.SecretName,
	)
}

// checkForStaleReleaseLock inspects the latest revision in the provided release
// history. If that revision is in a pending state and its Info.LastDeployed
// timestamp is older than threshold, it returns a *StaleReleaseLockError so the
// caller can fail fast with actionable diagnostics. Pending revisions whose
// Info.LastDeployed is within the threshold (i.e. a genuinely in-flight
// operation) are left alone and return nil.
func checkForStaleReleaseLock(logger logr.Logger, threshold time.Duration, versionsi []helmrelease.Releaser) error {
	versions, err := releaseListToV1List(versionsi)
	if err != nil {
		// Mirror isReleaseUninstalled: a conversion failure should not block the
		// deployment - just skip the stale-lock diagnostics.
		logger.Error(err, "cannot convert release list to v1 release list for stale-lock check")
		return nil
	}
	if len(versions) == 0 {
		return nil
	}

	latest := versions[len(versions)-1]
	if latest.Info == nil || !latest.Info.Status.IsPending() {
		return nil
	}

	age := releaseAge(latest)
	if age < threshold {
		logger.Info(
			"Latest release revision is in a pending state but within the staleness threshold; continuing.",
			"release", latest.Name,
			"namespace", latest.Namespace,
			"revision", latest.Version,
			"status", latest.Info.Status,
			"age", age.Round(time.Second).String(),
			"threshold", threshold.String(),
		)
		return nil
	}

	secretName := releaseSecretName(latest)
	logger.Info(
		"Detected stale Helm release lock; failing fast.",
		"release", latest.Name,
		"namespace", latest.Namespace,
		"revision", latest.Version,
		"status", latest.Info.Status,
		"age", age.Round(time.Second).String(),
		"threshold", threshold.String(),
		"secret", secretName,
	)
	return &StaleReleaseLockError{
		ReleaseName: latest.Name,
		Namespace:   latest.Namespace,
		Revision:    latest.Version,
		Status:      latest.Info.Status.String(),
		Age:         age,
		Threshold:   threshold,
		SecretName:  secretName,
	}
}

// releaseAge reports how long ago the given release revision was last deployed.
func releaseAge(release *helmreleasev1.Release) time.Duration {
	// A zero LastDeployed (e.g. a never-successfully-deployed pending-install)
	// is treated as age 0 so it is never flagged stale - failing fast on a
	// missing timestamp would break genuinely in-flight operations.
	if release == nil || release.Info == nil || release.Info.LastDeployed.IsZero() {
		return 0
	}
	return time.Since(release.Info.LastDeployed)
}

// releaseSecretName returns the name of the Kubernetes secret backing the given
// release revision, as used by Helm's default secret storage driver.
func releaseSecretName(release *helmreleasev1.Release) string {
	return fmt.Sprintf("sh.helm.release.v1.%s.v%d", release.Name, release.Version)
}
