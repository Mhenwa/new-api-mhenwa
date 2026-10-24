package channelstatus

import (
	"time"

	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

func RecordRelayResult(info *relaycommon.RelayInfo, apiErr *types.NewAPIError) {
	if info == nil || !IsConfigured() || !RedisAvailable() {
		return
	}
	outcome := perfmetrics.ClassifyRelayOutcome(nil, info, apiErr)
	if outcome == perfmetrics.OutcomeIgnored {
		return
	}
	now := time.Now()
	started := info.StartTime
	if started.IsZero() {
		started = now
	}
	latencyMs := max(int64(0), now.Sub(started).Milliseconds())
	hasTTFT := info.IsStream && info.HasSendResponse()
	ttftMs := int64(0)
	generationMs := latencyMs
	if hasTTFT {
		ttftMs = max(int64(0), info.FirstResponseTime.Sub(started).Milliseconds())
		generationMs = max(int64(0), now.Sub(info.FirstResponseTime).Milliseconds())
	}
	tps := float64(0)
	if info.PerformanceOutputTokens > 0 && generationMs > 0 {
		tps = float64(info.PerformanceOutputTokens) * 1000 / float64(generationMs)
	}
	status := "healthy"
	if outcome == perfmetrics.OutcomeFailure {
		status = "error"
	}
	_ = RecordRealSample(info.UsingGroup, info.OriginModelName, info.TokenId, Sample{
		Status: status, ObservedAt: now.Unix(), OrderMS: now.UnixMilli(), StartedAt: started.Unix(), TTFTMs: ttftMs,
		LatencyMs: latencyMs, TPS: tps,
	})
}
