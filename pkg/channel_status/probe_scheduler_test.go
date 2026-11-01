package channelstatus

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdleProbeUsesMostRecentCompletedRequest(t *testing.T) {
	for _, test := range []struct {
		name              string
		realAge, probeAge time.Duration
		want              bool
	}{
		{"first observation", 0, 0, true},
		{"recent real replaces an old probe", 20 * time.Second, 90 * time.Second, false},
		{"recent probe replaces an old real", 90 * time.Second, 20 * time.Second, false},
		{"both idle beyond maximum", 90 * time.Second, 80 * time.Second, true},
		{"exact maximum is due", 60 * time.Second, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			setupChannelStatusStore(t)
			target := testTarget()
			now := time.Unix(time.Now().Unix(), 0)
			for index, age := range []time.Duration{test.realAge, test.probeAge} {
				if age == 0 {
					continue
				}
				source := []string{"real", "probe"}[index]
				at := now.Add(-age)
				require.NoError(t, writeSample(context.Background(), target.Group, target.Model, source, Sample{
					ID: strings.Repeat([]string{"a", "b"}[index], 64), Status: "healthy", ObservedAt: at.Unix(), OrderMS: at.UnixMilli(), TTFTMs: 1, LatencyMs: 2,
				}))
			}
			due, err := targetNeedsProbe(context.Background(), target, now)
			require.NoError(t, err)
			assert.Equal(t, test.want, due)
		})
	}
}

func TestIdleProbeDoesNotAdvanceClockWhileRealRequestIsActive(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()
	now := time.Now()
	require.NoError(t, MarkRequestStarted(target.Group, target.Model, 7, "still-active", now.Add(-2*time.Minute)))
	due, err := targetNeedsProbe(context.Background(), target, now)
	require.NoError(t, err)
	assert.False(t, due)
	require.NoError(t, probeIfDue(context.Background(), target, now))
	assert.Zero(t, common.RDB.Exists(context.Background(), redisKey(target.Group, target.Model, "last-check")).Val())
	// Removing the only active request makes the original idle gap eligible
	// immediately. A skipped evaluation must not impose another whole interval.
	MarkRequestFinished(target.Group, target.Model, "still-active")
	require.Eventually(t, func() bool {
		return common.RDB.ZCard(context.Background(), redisKey(target.Group, target.Model, "real-active")).Val() == 0
	}, time.Second, time.Millisecond)
	due, err = targetNeedsProbe(context.Background(), target, now)
	require.NoError(t, err)
	assert.True(t, due)
}

func TestIdleProbeClearsExpiredOwnerAndKeepsLiveOwner(t *testing.T) {
	server := setupChannelStatusStore(t)
	target := testTarget()
	now := time.Now()
	server.SetTime(now)
	activeKey := redisKey(target.Group, target.Model, "real-active")
	leasesKey := activeRequestLeasesKey(target.Group, target.Model)
	staleOwner, liveOwner := "stale-process", "live-process"
	staleMember := activeRequestMember(staleOwner, "old-request")
	liveMember := activeRequestMember(liveOwner, "current-request")
	require.NoError(t, common.RDB.ZAdd(context.Background(), activeKey,
		&redis.Z{Score: float64(now.Add(-time.Hour).UnixMilli()), Member: staleMember},
		&redis.Z{Score: float64(now.UnixMilli()), Member: liveMember},
	).Err())
	require.NoError(t, common.RDB.ZAdd(context.Background(), leasesKey,
		&redis.Z{Score: float64(now.Add(-activeRequestLeaseTTL - time.Second).UnixMilli()), Member: staleMember},
		&redis.Z{Score: float64(now.UnixMilli()), Member: liveMember},
	).Err())

	due, err := targetNeedsProbe(context.Background(), target, now)
	require.NoError(t, err)
	assert.False(t, due, "another process with a live lease must continue suppressing a probe")
	assert.ErrorIs(t, common.RDB.ZScore(context.Background(), activeKey, staleMember).Err(), redis.Nil)
	require.NoError(t, common.RDB.ZScore(context.Background(), activeKey, liveMember).Err())
	assert.ErrorIs(t, common.RDB.ZScore(context.Background(), leasesKey, staleMember).Err(), redis.Nil)

	require.NoError(t, common.RDB.ZAdd(context.Background(), leasesKey,
		&redis.Z{Score: float64(now.Add(-activeRequestLeaseTTL - time.Second).UnixMilli()), Member: liveMember},
	).Err())
	due, err = targetNeedsProbe(context.Background(), target, now)
	require.NoError(t, err)
	assert.True(t, due, "a restarted process must eventually make its target eligible again")
	assert.Zero(t, common.RDB.ZCard(context.Background(), activeKey).Val())
	active, err := refreshActiveRequestLease(context.Background(), target.Group, target.Model, liveMember)
	require.NoError(t, err)
	assert.False(t, active, "a late heartbeat must not resurrect a reclaimed request")
	assert.Zero(t, common.RDB.ZCard(context.Background(), leasesKey).Val())
}

func TestIdleProbeRecoversOrphanMarkersAndPreservesLegacyGrace(t *testing.T) {
	for _, test := range []struct {
		name       string
		legacy     bool
		requestAge time.Duration
		leaseAge   time.Duration
		wantActive bool
	}{
		{name: "orphan without lease", requestAge: time.Minute},
		{name: "lease at expiry boundary", requestAge: 5 * time.Minute, leaseAge: activeRequestLeaseTTL},
		{name: "long request with live lease", requestAge: 25 * time.Hour, leaseAge: time.Second, wantActive: true},
		{name: "old node remains active", legacy: true, requestAge: 5 * time.Minute, wantActive: true},
		{name: "old node expires after grace", legacy: true, requestAge: sampleTTL + time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := setupChannelStatusStore(t)
			target := testTarget()
			now := time.Now()
			server.SetTime(now)
			member := "legacy-request"
			if !test.legacy {
				member = activeRequestMember("process", "request")
			}
			ctx := context.Background()
			activeKey := redisKey(target.Group, target.Model, "real-active")
			require.NoError(t, common.RDB.ZAdd(ctx, activeKey, &redis.Z{Score: float64(now.Add(-test.requestAge).UnixMilli()), Member: member}).Err())
			if test.leaseAge > 0 {
				require.NoError(t, common.RDB.ZAdd(ctx, activeRequestLeasesKey(target.Group, target.Model), &redis.Z{Score: float64(now.Add(-test.leaseAge).UnixMilli()), Member: member}).Err())
			}
			due, err := targetNeedsProbe(ctx, target, now)
			require.NoError(t, err)
			assert.Equal(t, !test.wantActive, due)
			assert.Equal(t, test.wantActive, common.RDB.ZCard(ctx, activeKey).Val() > 0)
		})
	}
}

func TestActiveRequestHeartbeatRenewsLease(t *testing.T) {
	server := setupChannelStatusStore(t)
	target := testTarget()
	now := time.Now()
	server.SetTime(now)
	ownerID := "heartbeat-process"
	member := activeRequestMember(ownerID, "long-request")
	activeKey := redisKey(target.Group, target.Model, "real-active")
	leasesKey := activeRequestLeasesKey(target.Group, target.Model)
	started := now.Add(-activeRequestLeaseTTL - time.Second)
	require.NoError(t, common.RDB.ZAdd(context.Background(), activeKey, &redis.Z{Score: float64(started.UnixMilli()), Member: member}).Err())
	require.NoError(t, common.RDB.ZAdd(context.Background(), leasesKey, &redis.Z{Score: float64(started.UnixMilli()), Member: member}).Err())

	active, err := refreshActiveRequestLease(context.Background(), target.Group, target.Model, member)
	require.NoError(t, err)
	assert.True(t, active)
	due, err := targetNeedsProbe(context.Background(), target, now)
	require.NoError(t, err)
	assert.False(t, due, "a renewed long-running request remains active")
	server.SetTime(now.Add(activeRequestLeaseTTL + time.Second))
	due, err = targetNeedsProbe(context.Background(), target, now.Add(activeRequestLeaseTTL+time.Second))
	require.NoError(t, err)
	assert.True(t, due, "a process that stops renewing must stop suppressing probes")
}

func TestIdleProbeRetainsCooldownWhenCompletedSampleWriteWasUnavailable(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()
	now := time.Now()
	require.NoError(t, setLastCheck(context.Background(), target.Group, target.Model, now.Add(-10*time.Second)))
	due, err := targetNeedsProbe(context.Background(), target, now)
	require.NoError(t, err)
	assert.False(t, due)
}

func TestIndependentProbeDispatchDoesNotWaitForSlowTarget(t *testing.T) {
	targets := []Target{{Group: "slow", Model: "model"}, {Group: "fast", Model: "model"}}
	var dispatcher probeDispatcher
	started, release, finished := make(chan string, 4), make(chan struct{}), make(chan string, 4)
	run := func(ctx context.Context, target Target) {
		started <- target.Group
		if target.Group == "slow" {
			<-release
		}
		finished <- target.Group
	}
	assert.Equal(t, 2, dispatcher.dispatch(targets, run))
	assert.ElementsMatch(t, []string{"slow", "fast"}, []string{<-started, <-started})
	assert.Equal(t, "fast", <-finished)
	// The slow target remains in flight, but another evaluation of the fast
	// target can start without waiting for it or queuing behind its network call.
	require.Eventually(t, func() bool { _, busy := dispatcher.active.Load(targetKey("fast", "model")); return !busy }, time.Second, time.Millisecond)
	assert.Equal(t, 1, dispatcher.dispatch(targets, run))
	assert.Equal(t, "fast", <-started)
	assert.Equal(t, "fast", <-finished)
	close(release)
	assert.Equal(t, "slow", <-finished)
	require.Eventually(t, func() bool { _, busy := dispatcher.active.Load(targetKey("slow", "model")); return !busy }, time.Second, time.Millisecond)
}
