package channelstatus

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupChannelStatusStore(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	server := miniredis.RunT(t)
	previousRedisEnabled, previousRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
	})
	t.Setenv(TargetsEnv, `[{"group":"standard","model":"mhenwa-chat","probe_token_id":42,"interval_seconds":60,"timeout_seconds":5}]`)
	return server
}

func testTarget() Target {
	targets, err := LoadTargets()
	if err != nil || len(targets) != 1 {
		panic("test target configuration is invalid")
	}
	return targets[0]
}

func TestRequestMarkerLifecycleAndProbeCancellation(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()
	started := time.UnixMilli(1_700_000_000_000)
	lockKey := redisKey(target.Group, target.Model, "probe-lock")
	cancelKey := redisKey(target.Group, target.Model, "probe-cancel")
	require.NoError(t, common.RDB.Set(context.Background(), lockKey, "probe-owner", time.Minute).Err())

	require.NoError(t, MarkRequestStarted(target.Group, target.Model, 7, "request-1", started))
	active, err := common.RDB.ZScore(context.Background(), redisKey(target.Group, target.Model, "real-active"), "request-1").Result()
	require.NoError(t, err)
	assert.Equal(t, float64(started.UnixMilli()), active)
	cancelValue, err := common.RDB.Get(context.Background(), cancelKey).Result()
	require.NoError(t, err)
	assert.Equal(t, "probe-owner", cancelValue)

	MarkRequestFinished(target.Group, target.Model, "request-1")
	assert.Equal(t, int64(0), common.RDB.ZCard(context.Background(), redisKey(target.Group, target.Model, "real-active")).Val())
}

func TestProbeTokenIsNeverMarkedAsRealRequest(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()

	require.NoError(t, MarkRequestStarted(target.Group, target.Model, target.ProbeTokenID, "probe-request", time.Now()))
	assert.Equal(t, int64(0), common.RDB.ZCard(context.Background(), redisKey(target.Group, target.Model, "real-active")).Val())
}

func TestSamplesKeepNewestObservationAndPreferActiveRealRequest(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()
	base := time.Now().UnixMilli()
	old := Sample{Status: "error", ObservedAt: (base - 1_000) / 1_000, OrderMS: base - 1_000, LatencyMs: 900}
	newer := Sample{Status: "healthy", ObservedAt: base / 1_000, OrderMS: base, LatencyMs: 120}
	require.NoError(t, RecordProbeSample(target.Group, target.Model, old))
	require.NoError(t, RecordProbeSample(target.Group, target.Model, newer))
	require.NoError(t, RecordProbeSample(target.Group, target.Model, old))

	status, err := GetTargetStatus(context.Background(), target)
	require.NoError(t, err)
	assert.Equal(t, "healthy", status.Status)
	assert.Equal(t, int64(120), status.LatencyMs)

	require.NoError(t, MarkRequestStarted(target.Group, target.Model, 7, "request-2", time.UnixMilli(base+1)))
	status, err = GetTargetStatus(context.Background(), target)
	require.NoError(t, err)
	assert.Equal(t, "probing", status.Status)
	assert.Equal(t, "real", status.Source)
	MarkRequestFinished(target.Group, target.Model, "request-2")
}

func TestExpiredAndMalformedSamplesAreNotReportedAsHealthy(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()
	expired := time.Now().Add(-3 * time.Duration(target.IntervalSeconds) * time.Second)
	require.NoError(t, RecordProbeSample(target.Group, target.Model, Sample{
		Status: "healthy", ObservedAt: expired.Unix(), OrderMS: expired.UnixMilli(),
	}))

	status, err := GetTargetStatus(context.Background(), target)
	require.NoError(t, err)
	assert.Equal(t, "no_data", status.Status)

	require.NoError(t, common.RDB.Set(context.Background(), redisKey(target.Group, target.Model, "real"), "not-json", time.Hour).Err())
	_, err = GetTargetStatus(context.Background(), target)
	require.Error(t, err)
}
