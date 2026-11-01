package channelstatus

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/tidwall/gjson"
)

const outputRateVersion = 1

// Sub-250 ms windows are commonly buffered bursts. Their delivery timing is
// too short to describe model generation speed; retain latency, omit speed.
const minOutputWindow = 250 * time.Millisecond

type streamOutputWindow struct {
	mu     sync.Mutex
	first  time.Time
	last   time.Time
	frames int
}

func (window *streamOutputWindow) Observe(payload []byte, at time.Time) {
	if !gjson.ValidBytes(payload) {
		return
	}
	value := gjson.ParseBytes(payload)
	content := false
	switch value.Get("type").String() {
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta", "response.refusal.delta", "response.function_call_arguments.delta":
		content = value.Get("delta").String() != ""
	case "content_block_delta":
		for _, path := range []string{"delta.text", "delta.thinking", "delta.partial_json"} {
			if value.Get(path).String() != "" {
				content = true
				break
			}
		}
	default:
		for _, choice := range value.Get("choices").Array() {
			for _, path := range []string{"delta.content", "delta.reasoning_content", "delta.reasoning", "delta.refusal"} {
				part := choice.Get(path)
				if part.Type == gjson.String && part.String() != "" {
					content = true
				}
			}
			for _, call := range choice.Get("delta.tool_calls").Array() {
				if call.Get("function.arguments").String() != "" {
					content = true
				}
			}
		}
		for _, candidate := range value.Get("candidates").Array() {
			for _, part := range candidate.Get("content.parts").Array() {
				if part.Get("text").String() != "" {
					content = true
				}
			}
		}
	}
	if !content {
		return
	}
	window.mu.Lock()
	defer window.mu.Unlock()
	if window.first.IsZero() {
		window.first = at
	}
	window.last = at
	window.frames++
}

func (window *streamOutputWindow) Snapshot() (first, last time.Time, frames int) {
	window.mu.Lock()
	defer window.mu.Unlock()
	return window.first, window.last, window.frames
}

// This is an estimate from upstream output usage and content delivery timing,
// not a count of wire chunks as tokens. The initial chunk is part of the usage
// numerator; without per-chunk usage its exact token contribution is unknown.
// Role, heartbeat, usage-only and terminal frames do not extend the window.
func observedOutputSpeed(window relaycommon.ChannelStatusOutputObserver, tokens int64) float64 {
	if window == nil || tokens <= 0 {
		return 0
	}
	first, last, frames := window.Snapshot()
	if frames < 2 || first.IsZero() || last.Sub(first) < minOutputWindow {
		return 0
	}
	return float64(tokens) / last.Sub(first).Seconds()
}

// AttachRelayObserver starts fresh for every channel attempt, including retries
// in the same group. Probe calls collect their own loopback measurements.
func AttachRelayObserver(info *relaycommon.RelayInfo) {
	if info == nil {
		return
	}
	info.ChannelStatusOutput = nil
	if !info.IsStream || !RedisAvailable() {
		return
	}
	targets, err := LoadTargets()
	if err != nil || isProbeToken(targets, info.TokenId) {
		return
	}
	if _, ok := findTarget(targets, info.UsingGroup, info.OriginModelName); ok {
		info.ChannelStatusOutput = &streamOutputWindow{}
	}
}

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
	if hasTTFT && info.ChannelStatusOutput == nil {
		ttftMs = max(int64(0), info.FirstResponseTime.Sub(started).Milliseconds())
	}
	tps := float64(0)
	if info.ChannelStatusOutput != nil {
		first, _, _ := info.ChannelStatusOutput.Snapshot()
		if !first.IsZero() {
			ttftMs = max(int64(0), first.Sub(started).Milliseconds())
		}
		if outcome == perfmetrics.OutcomeSuccess {
			tps = observedOutputSpeed(info.ChannelStatusOutput, info.PerformanceOutputTokens)
		}
	}
	status := "healthy"
	if outcome == perfmetrics.OutcomeFailure {
		status = "error"
	}
	outputTokens := max(int64(0), info.PerformanceOutputTokens)
	observationID := ""
	activeOwnerID := ""
	activeRequestID := ""
	if info.RequestId != "" {
		activeOwnerID, _ = currentActiveOwnerID()
		if activeOwnerID != "" {
			activeRequestID = activeRequestMember(activeOwnerID, info.RequestId)
		}
	}
	if info.RequestId != "" {
		identity := sha256.Sum256([]byte("real:" + info.RequestId))
		observationID = hex.EncodeToString(identity[:])
	}
	_ = RecordRealSample(info.UsingGroup, info.OriginModelName, info.TokenId, Sample{
		ID: observationID, Status: status, ObservedAt: now.Unix(), OrderMS: now.UnixMilli(), StartedAt: started.Unix(), TTFTMs: ttftMs,
		LatencyMs: latencyMs, TPS: tps, OutputTokens: &outputTokens, OutputRateVersion: outputRateVersion,
		activeRequestID: activeRequestID,
	})
}
