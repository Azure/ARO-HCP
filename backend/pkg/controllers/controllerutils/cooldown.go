package controllerutils

import (
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/lru"
)

type CooldownChecker interface {
	// returns true if we can synchronize this particular key
	CanSync(key any) bool
}

type TimeBasedCooldownChecker struct {
	clock            utilsclock.PassiveClock
	cooldownDuration time.Duration
	nextExecTime     *lru.Cache
}

func NewTimeBasedCooldownChecker(cooldownDuration time.Duration) TimeBasedCooldownChecker {
	return TimeBasedCooldownChecker{
		clock:            utilsclock.RealClock{},
		cooldownDuration: cooldownDuration,
		nextExecTime:     lru.New(1000000), // only holding item keys, so they are small

	}
}

func (c *TimeBasedCooldownChecker) CanSync(key any) bool {
	now := c.clock.Now()
	defer c.nextExecTime.Add(key, now.Add(c.cooldownDuration))

	nextExecTime, ok := c.nextExecTime.Get(key)
	if !ok {
		return true
	}
	if nextExecTime.(time.Time).Before(now) {
		return false
	}

	return true
}

type ActiveOperationBasedChecker struct {
	clock                 utilsclock.PassiveClock
	activeOperationLister listers.ActiveOperationLister

	activeOperationTimer   TimeBasedCooldownChecker
	inactiveOperationTimer TimeBasedCooldownChecker
	cooldownDuration       time.Duration
	nextExecTime           *lru.Cache
}

func NewCooldown(activeOperationLister listers.ActiveOperationLister, activeOperationCooldown, inactiveOperationCooldown time.Duration, ) ActiveOperationBasedChecker {
	return ActiveOperationBasedChecker{
		clock:            utilsclock.RealClock{},
		cooldownDuration: cooldownDuration,
		nextExecTime:     lru.New(1000000), // only holding item keys, so they are small

	}
}

func (c *TimeBasedCooldownChecker) CanSync(key any) bool {
	now := c.clock.Now()
	defer c.nextExecTime.Add(key, now.Add(c.cooldownDuration))

	nextExecTime, ok := c.nextExecTime.Get(key)
	if !ok {
		return true
	}
	if nextExecTime.(time.Time).Before(now) {
		return false
	}

	return true
}
