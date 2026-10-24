package channelstatus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/go-redis/redis/v8"
)

const sampleTTL = 24 * time.Hour

type Sample struct {
	Status     string  `json:"status"`
	Source     string  `json:"source"`
	ObservedAt int64   `json:"observed_at"`
	OrderMS    int64   `json:"order_ms"`
	StartedAt  int64   `json:"request_started_at,omitempty"`
	TTFTMs     int64   `json:"ttft_ms,omitempty"`
	LatencyMs  int64   `json:"latency_ms,omitempty"`
	TPS        float64 `json:"tps,omitempty"`
}

type TargetStatus struct {
	Group            string  `json:"group"`
	Model            string  `json:"model"`
	Status           string  `json:"status"`
	Source           string  `json:"source"`
	ObservedAt       int64   `json:"observed_at,omitempty"`
	RequestStartedAt int64   `json:"request_started_at,omitempty"`
	TTFTMs           int64   `json:"ttft_ms,omitempty"`
	LatencyMs        int64   `json:"latency_ms,omitempty"`
	TPS              float64 `json:"tps,omitempty"`
}

var maxStartScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
local probeOwner = redis.call('GET', KEYS[3])
if probeOwner then
  redis.call('SET', KEYS[4], probeOwner, 'EX', ARGV[2])
end
if not current or tonumber(ARGV[1]) > tonumber(current) then
  redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
else
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
redis.call('ZADD', KEYS[2], ARGV[1], ARGV[3])
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', ARGV[4])
redis.call('EXPIRE', KEYS[2], ARGV[2])
return 1
`)

var newerSampleScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current then
  local ok, decoded = pcall(cjson.decode, current)
  if ok and tonumber(decoded['order_ms'] or 0) > tonumber(ARGV[2]) then
    return 0
  end
end
redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[3])
return 1
`)

func RedisAvailable() bool {
	return common.RedisEnabled && common.RDB != nil
}

func IsConfigured() bool {
	return strings.TrimSpace(os.Getenv(TargetsEnv)) != ""
}

func MarkRequestStarted(group, model string, tokenID int, requestID string, at time.Time) error {
	if !RedisAvailable() {
		return errors.New("channel status requires Redis")
	}
	targets, err := LoadTargets()
	if err != nil {
		return err
	}
	if _, ok := findTarget(targets, group, model); !ok || isProbeToken(targets, tokenID) {
		return nil
	}
	if requestID == "" {
		return errors.New("channel status request ID is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	startKey := redisKey(group, model, "real-start")
	activeKey := redisKey(group, model, "real-active")
	return maxStartScript.Run(ctx, common.RDB, []string{startKey, activeKey, redisKey(group, model, "probe-lock"), redisKey(group, model, "probe-cancel")}, at.UnixMilli(), int64(sampleTTL.Seconds()), requestID, at.Add(-sampleTTL).UnixMilli()).Err()
}

func RecordRealSample(group, model string, tokenID int, sample Sample) error {
	if !RedisAvailable() {
		return errors.New("channel status requires Redis")
	}
	targets, err := LoadTargets()
	if err != nil {
		return err
	}
	if _, ok := findTarget(targets, group, model); !ok || isProbeToken(targets, tokenID) {
		return nil
	}
	sample.Source = "real"
	gopool.Go(func() {
		if err := writeSample(context.Background(), group, model, "real", sample); err != nil {
			common.SysLog(common.LogText("channel status real sample write failed: %v", err))
		}
	})
	return nil
}

func MarkRequestFinished(group, model, requestID string) {
	if !RedisAvailable() || requestID == "" {
		return
	}
	targets, err := LoadTargets()
	if err != nil {
		return
	}
	if _, ok := findTarget(targets, group, model); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := common.RDB.ZRem(ctx, redisKey(group, model, "real-active"), requestID).Err(); err != nil {
		common.SysLog(common.LogText("channel status active request cleanup failed: %v", err))
	}
}

func RecordProbeSample(group, model string, sample Sample) error {
	if !RedisAvailable() {
		return errors.New("channel status requires Redis")
	}
	sample.Source = "probe"
	return writeSample(context.Background(), group, model, "probe", sample)
}

func writeSample(parent context.Context, group, model, source string, sample Sample) error {
	if sample.OrderMS == 0 {
		sample.OrderMS = sample.ObservedAt * 1000
	}
	data, err := common.Marshal(sample)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	return newerSampleScript.Run(ctx, common.RDB, []string{redisKey(group, model, source)}, data, sample.OrderMS, int64(sampleTTL.Seconds())).Err()
}

func GetTargetStatus(ctx context.Context, target Target) (TargetStatus, error) {
	if !RedisAvailable() {
		return TargetStatus{}, errors.New("channel status requires Redis")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	probe, err := readSample(ctx, target.Group, target.Model, "probe")
	if err != nil {
		return TargetStatus{}, err
	}
	real, err := readSample(ctx, target.Group, target.Model, "real")
	if err != nil {
		return TargetStatus{}, err
	}
	activeRequests, err := common.RDB.ZRevRangeWithScores(ctx, redisKey(target.Group, target.Model, "real-active"), 0, 0).Result()
	if err != nil {
		return TargetStatus{}, err
	}
	activeAt := int64(0)
	if len(activeRequests) > 0 {
		activeAt = int64(activeRequests[0].Score)
	}
	status := resolveTargetStatus(target, real, probe, activeAt)
	if status.Status != "probing" && status.ObservedAt > 0 && target.IntervalSeconds > 0 && time.Now().Unix()-status.ObservedAt > int64(2*target.IntervalSeconds) {
		status.Status = "no_data"
	}
	return status, nil
}

func readSample(ctx context.Context, group, model, source string) (*Sample, error) {
	data, err := common.RDB.Get(ctx, redisKey(group, model, source)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sample Sample
	if err := common.Unmarshal(data, &sample); err != nil {
		return nil, fmt.Errorf("invalid channel status sample")
	}
	return &sample, nil
}

func resolveTargetStatus(target Target, real, probe *Sample, latestActiveAt int64) TargetStatus {
	result := TargetStatus{Group: target.Group, Model: target.Model, Status: "no_data", Source: "none"}
	latestObserved := int64(0)
	if real != nil && sampleOrderMS(real) > latestObserved {
		latestObserved = sampleOrderMS(real)
		result = statusFromSample(target, real)
	}
	if probe != nil && sampleOrderMS(probe) > latestObserved {
		latestObserved = sampleOrderMS(probe)
		result = statusFromSample(target, probe)
	}
	if latestActiveAt >= latestObserved && latestActiveAt > 0 {
		result.Status = "probing"
		result.Source = "real"
		result.RequestStartedAt = latestActiveAt / 1000
	}
	return result
}

func sampleOrderMS(sample *Sample) int64 {
	if sample == nil {
		return 0
	}
	if sample.OrderMS > 0 {
		return sample.OrderMS
	}
	return sample.ObservedAt * 1000
}

func statusFromSample(target Target, sample *Sample) TargetStatus {
	return TargetStatus{
		Group: target.Group, Model: target.Model, Status: sample.Status, Source: sample.Source,
		ObservedAt: sample.ObservedAt, RequestStartedAt: sample.StartedAt,
		TTFTMs: sample.TTFTMs, LatencyMs: sample.LatencyMs, TPS: sample.TPS,
	}
}

func redisKey(group, model, suffix string) string {
	return "newapi:channel-status:v1:" + targetKey(group, model) + ":" + suffix
}

func lastCheckAt(ctx context.Context, group, model string) (int64, error) {
	value, err := common.RDB.Get(ctx, redisKey(group, model, "last-check")).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(value, 10, 64)
}

func lastRealStartAt(ctx context.Context, group, model string) (int64, error) {
	value, err := common.RDB.Get(ctx, redisKey(group, model, "real-start")).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return value, err
}

func setLastCheck(ctx context.Context, group, model string, at time.Time) error {
	return common.RDB.Set(ctx, redisKey(group, model, "last-check"), at.Unix(), sampleTTL).Err()
}

func isProbeToken(targets []Target, tokenID int) bool {
	for _, target := range targets {
		if target.ProbeTokenID == tokenID {
			return true
		}
	}
	return false
}
