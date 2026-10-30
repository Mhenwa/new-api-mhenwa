package channelstatus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

const DefaultHistoryWindowSeconds = 3600
const historyRetentionMinutes = 24 * 60
const observationLimit = 60
const realSampleLimit = 30

// Read both bounded histories and their payloads atomically so a concurrent
// trim cannot remove a payload between reading its index and data.
const readObservationsScript = `
local observations = {}
local realSamples = {}
for _, id in ipairs(redis.call('ZREVRANGE', KEYS[1], 0, 59)) do
  table.insert(observations, redis.call('HGET', KEYS[3], id))
end
for _, id in ipairs(redis.call('ZREVRANGE', KEYS[2], 0, 29)) do
  table.insert(realSamples, redis.call('HGET', KEYS[3], id))
end
return {observations, realSamples}
`

// HistoryBucket describes observed requests, not continuous service uptime.
// Empty periods remain no_data, and mixed periods retain their failed samples.
type HistoryBucket struct {
	StartedAt           int64    `json:"started_at"`
	Status              string   `json:"status"`
	SampleCount         int64    `json:"sample_count"`
	SuccessCount        int64    `json:"success_count"`
	ErrorCount          int64    `json:"error_count"`
	DegradedCount       int64    `json:"degraded_count"`
	RealCount           int64    `json:"real_count"`
	ProbeCount          int64    `json:"probe_count"`
	TTFTMs              *float64 `json:"ttft_ms,omitempty"`
	TTFTSampleCount     int64    `json:"ttft_sample_count"`
	RealTTFTMs          *float64 `json:"real_ttft_ms,omitempty"`
	RealTTFTSampleCount int64    `json:"real_ttft_sample_count"`
}

type TargetHistory struct {
	ObservationLimit    int             `json:"observation_limit"`
	RealSampleLimit     int             `json:"real_sample_limit"`
	Observations        []Sample        `json:"observations"`
	RealSamples         []Sample        `json:"real_samples"`
	WindowSeconds       int             `json:"window_seconds"`
	BucketSeconds       int             `json:"bucket_seconds"`
	StartAt             int64           `json:"start_at"`
	EndAt               int64           `json:"end_at"`
	ObservedBucketCount int             `json:"observed_bucket_count"`
	TotalBucketCount    int             `json:"total_bucket_count"`
	SampleCount         int64           `json:"sample_count"`
	SuccessCount        int64           `json:"success_count"`
	ErrorCount          int64           `json:"error_count"`
	DegradedCount       int64           `json:"degraded_count"`
	SuccessRate         *float64        `json:"success_rate,omitempty"`
	Buckets             []HistoryBucket `json:"buckets"`
}

type historyMinute struct {
	Minute              int64 `json:"minute"`
	SuccessCount        int64 `json:"success"`
	ErrorCount          int64 `json:"error"`
	DegradedCount       int64 `json:"degraded"`
	RealCount           int64 `json:"real"`
	ProbeCount          int64 `json:"probe"`
	TTFTSum             int64 `json:"ttft_sum"`
	TTFTCount           int64 `json:"ttft_count"`
	RealTTFTSum         int64 `json:"real_ttft_sum"`
	RealTTFTSampleCount int64 `json:"real_ttft_count"`
}

// GetTargetHistory returns the newest individual observations and independent
// real samples. The minute buckets remain for older clients during rollout or
// rollback. Nothing is reconstructed from latest snapshots or consumption logs.
func GetTargetHistory(ctx context.Context, target Target, windowSeconds int, asOf time.Time) (TargetHistory, error) {
	if windowSeconds == 0 {
		windowSeconds = DefaultHistoryWindowSeconds
	}
	bucketSeconds := 60
	switch windowSeconds {
	case 1800, 3600:
	case 21600:
		bucketSeconds = 360
	case 86400:
		bucketSeconds = 1440
	default:
		return TargetHistory{}, errors.New("invalid channel status history window")
	}
	if !RedisAvailable() {
		return TargetHistory{}, errors.New("channel status requires Redis")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	end := (asOf.Unix()/60 + 1) * 60
	start := end - int64(windowSeconds)
	result := TargetHistory{
		ObservationLimit: observationLimit, RealSampleLimit: realSampleLimit,
		Observations: []Sample{}, RealSamples: []Sample{},
		WindowSeconds: windowSeconds, BucketSeconds: bucketSeconds,
		StartAt: start, EndAt: end, TotalBucketCount: windowSeconds / bucketSeconds,
		Buckets: make([]HistoryBucket, windowSeconds/bucketSeconds),
	}
	for i := range result.Buckets {
		result.Buckets[i] = HistoryBucket{StartedAt: start + int64(i*bucketSeconds), Status: "no_data"}
	}
	fields := make([]string, windowSeconds/60)
	for i := range fields {
		fields[i] = fmt.Sprint((start/60 + int64(i)) % historyRetentionMinutes)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var minuteValues *redis.SliceCmd
	var recentValues *redis.Cmd
	_, err := common.RDB.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		minuteValues = pipe.HMGet(ctx, redisKey(target.Group, target.Model, "history"), fields...)
		recentValues = pipe.Eval(ctx, readObservationsScript, []string{
			redisKey(target.Group, target.Model, "observations"),
			redisKey(target.Group, target.Model, "real-samples"),
			redisKey(target.Group, target.Model, "observation-data"),
		})
		return nil
	})
	if err != nil {
		return TargetHistory{}, err
	}
	values := minuteValues.Val()
	recent, ok := recentValues.Val().([]any)
	if !ok || len(recent) != 2 {
		return TargetHistory{}, errors.New("invalid channel status observations")
	}
	result.Observations, err = decodeHistoryObservations(recent[0], false)
	if err != nil {
		return TargetHistory{}, err
	}
	result.RealSamples, err = decodeHistoryObservations(recent[1], true)
	if err != nil {
		return TargetHistory{}, err
	}
	ttftSums := make([]int64, len(result.Buckets))
	realTTFTSums := make([]int64, len(result.Buckets))
	for i, value := range values {
		if value == nil {
			continue
		}
		encoded, ok := value.(string)
		if !ok {
			return TargetHistory{}, errors.New("invalid channel status history bucket")
		}
		var minute historyMinute
		if err := common.UnmarshalJsonStr(encoded, &minute); err != nil {
			return TargetHistory{}, errors.New("invalid channel status history bucket")
		}
		if minute.Minute != start/60+int64(i) {
			continue
		}
		if minute.SuccessCount < 0 || minute.ErrorCount < 0 || minute.DegradedCount < 0 ||
			minute.DegradedCount > minute.SuccessCount || minute.RealCount < 0 || minute.ProbeCount < 0 ||
			minute.SuccessCount+minute.ErrorCount != minute.RealCount+minute.ProbeCount || minute.TTFTCount < 0 ||
			minute.TTFTSum < 0 || minute.RealTTFTSampleCount < 0 || minute.RealTTFTSum < 0 ||
			minute.TTFTCount > minute.SuccessCount+minute.ErrorCount || minute.RealTTFTSampleCount > minute.RealCount ||
			minute.RealTTFTSampleCount > minute.TTFTCount || minute.RealTTFTSum > minute.TTFTSum ||
			(minute.TTFTCount == 0) != (minute.TTFTSum == 0) ||
			(minute.RealTTFTSampleCount == 0) != (minute.RealTTFTSum == 0) {
			return TargetHistory{}, errors.New("invalid channel status history counters")
		}
		index := i * 60 / bucketSeconds
		bucket := &result.Buckets[index]
		bucket.SuccessCount += minute.SuccessCount
		bucket.ErrorCount += minute.ErrorCount
		bucket.DegradedCount += minute.DegradedCount
		bucket.RealCount += minute.RealCount
		bucket.ProbeCount += minute.ProbeCount
		bucket.TTFTSampleCount += minute.TTFTCount
		bucket.RealTTFTSampleCount += minute.RealTTFTSampleCount
		ttftSums[index] += minute.TTFTSum
		realTTFTSums[index] += minute.RealTTFTSum
	}
	for i := range result.Buckets {
		bucket := &result.Buckets[i]
		bucket.SampleCount = bucket.SuccessCount + bucket.ErrorCount
		if bucket.SampleCount == 0 {
			continue
		}
		result.ObservedBucketCount++
		result.SampleCount += bucket.SampleCount
		result.SuccessCount += bucket.SuccessCount
		result.ErrorCount += bucket.ErrorCount
		result.DegradedCount += bucket.DegradedCount
		switch {
		case bucket.SuccessCount > 0 && bucket.ErrorCount > 0:
			bucket.Status = "mixed"
		case bucket.ErrorCount > 0:
			bucket.Status = "error"
		case bucket.DegradedCount > 0:
			bucket.Status = "degraded"
		default:
			bucket.Status = "healthy"
		}
		if bucket.TTFTSampleCount > 0 {
			mean := float64(ttftSums[i]) / float64(bucket.TTFTSampleCount)
			bucket.TTFTMs = &mean
		}
		if bucket.RealTTFTSampleCount > 0 {
			mean := float64(realTTFTSums[i]) / float64(bucket.RealTTFTSampleCount)
			bucket.RealTTFTMs = &mean
		}
	}
	if result.SampleCount > 0 {
		rate := float64(result.SuccessCount) * 100 / float64(result.SampleCount)
		result.SuccessRate = &rate
	}
	return result, nil
}

func decodeHistoryObservations(value any, realOnly bool) ([]Sample, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, errors.New("invalid channel status observations")
	}
	samples := make([]Sample, 0, len(values))
	for _, value := range values {
		encoded, ok := value.(string)
		if !ok {
			return nil, errors.New("invalid channel status observation payload")
		}
		var sample Sample
		if err := common.UnmarshalJsonStr(encoded, &sample); err != nil || sample.ID == "" ||
			sample.ObservedAt <= 0 || sample.OrderMS <= 0 || sample.TTFTMs < 0 || sample.LatencyMs < 0 ||
			(sample.Source != "real" && sample.Source != "probe") || realOnly && sample.Source != "real" ||
			(sample.Status != "healthy" && sample.Status != "degraded" && sample.Status != "error") {
			return nil, errors.New("invalid channel status observation")
		}
		samples = append(samples, sample)
	}
	return samples, nil
}
