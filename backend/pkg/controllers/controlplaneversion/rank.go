package main

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/klog/v2"
)

// rankRelease allows hosting maintainers to rank a target release
// based on their own criteria.  Releases that the function ranks are
// preferred over releases where ranking errors.  If error state ties,
// releases with a higher rank are preferred over releases with a
// lower rank.  If both error state and rank tie, releases with a larger
// Semantic Version are preferred over releases with a lower Semantic
// Version.
type rankRelease func(release configv1.Release) (float32, error)

// rankedRelease holds a release and the hosting-maintainer's rank of that release.
type rankedRelease struct {
	rank    float32
	error   error
	release configv1.Release
}

// String represents the rankedRelease as a string.
func (rr rankedRelease) String() string {
	if rr.error != nil {
		return fmt.Sprintf("%v (rank %g, error %v)", rr.release.Version, rr.rank, rr.error)
	}
	return fmt.Sprintf("%v (rank %g)", rr.release.Version, rr.rank)
}

// rankedSelection runs the rankRelease function on each of the
// releases, and returns the release that the ranking function prefers,
// even if that is not the largest semantic version in the release set.
func rankedSelection(targets []configv1.Release, rankRelease rankRelease) (*semver.Version, error) {
	initialTargets := len(targets)
	var err error
	for i := len(targets) - 1; i >= 0; i-- {
		if _, err = semver.Parse(targets[i].Version); err != nil {
			err = fmt.Errorf("failed to parse SemVer %q: %w", targets[i].Version, err)
			targets = append(targets[:i], targets[i+1:]...)
		}
	}

	if len(targets) == 0 {
		if initialTargets > 0 {
			return nil, fmt.Errorf("rankedSelection requires a non-empty target set, and while this call had %d targets, none of them had valid SemVer versions: %w", initialTargets, err)
		}
		return nil, errors.New("rankedSelection requires a non-empty target set.  Callers with zero targets should not call us.")
	}

	slices.SortFunc(targets, func(a, b configv1.Release) int {
		vA := semver.MustParse(a.Version)
		vB := semver.MustParse(b.Version)
		return -vA.Compare(vB)
	})

	if rankRelease == nil {
		v, err := semver.Parse(targets[0].Version)
		return &v, err
	}

	ranks := make([]rankedRelease, 0, len(targets))
	for _, target := range targets {
		rank, err := rankRelease(target)
		ranks = append(ranks, rankedRelease{
			rank:    rank,
			error:   err,
			release: target,
		})
	}

	slices.SortFunc(ranks, func(a, b rankedRelease) int {
		if a.error == nil && b.error != nil {
			return -1
		}
		if a.error != nil && b.error == nil {
			return 1
		}
		if a.rank != b.rank {
			return -cmp.Compare(a.rank, b.rank)
		}
		vA := semver.MustParse(a.release.Version)
		vB := semver.MustParse(b.release.Version)
		return -vA.Compare(vB)
	})
	if ranks[0].error != nil {
		return nil, fmt.Errorf("failed to rank all %d releases, including %s", len(ranks), ranks[0])
	}
	if ranks[0].release.Version != targets[0].Version {
		rankInversions := make([]rankedRelease, 0, len(targets))
		for _, target := range targets {
			if target.Version == ranks[0].release.Version {
				break
			}
			for _, ranked := range ranks {
				if ranked.release.Version == target.Version {
					rankInversions = append(rankInversions, ranked)
					break
				}
			}
		}
		klog.V(2).Infof("recommending %s after the caller had concerns with greater releases like %s (%v)", ranks[0], targets[0].Version, rankInversions)
	}
	v, err := semver.Parse(ranks[0].release.Version)
	return &v, err
}
