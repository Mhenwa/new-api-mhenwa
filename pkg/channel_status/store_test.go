package channelstatus

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupChannelStatusStore(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	server := miniredis.RunT(t)
	previousRedisEnabled, previousRDB := common.RedisEnabled, common.RDB
	previousOptions := common.OptionMap
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		require.NoError(t, common.RDB.Close())
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	t.Setenv(TargetsEnv, `[{"group":"standard","model":"mhenwa-chat","probe_token_id":42,"interval_seconds":60,"timeout_seconds":5}]`)
	return server
}

func TestDatabaseConfigurationRecordsRealResultAndSuppressesProbe(t *testing.T) {
	setupChannelStatusStore(t)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[TargetsOption] = `[{"group":"default","model":"db-model","probe_token_id":42,"interval_seconds":60,"timeout_seconds":5}]`
	common.OptionMapRWMutex.Unlock()
	t.Setenv(TargetsEnv, "")
	target := testTarget()
	started := time.Now().Add(-time.Second)
	require.NoError(t, MarkRequestStarted(target.Group, target.Model, 7, "database-real-request", started))
	RecordRelayResult(&relaycommon.RelayInfo{UsingGroup: target.Group, OriginModelName: target.Model, TokenId: 7, StartTime: started}, nil)
	require.Eventually(t, func() bool {
		sample, err := readSample(context.Background(), target.Group, target.Model, "real")
		return err == nil && sample != nil && sample.Status == "degraded"
	}, time.Second, time.Millisecond)
	MarkRequestFinished(target.Group, target.Model, "database-real-request")
	require.Eventually(t, func() bool {
		return common.RDB.ZCard(context.Background(), redisKey(target.Group, target.Model, "real-active")).Val() == 0
	}, time.Second, time.Millisecond)
	// A real request in the current interval must prevent a synthetic request.
	// No token database or upstream is configured, so an attempted probe fails.
	require.NoError(t, probeIfDue(context.Background(), target, time.Now()))
	assert.False(t, common.RDB.Exists(context.Background(), redisKey(target.Group, target.Model, "probe")).Val() > 0)
	assert.Empty(t, common.RDB.Get(context.Background(), redisKey(target.Group, target.Model, "last-check")).Val(), "skipping for real traffic must not postpone its idle deadline")

	common.OptionMapRWMutex.Lock()
	common.OptionMap[TargetsOption] = "[]"
	common.OptionMapRWMutex.Unlock()
	t.Setenv(TargetsEnv, `[{"group":"default","model":"db-model","probe_token_id":42}]`)
	assert.False(t, IsConfigured())
	require.NoError(t, MarkRequestStarted(target.Group, target.Model, 7, "disabled-request", time.Now()))
	assert.Zero(t, common.RDB.ZCard(context.Background(), redisKey(target.Group, target.Model, "real-active")).Val())
	RecordRelayResult(&relaycommon.RelayInfo{UsingGroup: target.Group, OriginModelName: target.Model, TokenId: 7, StartTime: started}, nil)
	require.NoError(t, ProbeAll(context.Background()))
}

func TestProbeStreamReadsUsageAfterFinishReason(t *testing.T) {
	for _, test := range []struct {
		name      string
		stream    string
		tokens    int64
		wantError string
	}{
		{"trailing usage", "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"completion_tokens\":8}}\n\ndata: [DONE]\n\n", 8, ""},
		{"finish reason and EOF", "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}],\"usage\":{\"completion_tokens\":2}}\n", 2, ""},
		{"reasoning before text", "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}],\"usage\":{\"completion_tokens\":3}}\n", 3, ""},
		{"missing terminal", "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n", 0, "without a valid terminal"},
		{"malformed trailing chunk", "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\ndata: invalid\ndata: [DONE]\n", 0, "without a valid terminal"},
		{"terminal without output", "data: [DONE]\n", 0, "without text output"},
	} {
		t.Run(test.name, func(t *testing.T) {
			measurement, err := readProbeStream(strings.NewReader(test.stream))
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				return
			}
			require.NoError(t, err)
			assert.False(t, measurement.firstContentAt.IsZero())
			assert.Equal(t, test.tokens, measurement.completionTokens)
			firstOutputAt, _, _ := measurement.outputWindow.Snapshot()
			assert.False(t, firstOutputAt.IsZero())
			assert.False(t, firstOutputAt.After(measurement.firstContentAt), "reasoning and text use the same first-output clock")
			assert.Zero(t, observedOutputSpeed(measurement.outputWindow, measurement.completionTokens), "one buffered content frame has no measurable output speed")
		})
	}
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
	require.Eventually(t, func() bool {
		return common.RDB.ZCard(context.Background(), redisKey(target.Group, target.Model, "real-active")).Val() == 0
	}, time.Second, time.Millisecond)
}

func TestProbeTokenIsNeverMarkedAsRealRequest(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()

	require.NoError(t, MarkRequestStarted(target.Group, target.Model, target.ProbeTokenID, "probe-request", time.Now()))
	assert.Equal(t, int64(0), common.RDB.ZCard(context.Background(), redisKey(target.Group, target.Model, "real-active")).Val())
}

func TestRequestCleanupAfterTargetRemovalDoesNotLeaveActiveMarker(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()
	require.NoError(t, MarkRequestStarted(target.Group, target.Model, 7, "removed-target-request", time.Now()))
	common.OptionMapRWMutex.Lock()
	common.OptionMap[TargetsOption] = "[]"
	common.OptionMapRWMutex.Unlock()
	MarkRequestFinished(target.Group, target.Model, "removed-target-request")
	require.Eventually(t, func() bool {
		return common.RDB.ZCard(context.Background(), redisKey(target.Group, target.Model, "real-active")).Val() == 0
	}, time.Second, time.Millisecond)
	common.OptionMapRWMutex.Lock()
	delete(common.OptionMap, TargetsOption)
	common.OptionMapRWMutex.Unlock()
	status, err := GetTargetStatus(context.Background(), target)
	require.NoError(t, err)
	assert.Equal(t, "no_data", status.Status, "re-adding a target must not revive its finished request")
}

func TestSamplesKeepNewestCompletedObservationDuringActiveRequests(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()
	base := time.Now().UnixMilli()
	require.NoError(t, MarkRequestStarted(target.Group, target.Model, 7, "first-unfinished-request", time.UnixMilli(base)))
	status, err := GetTargetStatus(context.Background(), target)
	require.NoError(t, err)
	assert.Equal(t, "no_data", status.Status, "an unfinished request is not a completed observation")
	MarkRequestFinished(target.Group, target.Model, "first-unfinished-request")
	require.Eventually(t, func() bool {
		return common.RDB.ZCard(context.Background(), redisKey(target.Group, target.Model, "real-active")).Val() == 0
	}, time.Second, time.Millisecond)
	old := Sample{Status: "error", ObservedAt: (base - 1_000) / 1_000, OrderMS: base - 1_000, LatencyMs: 900}
	newer := Sample{Status: "healthy", ObservedAt: base / 1_000, OrderMS: base, TTFTMs: 100, LatencyMs: 120}
	require.NoError(t, RecordProbeSample(target.Group, target.Model, old))
	require.NoError(t, RecordProbeSample(target.Group, target.Model, newer))
	require.NoError(t, RecordProbeSample(target.Group, target.Model, old))

	status, err = GetTargetStatus(context.Background(), target)
	require.NoError(t, err)
	assert.Equal(t, "healthy", status.Status)
	assert.Equal(t, int64(120), status.LatencyMs)

	require.NoError(t, MarkRequestStarted(target.Group, target.Model, 7, "request-2", time.UnixMilli(base+1)))
	status, err = GetTargetStatus(context.Background(), target)
	require.NoError(t, err)
	assert.Equal(t, "healthy", status.Status, "in-flight requests must not hide the last completed observation")
	assert.Equal(t, "probe", status.Source)
	assert.Equal(t, newer.ObservedAt, status.ObservedAt)
	MarkRequestFinished(target.Group, target.Model, "request-2")
	require.Eventually(t, func() bool {
		return common.RDB.ZCard(context.Background(), redisKey(target.Group, target.Model, "real-active")).Val() == 0
	}, time.Second, time.Millisecond)
}

func TestCompletedSamplesRemainVisibleUntilRetentionExpires(t *testing.T) {
	server := setupChannelStatusStore(t)
	target := testTarget()
	expired := time.Now().Add(-3 * time.Duration(target.IntervalSeconds) * time.Second)
	require.NoError(t, RecordProbeSample(target.Group, target.Model, Sample{
		Status: "healthy", ObservedAt: expired.Unix(), OrderMS: expired.UnixMilli(), TTFTMs: 100, LatencyMs: 200,
	}))

	status, err := GetTargetStatus(context.Background(), target)
	require.NoError(t, err)
	assert.Equal(t, "healthy", status.Status, "the newest completed sample survives more than two probe intervals")
	assert.Equal(t, expired.Unix(), status.ObservedAt)
	server.FastForward(sampleTTL + time.Second)
	status, err = GetTargetStatus(context.Background(), target)
	require.NoError(t, err)
	assert.Equal(t, "no_data", status.Status)

	require.NoError(t, common.RDB.Set(context.Background(), redisKey(target.Group, target.Model, "real"), "not-json", time.Hour).Err())
	_, err = GetTargetStatus(context.Background(), target)
	require.Error(t, err)
}

func TestOutputSpeedRequiresAResolvedContentWindow(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	type timedFrame struct {
		payload string
		at      time.Duration
	}
	for _, test := range []struct {
		name   string
		frames []timedFrame
		want   float64
	}{
		{
			name: "one text chunk followed by a slow usage tail stays unknown",
			frames: []timedFrame{
				{`{"choices":[{"delta":{"content":"OK"}}]}`, time.Second},
				{`{"choices":[],"usage":{"completion_tokens":8}}`, 5 * time.Second},
				{`[DONE]`, 6 * time.Second},
			},
		},
		{
			name: "buffered content frames below the measurement window stay unknown",
			frames: []timedFrame{
				{`{"choices":[{"delta":{"content":"O"}}]}`, time.Second},
				{`{"choices":[{"delta":{"content":"K"}}]}`, time.Second + 249*time.Millisecond},
				{`{"choices":[{"finish_reason":"stop"}]}`, 10 * time.Second},
			},
		},
		{
			name: "multiple content frames exclude role usage and terminal latency",
			frames: []timedFrame{
				{`{"choices":[{"delta":{"role":"assistant"}}]}`, 0},
				{`{"choices":[{"delta":{"reasoning_content":"think"}}]}`, time.Second},
				{`{"choices":[{"delta":{"content":"answer"}}]}`, 2 * time.Second},
				{`{"choices":[{"finish_reason":"stop"}]}`, 6 * time.Second},
				{`{"choices":[],"usage":{"completion_tokens":8}}`, 7 * time.Second},
				{`[DONE]`, 8 * time.Second},
			},
			want: 8,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var window streamOutputWindow
			for _, frame := range test.frames {
				payload := []byte(frame.payload)
				before := string(payload)
				window.Observe(payload, base.Add(frame.at))
				assert.Equal(t, before, string(payload), "observation must not rewrite a frame")
			}
			assert.Equal(t, test.want, observedOutputSpeed(&window, 8))
		})
	}
}

func TestRecognizedContentProtocolsHaveAnEstimatedSpeed(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	for _, test := range []struct {
		name  string
		first string
		last  string
	}{
		{"responses", `{"type":"response.reasoning_text.delta","delta":"think"}`, `{"type":"response.output_text.delta","delta":"answer"}`},
		{"anthropic", `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"think"}}`, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"answer"}}`},
		{"gemini", `{"candidates":[{"content":{"parts":[{"thought":true,"text":"think"}]}}]}`, `{"candidates":[{"content":{"parts":[{"text":"answer"}]}}]}`},
		{"tool arguments", `{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{"}}]}}]}`, `{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"}"}}]}}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var window streamOutputWindow
			window.Observe([]byte(test.first), base)
			window.Observe([]byte(test.last), base.Add(minOutputWindow))
			assert.Equal(t, float64(32), observedOutputSpeed(&window, 8))
			assert.Zero(t, observedOutputSpeed(&window, 0), "missing usage must not be inferred from text")
		})
	}
}

func TestMonitoredScannerObservationPreservesStreamPayloadAndCompletion(t *testing.T) {
	setupChannelStatusStore(t)
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	target := testTarget()
	info := &relaycommon.RelayInfo{
		UsingGroup: target.Group, OriginModelName: target.Model, TokenId: 7, IsStream: true,
		StartTime: time.Now(), ChannelMeta: &relaycommon.ChannelMeta{},
	}
	AttachRelayObserver(info)
	require.NotNil(t, info.ChannelStatusOutput)
	payloads := []string{`{"choices":[{"delta":{"content":"O"}}]}`, `{"choices":[{"delta":{"content":"K"}}]}`, `{"choices":[],"usage":{"completion_tokens":8}}`}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	response := &http.Response{Body: io.NopCloser(strings.NewReader("data: " + strings.Join(payloads, "\n\ndata: ") + "\n\ndata: [DONE]\n\n"))}
	var received []string
	helper.StreamScannerHandler(c, response, info, func(data string, result *helper.StreamResult) {
		received = append(received, data)
	})
	assert.Equal(t, payloads, received)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	assert.NoError(t, c.Request.Context().Err())
	_, _, frames := info.ChannelStatusOutput.Snapshot()
	assert.Equal(t, 2, frames, "usage must not be recorded as a content frame")
	AttachRelayObserver(info)
	_, _, frames = info.ChannelStatusOutput.Snapshot()
	assert.Zero(t, frames, "retry attempts must not reuse an earlier content window")
	info.TokenId = target.ProbeTokenID
	AttachRelayObserver(info)
	assert.Nil(t, info.ChannelStatusOutput, "probe calls own their separate loopback observation")
	info.TokenId = 7
	info.OriginModelName = "unmonitored"
	AttachRelayObserver(info)
	assert.Nil(t, info.ChannelStatusOutput, "unmonitored traffic must not install an observer")
}

func TestRealResultMeasuresContentAndHidesFailureSpeed(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "successful stream", true: "failed stream"}[failed], func(t *testing.T) {
			setupChannelStatusStore(t)
			target := testTarget()
			base := time.Now().Add(-10 * time.Second)
			info := &relaycommon.RelayInfo{
				UsingGroup: target.Group, OriginModelName: target.Model, TokenId: 7,
				IsStream: true, StartTime: base, FirstResponseTime: base.Add(10 * time.Millisecond),
				PerformanceOutputTokens: 20,
			}
			AttachRelayObserver(info)
			info.ChannelStatusOutput.Observe([]byte(`{"choices":[{"delta":{"content":"hello"}}]}`), base.Add(time.Second))
			info.ChannelStatusOutput.Observe([]byte(`{"choices":[{"delta":{"content":"world"}}]}`), base.Add(3*time.Second))
			if failed {
				info.StreamStatus = relaycommon.NewStreamStatus()
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, nil)
			}
			RecordRelayResult(info, nil)
			var sample *Sample
			require.Eventually(t, func() bool {
				var err error
				sample, err = readSample(context.Background(), target.Group, target.Model, "real")
				return err == nil && sample != nil
			}, time.Second, time.Millisecond)
			assert.Equal(t, int64(1000), sample.TTFTMs, "role/transport first response is not first content")
			assert.GreaterOrEqual(t, sample.LatencyMs, int64(10000), "total time still includes the full request")
			require.NotNil(t, sample.OutputTokens)
			assert.Equal(t, int64(20), *sample.OutputTokens)
			assert.Equal(t, int64(20), info.PerformanceOutputTokens, "status sampling must not change settlement output usage")
			if failed {
				assert.Equal(t, "error", sample.Status)
				assert.Zero(t, sample.TPS)
			} else {
				assert.Equal(t, "healthy", sample.Status)
				assert.Equal(t, float64(10), sample.TPS, "post-content terminal and settlement time is not generation")
			}
		})
	}
}

func TestStoredLegacySpeedIsHiddenWithoutLosingAvailability(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()
	for _, version := range []int{0, outputRateVersion} {
		sample := Sample{Status: "healthy", ObservedAt: time.Now().Unix(), TTFTMs: 100, LatencyMs: 200, TPS: 223994, OutputRateVersion: version}
		require.NoError(t, RecordProbeSample(target.Group, target.Model, sample))
		status, err := GetTargetStatus(context.Background(), target)
		require.NoError(t, err)
		assert.Equal(t, "healthy", status.Status)
		if version == 0 {
			assert.Zero(t, status.TPS)
		} else {
			assert.Equal(t, sample.TPS, status.TPS, "a measured speed must not be arbitrarily capped")
		}
	}
}

func TestCompletedStatusMatchesLogDangerThresholds(t *testing.T) {
	for _, test := range []struct {
		name    string
		ttft    int64
		latency int64
		tokens  *int64
		failed  bool
		want    string
	}{
		{name: "log green and yellow both remain healthy", ttft: 9999, latency: 29999, tokens: new(int64(99)), want: "healthy"},
		{name: "red first token is degraded", ttft: 10000, latency: 20000, tokens: new(int64(99)), want: "degraded"},
		{name: "short output red total latency is degraded", ttft: 1000, latency: 30000, tokens: new(int64(99)), want: "degraded"},
		{name: "long output uses throughput instead of total duration", ttft: 1000, latency: 60000, tokens: new(int64(900)), want: "healthy"},
		{name: "red long output throughput is degraded", ttft: 1000, latency: 60000, tokens: new(int64(899)), want: "degraded"},
		{name: "hundred output tokens switches to throughput", ttft: 1000, latency: 10000, tokens: new(int64(100)), want: "degraded"},
		{name: "legacy unknown output falls back to short response", ttft: 1000, latency: 29999, want: "healthy"},
		{name: "missing first token remains completed degraded", latency: 1000, want: "degraded"},
		{name: "missing total latency remains completed degraded", ttft: 1000, want: "degraded"},
		{name: "real error has priority over good timing", ttft: 1000, latency: 1000, failed: true, want: "error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			setupChannelStatusStore(t)
			target := testTarget()
			base := time.Now()
			status := "healthy"
			if test.failed {
				status = "error"
			}
			sample := Sample{Status: status, ObservedAt: base.Unix(), TTFTMs: test.ttft, LatencyMs: test.latency, OutputTokens: test.tokens}
			require.NoError(t, RecordProbeSample(target.Group, target.Model, sample))
			latest, err := GetTargetStatus(context.Background(), target)
			require.NoError(t, err)
			assert.Equal(t, test.want, latest.Status)
			assert.Equal(t, test.tokens, latest.OutputTokens)
			history, err := GetTargetHistory(context.Background(), target, 3600, base)
			require.NoError(t, err)
			assert.Equal(t, test.want, history.Buckets[59].Status)
			assert.Equal(t, int64(1), history.SampleCount)
			if test.failed {
				assert.Zero(t, history.SuccessCount)
				assert.Zero(t, history.DegradedCount)
				assert.Equal(t, int64(1), history.ErrorCount)
			} else {
				assert.Equal(t, int64(1), history.SuccessCount, "degraded success still counts toward observed success rate")
				require.NotNil(t, history.SuccessRate)
				assert.Equal(t, float64(100), *history.SuccessRate)
				if test.want == "degraded" {
					assert.Equal(t, int64(1), history.DegradedCount)
				}
			}
		})
	}
}

func TestHistoryAggregatesNativeObservationsAndPreservesEmptyPeriods(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()
	base := time.Now().Truncate(time.Minute)
	legacy := Sample{Status: "healthy", ObservedAt: base.Add(-4 * time.Minute).Unix(), Source: "probe", TTFTMs: 999}
	encoded, err := common.Marshal(legacy)
	require.NoError(t, err)
	require.NoError(t, common.RDB.Set(context.Background(), redisKey(target.Group, target.Model, "probe"), encoded, time.Hour).Err())
	history, err := GetTargetHistory(context.Background(), target, 3600, base)
	require.NoError(t, err)
	assert.Zero(t, history.SampleCount, "latest snapshots must not be backfilled as historical observations")
	assert.Empty(t, history.Observations, "latest snapshots cannot reconstruct individual requests")
	assert.Empty(t, history.RealSamples)
	assert.Nil(t, history.SuccessRate)
	for _, bucket := range history.Buckets {
		assert.Equal(t, "no_data", bucket.Status)
		assert.Nil(t, bucket.TTFTMs)
	}

	require.NoError(t, RecordProbeSample(target.Group, target.Model, Sample{Status: "healthy", ObservedAt: base.Add(-time.Minute).Unix(), TTFTMs: 100, LatencyMs: 200}))
	require.NoError(t, writeSample(context.Background(), target.Group, target.Model, "real", Sample{Status: "error", ObservedAt: base.Add(-time.Minute).Unix(), TTFTMs: 300}))
	require.NoError(t, writeSample(context.Background(), target.Group, target.Model, "real", Sample{Status: "healthy", ObservedAt: base.Add(-2 * time.Minute).Unix()}))
	history, err = GetTargetHistory(context.Background(), target, 3600, base)
	require.NoError(t, err)
	require.Len(t, history.Buckets, 60)
	assert.Equal(t, base.Add(time.Minute).Unix(), history.EndAt)
	assert.Equal(t, history.EndAt-3600, history.StartAt)
	assert.Equal(t, int64(3), history.SampleCount)
	assert.Equal(t, 2, history.ObservedBucketCount)
	require.NotNil(t, history.SuccessRate)
	assert.InDelta(t, 200.0/3, *history.SuccessRate, 0.001)
	mixed := history.Buckets[58]
	assert.Equal(t, "mixed", mixed.Status)
	assert.Equal(t, int64(1), mixed.RealCount)
	assert.Equal(t, int64(1), mixed.ProbeCount)
	require.NotNil(t, mixed.TTFTMs)
	assert.Equal(t, float64(200), *mixed.TTFTMs)
	require.NotNil(t, mixed.RealTTFTMs)
	assert.Equal(t, float64(300), *mixed.RealTTFTMs)
	assert.Equal(t, int64(2), mixed.TTFTSampleCount)
	assert.Equal(t, int64(1), mixed.RealTTFTSampleCount)
	assert.Nil(t, history.Buckets[57].TTFTMs, "missing first-token measurements must not become zero")
	assert.Equal(t, "degraded", history.Buckets[57].Status, "a completed success with unknown timing remains an observed degraded period")
	assert.Equal(t, int64(1), history.Buckets[57].DegradedCount)
	assert.Equal(t, int64(1), history.DegradedCount)
	assert.Equal(t, "no_data", history.Buckets[59].Status)
}

func TestHistoryConcurrentAndLateWritesDoNotLoseCountsOrRollLatestBack(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()
	base := time.Now().Truncate(time.Minute)
	var group sync.WaitGroup
	errors := make(chan error, 40)
	for i := range 40 {
		group.Go(func() {
			source, status := "real", "healthy"
			if i%2 == 0 {
				source, status = "probe", "error"
			}
			errors <- writeSample(context.Background(), target.Group, target.Model, source, Sample{Status: status, ObservedAt: base.Unix(), OrderMS: base.UnixMilli() + int64(i), TTFTMs: 100, LatencyMs: 200})
		})
	}
	group.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.NoError(t, writeSample(context.Background(), target.Group, target.Model, "real", Sample{Status: "error", ObservedAt: base.Add(-time.Minute).Unix(), OrderMS: base.Add(-time.Minute).UnixMilli()}))
	history, err := GetTargetHistory(context.Background(), target, 1800, base)
	require.NoError(t, err)
	assert.Equal(t, int64(41), history.SampleCount)
	assert.Equal(t, int64(20), history.SuccessCount)
	assert.Equal(t, int64(21), history.ErrorCount)
	assert.Equal(t, int64(40), history.Buckets[29].SampleCount)
	assert.Equal(t, "mixed", history.Buckets[29].Status)
	latest, err := readSample(context.Background(), target.Group, target.Model, "real")
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, "healthy", latest.Status, "late history must not replace a newer latest observation")
}

func TestHistoryWindowRollupAndRingRetention(t *testing.T) {
	server := setupChannelStatusStore(t)
	target := testTarget()
	base := time.Now().Truncate(time.Minute)
	for _, seconds := range []int{1800, 3600, 21600, 86400} {
		t.Run(fmt.Sprint(seconds), func(t *testing.T) {
			history, err := GetTargetHistory(context.Background(), target, seconds, base)
			require.NoError(t, err)
			assert.LessOrEqual(t, len(history.Buckets), 60)
			assert.Equal(t, seconds, history.BucketSeconds*len(history.Buckets))
			assert.Equal(t, int64(seconds), history.EndAt-history.StartAt)
		})
	}
	_, err := GetTargetHistory(context.Background(), target, 42, base)
	require.Error(t, err)
	require.NoError(t, writeSample(context.Background(), target.Group, target.Model, "probe", Sample{Status: "healthy", ObservedAt: base.Add(-time.Minute).Unix(), TTFTMs: 100}))
	require.NoError(t, writeSample(context.Background(), target.Group, target.Model, "real", Sample{Status: "error", ObservedAt: base.Add(-2 * time.Minute).Unix(), TTFTMs: 300}))
	history, err := GetTargetHistory(context.Background(), target, 21600, base)
	require.NoError(t, err)
	assert.Equal(t, "mixed", history.Buckets[59].Status)
	assert.Equal(t, int64(2), history.Buckets[59].SampleCount)
	require.NotNil(t, history.Buckets[59].TTFTMs)
	assert.Equal(t, float64(200), *history.Buckets[59].TTFTMs)

	// Populate all ring slots with an expired day, then reuse the current slot.
	key := redisKey(target.Group, target.Model, "history")
	for i := range historyRetentionMinutes {
		minute := base.Unix()/60 - int64(historyRetentionMinutes) - int64(i)
		data, err := common.Marshal(historyMinute{Minute: minute, SuccessCount: 7, ProbeCount: 7})
		require.NoError(t, err)
		require.NoError(t, common.RDB.HSet(context.Background(), key, fmt.Sprint(minute%historyRetentionMinutes), data).Err())
	}
	require.NoError(t, RecordProbeSample(target.Group, target.Model, Sample{Status: "healthy", ObservedAt: base.Unix(), TTFTMs: 50, LatencyMs: 100}))
	require.NoError(t, RecordProbeSample(target.Group, target.Model, Sample{Status: "error", ObservedAt: base.Add(-24 * time.Hour).Unix(), TTFTMs: 999}))
	assert.Equal(t, int64(historyRetentionMinutes), common.RDB.HLen(context.Background(), key).Val())
	history, err = GetTargetHistory(context.Background(), target, 86400, base)
	require.NoError(t, err)
	assert.Equal(t, int64(1), history.SampleCount, "expired ring contents and old late writes must not become fresh observations")
	assert.Equal(t, int64(1), history.SuccessCount)
	assert.Zero(t, history.ErrorCount)
	assert.Equal(t, "healthy", history.Buckets[59].Status)
	server.FastForward(25*time.Hour + time.Second)
	assert.False(t, server.Exists(key), "inactive history must expire")
}

func TestHistoryRejectsMalformedBucketsWithoutFakingHealthy(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()
	base := time.Now().Truncate(time.Minute)
	key := redisKey(target.Group, target.Model, "history")
	slot := fmt.Sprint(base.Unix() / 60 % historyRetentionMinutes)
	for _, test := range []struct {
		name string
		data string
	}{
		{"invalid JSON", `broken`},
		{"mismatched observed counters", fmt.Sprintf(`{"minute":%d,"success":1}`, base.Unix()/60)},
		{"negative counters", fmt.Sprintf(`{"minute":%d,"success":-1,"probe":-1}`, base.Unix()/60)},
		{"degraded exceeds successful observations", fmt.Sprintf(`{"minute":%d,"success":1,"probe":1,"degraded":2}`, base.Unix()/60)},
		{"unmeasured nonzero TTFT", fmt.Sprintf(`{"minute":%d,"success":1,"probe":1,"ttft_sum":5}`, base.Unix()/60)},
		{"real TTFT exceeds all TTFT", fmt.Sprintf(`{"minute":%d,"success":1,"real":1,"real_ttft_sum":5,"real_ttft_count":1}`, base.Unix()/60)},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, common.RDB.HSet(context.Background(), key, slot, test.data).Err())
			_, err := GetTargetHistory(context.Background(), target, 3600, base)
			require.Error(t, err, "corrupt observations must not be converted into a healthy historical period")
		})
	}

	// A failed history write must not replace a valid latest observation.
	previous := Sample{Status: "healthy", Source: "probe", ObservedAt: base.Add(-time.Minute).Unix()}
	encoded, err := common.Marshal(previous)
	require.NoError(t, err)
	require.NoError(t, common.RDB.Set(context.Background(), redisKey(target.Group, target.Model, "probe"), encoded, time.Hour).Err())
	require.NoError(t, common.RDB.HSet(context.Background(), key, slot, `broken`).Err())
	require.Error(t, RecordProbeSample(target.Group, target.Model, Sample{Status: "error", ObservedAt: base.Unix()}))
	latest, err := readSample(context.Background(), target.Group, target.Model, "probe")
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, previous.ObservedAt, latest.ObservedAt)
	assert.Equal(t, previous.Status, latest.Status)
}

func TestObservationHistoryRetainsIndividualRequestsWithStableReplayAndLateOrdering(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()
	base := time.Now().Truncate(time.Minute)
	for _, item := range []struct {
		source string
		sample Sample
	}{
		{"real", Sample{ID: "real-a", Status: "healthy", ObservedAt: base.Unix(), OrderMS: base.UnixMilli() + 400, TTFTMs: 100, LatencyMs: 200}},
		{"real", Sample{ID: "real-b", Status: "error", ObservedAt: base.Unix(), OrderMS: base.UnixMilli() + 400}},
		{"probe", Sample{ID: "probe", Status: "healthy", ObservedAt: base.Unix(), OrderMS: base.UnixMilli() + 500, TTFTMs: 150, LatencyMs: 250}},
		{"real", Sample{ID: "late-real", Status: "healthy", ObservedAt: base.Add(-time.Minute).Unix(), OrderMS: base.Add(-time.Minute).UnixMilli()}},
		// Re-recording the same completed request must not create another bar,
		// even if the caller supplies a later recording time or new metrics.
		{"real", Sample{ID: "real-a", Status: "healthy", ObservedAt: base.Unix(), OrderMS: base.UnixMilli() + 900, TTFTMs: 999, LatencyMs: 1000}},
	} {
		require.NoError(t, writeSample(context.Background(), target.Group, target.Model, item.source, item.sample))
	}
	history, err := GetTargetHistory(context.Background(), target, 3600, base)
	require.NoError(t, err)
	require.Len(t, history.Observations, 4)
	assert.Equal(t, []string{"probe", "real-b", "real-a", "late-real"}, []string{
		history.Observations[0].ID, history.Observations[1].ID, history.Observations[2].ID, history.Observations[3].ID,
	})
	assert.Equal(t, "probe", history.Observations[0].Source)
	assert.Equal(t, "error", history.Observations[1].Status)
	assert.Zero(t, history.Observations[1].TTFTMs, "failed requests without first output retain an unknown first token")
	assert.Equal(t, int64(100), history.Observations[2].TTFTMs)
	assert.Equal(t, "degraded", history.Observations[3].Status)
	require.Len(t, history.RealSamples, 3)
	assert.Equal(t, []string{"real-b", "real-a", "late-real"}, []string{
		history.RealSamples[0].ID, history.RealSamples[1].ID, history.RealSamples[2].ID,
	})
	assert.Equal(t, int64(4), history.SampleCount, "replay must not inflate retained legacy counts either")
}

func TestObservationHistoryKeepsRealSamplesWhenMixedHistoryIsFilledByProbes(t *testing.T) {
	server := setupChannelStatusStore(t)
	target := testTarget()
	base := time.Now().Truncate(time.Minute)
	for index := range 31 {
		require.NoError(t, writeSample(context.Background(), target.Group, target.Model, "real", Sample{
			ID: fmt.Sprintf("real-%02d", index), Status: "healthy", ObservedAt: base.Add(-5 * time.Minute).Unix(),
			OrderMS: base.Add(-5*time.Minute).UnixMilli() + int64(index), TTFTMs: 100, LatencyMs: 200,
		}))
	}
	for index := range 61 {
		require.NoError(t, RecordProbeSample(target.Group, target.Model, Sample{
			ID: fmt.Sprintf("probe-%02d", index), Status: "healthy", ObservedAt: base.Add(-time.Minute).Unix(),
			OrderMS: base.Add(-time.Minute).UnixMilli() + int64(index), TTFTMs: 150, LatencyMs: 250,
		}))
	}
	// A late request older than both retained sets must not displace newer data.
	require.NoError(t, writeSample(context.Background(), target.Group, target.Model, "real", Sample{
		ID: "late", Status: "error", ObservedAt: base.Add(-10 * time.Minute).Unix(), OrderMS: base.Add(-10 * time.Minute).UnixMilli(),
	}))
	history, err := GetTargetHistory(context.Background(), target, 3600, base)
	require.NoError(t, err)
	require.Len(t, history.Observations, 60)
	assert.Equal(t, "probe-60", history.Observations[0].ID)
	assert.Equal(t, "probe-01", history.Observations[59].ID)
	require.Len(t, history.RealSamples, 30)
	assert.Equal(t, "real-30", history.RealSamples[0].ID)
	assert.Equal(t, "real-01", history.RealSamples[29].ID)
	assert.Equal(t, int64(90), common.RDB.HLen(context.Background(), redisKey(target.Group, target.Model, "observation-data")).Val())
	assert.False(t, common.RDB.HExists(context.Background(), redisKey(target.Group, target.Model, "observation-data"), "late").Val())
	server.FastForward(25*time.Hour + time.Second)
	for _, suffix := range []string{"observations", "real-samples", "observation-data"} {
		assert.False(t, server.Exists(redisKey(target.Group, target.Model, suffix)), "inactive raw observations must expire")
	}
}

func TestCompletedRealObservationAtomicallyClearsOnlyItsOwnActiveMarker(t *testing.T) {
	setupChannelStatusStore(t)
	target := testTarget()
	base := time.Now()
	require.NoError(t, MarkRequestStarted(target.Group, target.Model, 7, "private-request-a", base))
	require.NoError(t, MarkRequestStarted(target.Group, target.Model, 7, "private-request-b", base))
	sample := Sample{ID: "opaque-a", Status: "healthy", ObservedAt: base.Unix(), OrderMS: base.UnixMilli(),
		TTFTMs: 100, LatencyMs: 200, activeRequestID: "private-request-a"}
	require.NoError(t, writeSample(context.Background(), target.Group, target.Model, "real", sample))
	assert.Equal(t, int64(1), common.RDB.ZCard(context.Background(), redisKey(target.Group, target.Model, "real-active")).Val())
	require.NoError(t, common.RDB.ZScore(context.Background(), redisKey(target.Group, target.Model, "real-active"), "private-request-b").Err())
	payload := common.RDB.HGet(context.Background(), redisKey(target.Group, target.Model, "observation-data"), "opaque-a").Val()
	assert.NotContains(t, payload, "private-request-a", "public observation data must never expose raw request IDs")
	// A duplicate completion still cleans its marker without a duplicate bar.
	require.NoError(t, MarkRequestStarted(target.Group, target.Model, 7, "private-request-a", base))
	require.NoError(t, writeSample(context.Background(), target.Group, target.Model, "real", sample))
	assert.Equal(t, int64(1), common.RDB.ZCard(context.Background(), redisKey(target.Group, target.Model, "real-active")).Val())
	history, err := GetTargetHistory(context.Background(), target, 3600, base)
	require.NoError(t, err)
	require.Len(t, history.Observations, 1)
	MarkRequestFinished(target.Group, target.Model, "private-request-b")
	require.Eventually(t, func() bool {
		return common.RDB.ZCard(context.Background(), redisKey(target.Group, target.Model, "real-active")).Val() == 0
	}, time.Second, time.Millisecond)
}
