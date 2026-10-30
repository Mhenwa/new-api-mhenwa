package channelstatus

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/go-redis/redis/v8"
)

const probeEvaluationInterval = 5 * time.Second

var probeSchedulerOnce sync.Once

type probeDispatcher struct {
	active sync.Map
}

// StartProbeScheduler evaluates each configured target independently. A slow
// model never postpones another target's idle deadline. Only masters start it;
// the existing per-target Redis lease deduplicates multiple masters.
func StartProbeScheduler() {
	if !common.IsMasterNode {
		return
	}
	probeSchedulerOnce.Do(func() {
		gopool.Go(func() {
			var dispatcher probeDispatcher
			ticker := time.NewTicker(probeEvaluationInterval)
			defer ticker.Stop()
			for {
				targets, err := LoadTargets()
				if err != nil {
					common.SysLog(common.LogText("channel status idle scheduler configuration failed: %v", err))
				} else if RedisAvailable() {
					dispatcher.dispatch(targets, func(ctx context.Context, target Target) {
						if err := probeIfDue(ctx, target, time.Now()); err != nil {
							common.SysLog(common.LogText("channel status idle probe failed for %s/%s: %v", target.Group, target.Model, err))
						}
					})
				}
				<-ticker.C
			}
		})
	})
}

func (dispatcher *probeDispatcher) dispatch(targets []Target, run func(context.Context, Target)) int {
	started := 0
	for _, target := range targets {
		key := targetKey(target.Group, target.Model)
		if _, busy := dispatcher.active.LoadOrStore(key, struct{}{}); busy {
			continue
		}
		started++
		gopool.Go(func() {
			defer dispatcher.active.Delete(key)
			// Workers outlive a scheduling pass but always have a finite deadline.
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(max(target.TimeoutSeconds, 5)+20)*time.Second)
			defer cancel()
			run(ctx, target)
		})
	}
	return started
}

func targetNeedsProbe(parent context.Context, target Target, now time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	pipe := common.RDB.Pipeline()
	real := pipe.Get(ctx, redisKey(target.Group, target.Model, "real"))
	probe := pipe.Get(ctx, redisKey(target.Group, target.Model, "probe"))
	lastCheck := pipe.Get(ctx, redisKey(target.Group, target.Model, "last-check"))
	active := pipe.ZCard(ctx, redisKey(target.Group, target.Model, "real-active"))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return false, err
	}
	if active.Err() != nil {
		return false, active.Err()
	}
	if active.Val() > 0 {
		return false, nil
	}
	latest := int64(0)
	for _, result := range []*redis.StringCmd{real, probe} {
		if errors.Is(result.Err(), redis.Nil) {
			continue
		}
		if result.Err() != nil {
			return false, result.Err()
		}
		var sample Sample
		if err := common.UnmarshalJsonStr(result.Val(), &sample); err != nil {
			return false, errors.New("invalid channel status sample")
		}
		latest = max(latest, sample.ObservedAt*1000, sample.OrderMS)
	}
	// last-check is written only after an actual fallback attempt. It also
	// prevents a hot retry loop if the attempt's sample could not be persisted.
	if !errors.Is(lastCheck.Err(), redis.Nil) {
		value, err := lastCheck.Int64()
		if err != nil {
			return false, err
		}
		latest = max(latest, value*1000)
	}
	return latest == 0 || now.UnixMilli()-latest >= int64(target.IntervalSeconds)*1000, nil
}
