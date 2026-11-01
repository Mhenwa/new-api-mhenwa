package channelstatus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/go-redis/redis/v8"
)

const (
	sampleTTL                      = 24 * time.Hour
	activeRequestLeaseTTL          = 2 * time.Minute
	activeRequestHeartbeatInterval = 30 * time.Second
)

var (
	activeOwnerIDMu sync.Mutex
	activeOwnerID   string
	activeRequests  sync.Map
)

type Sample struct {
	ID                string  `json:"id,omitempty"`
	Status            string  `json:"status"`
	Source            string  `json:"source"`
	ObservedAt        int64   `json:"observed_at"`
	OrderMS           int64   `json:"order_ms"`
	StartedAt         int64   `json:"request_started_at,omitempty"`
	TTFTMs            int64   `json:"ttft_ms,omitempty"`
	LatencyMs         int64   `json:"latency_ms,omitempty"`
	TPS               float64 `json:"tps,omitempty"`
	OutputTokens      *int64  `json:"output_tokens"`
	OutputRateVersion int     `json:"output_rate_version,omitempty"`
	activeRequestID   string
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
	OutputTokens     *int64  `json:"output_tokens"`
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
local legacy = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', ARGV[4])
for _, member in ipairs(legacy) do
  if not string.find(member, string.char(0), 1, true) then
    redis.call('ZREM', KEYS[2], member)
  end
end
local clock = redis.call('TIME')
local nowMS = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
redis.call('ZADD', KEYS[5], nowMS, ARGV[3])
redis.call('EXPIRE', KEYS[2], ARGV[2])
redis.call('EXPIRE', KEYS[5], ARGV[2])
return 1
`)

var refreshActiveRequestLeaseScript = redis.NewScript(`
if not redis.call('ZSCORE', KEYS[1], ARGV[1]) or
   not redis.call('ZSCORE', KEYS[2], ARGV[1]) then
  return 0
end
local clock = redis.call('TIME')
local nowMS = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
redis.call('ZADD', KEYS[2], nowMS, ARGV[1])
redis.call('EXPIRE', KEYS[1], ARGV[2])
redis.call('EXPIRE', KEYS[2], ARGV[2])
return 1
`)

var finishActiveRequestScript = redis.NewScript(`
local removed = redis.call('ZREM', KEYS[1], ARGV[1])
redis.call('ZREM', KEYS[2], ARGV[1])
return removed
`)

var clearStaleActiveRequestsScript = redis.NewScript(`
local clock = redis.call('TIME')
local nowMS = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local members = redis.call('ZRANGE', KEYS[1], 0, -1, 'WITHSCORES')
for index = 1, #members, 2 do
  local member = members[index]
  local stale = false
  if string.find(member, string.char(0), 1, true) then
    local lease = redis.call('ZSCORE', KEYS[2], member)
    stale = not lease or tonumber(lease) <= nowMS - tonumber(ARGV[1])
  else
    stale = tonumber(members[index + 1]) <= tonumber(ARGV[2])
  end
  if stale then
    redis.call('ZREM', KEYS[1], member)
    redis.call('ZREM', KEYS[2], member)
  end
end
return redis.call('ZCARD', KEYS[1])
`)

var newerSampleScript = redis.NewScript(`
if ARGV[5] == 'real' and ARGV[7] ~= '' then
  redis.call('ZREM', KEYS[6], ARGV[7])
  redis.call('ZREM', KEYS[7], ARGV[7])
end
local current = redis.call('GET', KEYS[1])
local updateLatest = true
if current then
  local ok, decoded = pcall(cjson.decode, current)
  if ok and tonumber(decoded['order_ms'] or 0) > tonumber(ARGV[2]) then
    updateLatest = false
  end
end
local sample = cjson.decode(ARGV[1])
local minute = math.floor(tonumber(sample['observed_at'] or 0) / 60)
local nowMinute = tonumber(ARGV[4])
local status = sample['status']
local source = ARGV[5]
if minute < nowMinute - 1439 or minute > nowMinute or
   (status ~= 'healthy' and status ~= 'degraded' and status ~= 'error') or
   (source ~= 'real' and source ~= 'probe') then
  if updateLatest then redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[3]) end
  return 1
end
local slot = tostring(minute % 1440)
local stored = redis.call('HGET', KEYS[2], slot)
local bucket = nil
if stored then
  local ok, decoded = pcall(cjson.decode, stored)
  if not ok then return redis.error_reply('invalid channel status history bucket') end
  if tonumber(decoded['minute']) > minute then
    if updateLatest then redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[3]) end
    return 1
  end
  if tonumber(decoded['minute']) == minute then bucket = decoded end
end
if not bucket then
  bucket = {minute=minute, success=0, error=0, degraded=0, real=0, probe=0,
            ttft_sum=0, ttft_count=0, real_ttft_sum=0, real_ttft_count=0}
end
local id = sample['id']
if redis.call('HEXISTS', KEYS[5], id) == 1 then return 0 end
if status == 'healthy' or status == 'degraded' then bucket['success'] = bucket['success'] + 1
else bucket['error'] = bucket['error'] + 1 end
if status == 'degraded' then bucket['degraded'] = (bucket['degraded'] or 0) + 1 end
bucket[source] = bucket[source] + 1
local ttft = tonumber(sample['ttft_ms'] or 0)
if ttft > 0 then
  bucket['ttft_sum'] = bucket['ttft_sum'] + ttft
  bucket['ttft_count'] = bucket['ttft_count'] + 1
  if source == 'real' then
    bucket['real_ttft_sum'] = bucket['real_ttft_sum'] + ttft
    bucket['real_ttft_count'] = bucket['real_ttft_count'] + 1
  end
end
if updateLatest then
  redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[3])
end
redis.call('HSET', KEYS[2], slot, cjson.encode(bucket))
redis.call('EXPIRE', KEYS[2], ARGV[6])
redis.call('HSET', KEYS[5], id, ARGV[1])
redis.call('ZADD', KEYS[3], ARGV[2], id)
if source == 'real' then redis.call('ZADD', KEYS[4], ARGV[2], id) end
local removed = redis.call('ZRANGE', KEYS[3], 0, -61)
redis.call('ZREMRANGEBYRANK', KEYS[3], 0, -61)
local removedReal = redis.call('ZRANGE', KEYS[4], 0, -31)
redis.call('ZREMRANGEBYRANK', KEYS[4], 0, -31)
for _, member in ipairs(removedReal) do table.insert(removed, member) end
for _, member in ipairs(removed) do
  if not redis.call('ZSCORE', KEYS[3], member) and not redis.call('ZSCORE', KEYS[4], member) then
    redis.call('HDEL', KEYS[5], member)
  end
end
redis.call('EXPIRE', KEYS[3], ARGV[6])
redis.call('EXPIRE', KEYS[4], ARGV[6])
redis.call('EXPIRE', KEYS[5], ARGV[6])
return 1
`)

func RedisAvailable() bool {
	return common.RedisEnabled && common.RDB != nil
}

func IsConfigured() bool {
	targets, err := LoadTargets()
	return err == nil && len(targets) > 0
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
	ownerID, err := currentActiveOwnerID()
	if err != nil {
		return err
	}
	member := activeRequestMember(ownerID, requestID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	startKey := redisKey(group, model, "real-start")
	activeKey := redisKey(group, model, "real-active")
	if err := maxStartScript.Run(ctx, common.RDB, []string{
		startKey, activeKey, redisKey(group, model, "probe-lock"), redisKey(group, model, "probe-cancel"),
		activeRequestLeasesKey(group, model),
	}, at.UnixMilli(), int64(sampleTTL.Seconds()), member, time.Now().Add(-sampleTTL).UnixMilli()).Err(); err != nil {
		return err
	}
	startActiveRequestHeartbeat(group, model, requestID, member)
	return nil
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
	stopActiveRequestHeartbeat(group, model, requestID)
	if !RedisAvailable() || requestID == "" {
		return
	}
	ownerID, err := currentActiveOwnerID()
	if err != nil {
		return
	}
	// The caller retains the groups/models it registered. Cleanup must survive
	// an administrator removing a target while its request is still running.
	client := common.RDB
	member := activeRequestMember(ownerID, requestID)
	gopool.Go(func() {
		var err error
		for range 2 {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err = finishActiveRequestScript.Run(ctx, client, []string{
				redisKey(group, model, "real-active"), activeRequestLeasesKey(group, model),
			}, member).Err()
			cancel()
			if err == nil {
				return
			}
		}
		common.SysLog(common.LogText("channel status active request cleanup failed: %v", err))
	})
}

func currentActiveOwnerID() (string, error) {
	activeOwnerIDMu.Lock()
	defer activeOwnerIDMu.Unlock()
	if activeOwnerID != "" {
		return activeOwnerID, nil
	}
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return "", err
	}
	activeOwnerID = hex.EncodeToString(identity[:])
	return activeOwnerID, nil
}

func activeRequestMember(ownerID, requestID string) string {
	// Scope each marker to a process lifetime, including when nodes reuse a
	// request ID. A restarted process must never finish another node's request.
	return ownerID + "\x00" + requestID
}

func activeRequestHeartbeatKey(group, model, requestID string) string {
	return targetKey(group, model) + "\x00" + requestID
}

func activeRequestLeasesKey(group, model string) string {
	return redisKey(group, model, "real-active-leases")
}

func startActiveRequestHeartbeat(group, model, requestID, member string) {
	heartbeatKey := activeRequestHeartbeatKey(group, model, requestID)
	stop := make(chan struct{})
	if _, loaded := activeRequests.LoadOrStore(heartbeatKey, stop); loaded {
		return
	}
	gopool.Go(func() {
		defer activeRequests.CompareAndDelete(heartbeatKey, stop)
		ticker := time.NewTicker(activeRequestHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if !RedisAvailable() {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				active, err := refreshActiveRequestLease(ctx, group, model, member)
				cancel()
				if err == nil && !active {
					return
				}
			}
		}
	})
}

func stopActiveRequestHeartbeat(group, model, requestID string) {
	if value, loaded := activeRequests.LoadAndDelete(activeRequestHeartbeatKey(group, model, requestID)); loaded {
		close(value.(chan struct{}))
	}
}

func refreshActiveRequestLease(ctx context.Context, group, model, member string) (bool, error) {
	result, err := refreshActiveRequestLeaseScript.Run(ctx, common.RDB, []string{
		redisKey(group, model, "real-active"), activeRequestLeasesKey(group, model),
	}, member, int64(sampleTTL.Seconds())).Int64()
	return result == 1, err
}

func clearStaleActiveRequests(ctx context.Context, group, model string, now time.Time) (int64, error) {
	// Lease times use Redis's clock so B/D clock skew cannot remove a live
	// request. Legacy markers have no heartbeat and retain the old 24h grace.
	return clearStaleActiveRequestsScript.Run(ctx, common.RDB, []string{
		redisKey(group, model, "real-active"), activeRequestLeasesKey(group, model),
	}, activeRequestLeaseTTL.Milliseconds(), now.Add(-sampleTTL).UnixMilli()).Int64()
}

func RecordProbeSample(group, model string, sample Sample) error {
	if !RedisAvailable() {
		return errors.New("channel status requires Redis")
	}
	sample.Source = "probe"
	return writeSample(context.Background(), group, model, "probe", sample)
}

func writeSample(parent context.Context, group, model, source string, sample Sample) error {
	sample.Source = source
	sample.Status = classifySampleStatus(sample)
	if sample.ID == "" {
		var identity [16]byte
		if _, err := rand.Read(identity[:]); err != nil {
			return err
		}
		sample.ID = hex.EncodeToString(identity[:])
	}
	if sample.OrderMS == 0 {
		sample.OrderMS = sample.ObservedAt * 1000
	}
	data, err := common.Marshal(sample)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	// Real samples already enter here through the asynchronous recording queue.
	// One bounded Lua operation retains the latest 60 observations and 30 real
	// samples independently. The minute ring remains for existing clients and
	// rollback; no aggregated or latest-only data is backfilled as raw samples.
	return newerSampleScript.Run(ctx, common.RDB, []string{
		redisKey(group, model, source), redisKey(group, model, "history"),
		redisKey(group, model, "observations"), redisKey(group, model, "real-samples"),
		redisKey(group, model, "observation-data"),
		redisKey(group, model, "real-active"),
		activeRequestLeasesKey(group, model),
	}, data, sample.OrderMS, int64(sampleTTL.Seconds()), time.Now().Unix()/60, source, int64((sampleTTL + time.Hour).Seconds()), sample.activeRequestID).Err()
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
	return resolveTargetStatus(target, real, probe), nil
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

func resolveTargetStatus(target Target, real, probe *Sample) TargetStatus {
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
	return result
}

// Availability uses the same danger thresholds as the usage-log timing badges.
// A successful observation with missing timing stays degraded, not no_data;
// output tokens absent in a legacy sample use the log's short-response branch.
func classifySampleStatus(sample Sample) string {
	if sample.Status == "error" || sample.Status == "degraded" {
		return sample.Status
	}
	if sample.Status != "healthy" {
		return "no_data"
	}
	if sample.TTFTMs <= 0 || sample.LatencyMs <= 0 || sample.TTFTMs >= 10_000 {
		return "degraded"
	}
	outputTokens := int64(0)
	if sample.OutputTokens != nil {
		outputTokens = max(int64(0), *sample.OutputTokens)
	}
	if outputTokens < 100 {
		if sample.LatencyMs >= 30_000 {
			return "degraded"
		}
	} else if float64(outputTokens)/(float64(sample.LatencyMs)/1_000) < 15 {
		return "degraded"
	}
	return "healthy"
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
	tps := float64(0)
	if sample.OutputRateVersion == outputRateVersion {
		tps = sample.TPS
	}
	return TargetStatus{
		Group: target.Group, Model: target.Model, Status: classifySampleStatus(*sample), Source: sample.Source,
		ObservedAt: sample.ObservedAt, RequestStartedAt: sample.StartedAt,
		TTFTMs: sample.TTFTMs, LatencyMs: sample.LatencyMs, TPS: tps, OutputTokens: sample.OutputTokens,
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
