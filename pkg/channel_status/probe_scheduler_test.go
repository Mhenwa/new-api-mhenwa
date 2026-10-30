package channelstatus

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
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
	require.NoError(t, common.RDB.ZRem(context.Background(), redisKey(target.Group, target.Model, "real-active"), "still-active").Err())
	due, err = targetNeedsProbe(context.Background(), target, now)
	require.NoError(t, err)
	assert.True(t, due)
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
