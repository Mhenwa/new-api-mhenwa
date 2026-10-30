package channelstatus

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/go-redis/redis/v8"
)

const maxProbeOutputTokens = 8

var errProbeCanceledByReal = errors.New("probe canceled by real request")

var releaseProbeLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1], KEYS[2])
end
return 0
`)

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func ProbeAll(ctx context.Context) error {
	targets, err := LoadTargets()
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	if !RedisAvailable() {
		return errors.New("channel status requires Redis")
	}
	if err := common.RDB.Ping(ctx).Err(); err != nil {
		return errors.New("channel status Redis check failed")
	}

	var probeErrors []error
	for _, target := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := probeIfDue(ctx, target, time.Now()); err != nil {
			probeErrors = append(probeErrors, fmt.Errorf("%s/%s: %w", target.Group, target.Model, err))
		}
	}
	return errors.Join(probeErrors...)
}

func probeIfDue(ctx context.Context, target Target, now time.Time) error {
	due, err := targetNeedsProbe(ctx, target, now)
	if err != nil || !due {
		return err
	}
	current, err := LoadTargets()
	if err != nil {
		return err
	}
	configured, exists := findTarget(current, target.Group, target.Model)
	if !exists || configured != target {
		return nil
	}

	lockKey := redisKey(target.Group, target.Model, "probe-lock")
	lockValue, err := newProbeLockValue()
	if err != nil {
		return err
	}
	locked, err := common.RDB.SetNX(ctx, lockKey, lockValue, time.Duration(target.TimeoutSeconds+30)*time.Second).Result()
	if err != nil || !locked {
		return err
	}
	cancelKey := redisKey(target.Group, target.Model, "probe-cancel")
	defer releaseProbeLock(lockKey, cancelKey, lockValue)

	// A different master or a real request may have completed while the lease
	// was acquired. Recheck the same idle deadline under the distributed lease.
	due, err = targetNeedsProbe(ctx, target, time.Now())
	if err != nil || !due {
		return err
	}

	probeStarted := time.Now()
	sample, probeErr := runProbe(ctx, target, probeStarted, lockValue)
	if errors.Is(probeErr, errProbeCanceledByReal) {
		return nil
	}
	if probeErr != nil {
		sample.Status = "error"
	}
	if sample.ObservedAt == 0 {
		sample.ObservedAt = time.Now().Unix()
	}
	if sample.OrderMS == 0 {
		sample.OrderMS = time.Now().UnixMilli()
	}
	if sample.LatencyMs == 0 {
		sample.LatencyMs = time.Since(probeStarted).Milliseconds()
	}
	latestRealStart, err := lastRealStartAt(ctx, target.Group, target.Model)
	if err != nil {
		return err
	}
	if latestRealStart >= probeStarted.UnixMilli() {
		return nil
	}
	if err := RecordProbeSample(target.Group, target.Model, sample); err != nil {
		// Keep a bounded retry cooldown even when recording is unavailable.
		_ = setLastCheck(ctx, target.Group, target.Model, time.Now())
		return err
	}
	if err := setLastCheck(ctx, target.Group, target.Model, time.Now()); err != nil {
		return err
	}
	return probeErr
}

func runProbe(parent context.Context, target Target, started time.Time, lockValue string) (Sample, error) {
	sample := Sample{ID: lockValue, Source: "probe", StartedAt: started.Unix()}
	token, err := model.GetTokenById(target.ProbeTokenID)
	if err != nil || token == nil {
		return sample, errors.New("probe token is unavailable")
	}
	if err := ValidateProbeToken(token, target); err != nil {
		return sample, err
	}
	latestRealStart, err := lastRealStartAt(parent, target.Group, target.Model)
	if err != nil {
		return sample, err
	}
	if latestRealStart >= started.UnixMilli() {
		return sample, errors.New("real request started before probe")
	}

	port := os.Getenv("PORT")
	if port == "" {
		if common.Port == nil || *common.Port <= 0 {
			return sample, errors.New("local API port is unavailable")
		}
		port = strconv.Itoa(*common.Port)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return sample, errors.New("local API port is invalid")
	}
	endpoint := (&url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", port), Path: "/v1/chat/completions"}).String()
	body, err := common.Marshal(map[string]any{
		"model":          target.Model,
		"messages":       []map[string]string{{"role": "user", "content": "Reply with one short word: OK"}},
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
		"max_tokens":     maxProbeOutputTokens,
	})
	if err != nil {
		return sample, err
	}
	requestCtx, cancel := context.WithTimeout(parent, time.Duration(target.TimeoutSeconds)*time.Second)
	defer cancel()
	stopCancellationPoll := make(chan struct{})
	go pollProbeCancellation(requestCtx, cancel, redisKey(target.Group, target.Model, "probe-cancel"), lockValue, stopCancellationPoll)
	defer close(stopCancellationPoll)
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return sample, err
	}
	request.Header.Set("Authorization", "Bearer "+token.Key)
	request.Header.Set("Content-Type", "application/json")
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return sample, errors.New("default HTTP transport is unavailable")
	}
	transport = transport.Clone()
	transport.Proxy = nil
	client := &http.Client{
		Timeout:   time.Duration(target.TimeoutSeconds) * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		sample.ObservedAt = time.Now().Unix()
		sample.OrderMS = time.Now().UnixMilli()
		sample.LatencyMs = time.Since(started).Milliseconds()
		if requestCtx.Err() == context.Canceled {
			cancelCheckCtx, cancelCheck := context.WithTimeout(context.Background(), 100*time.Millisecond)
			cancelValue, cancelErr := common.RDB.Get(cancelCheckCtx, redisKey(target.Group, target.Model, "probe-cancel")).Result()
			cancelCheck()
			if cancelErr == nil && cancelValue == lockValue {
				return sample, errProbeCanceledByReal
			}
		}
		return sample, errors.New("probe request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		sample.ObservedAt = time.Now().Unix()
		sample.OrderMS = time.Now().UnixMilli()
		sample.LatencyMs = time.Since(started).Milliseconds()
		return sample, fmt.Errorf("probe returned HTTP %d", response.StatusCode)
	}

	measurement, err := readProbeStream(response.Body)
	finished := time.Now()
	sample.ObservedAt = finished.Unix()
	sample.OrderMS = finished.UnixMilli()
	sample.LatencyMs = finished.Sub(started).Milliseconds()
	sample.OutputTokens = &measurement.completionTokens
	if err != nil {
		return sample, err
	}
	sample.Status = "healthy"
	firstOutputAt, _, _ := measurement.outputWindow.Snapshot()
	sample.TTFTMs = firstOutputAt.Sub(started).Milliseconds()
	sample.TPS = observedOutputSpeed(measurement.outputWindow, measurement.completionTokens)
	sample.OutputRateVersion = outputRateVersion
	return sample, nil
}

type probeStreamMeasurement struct {
	firstContentAt   time.Time
	completionTokens int64
	outputWindow     *streamOutputWindow
}

func readProbeStream(body io.Reader) (probeStreamMeasurement, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	measurement := probeStreamMeasurement{outputWindow: &streamOutputWindow{}}
	sawTerminal := false
	malformedChunk := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			sawTerminal = true
			break
		}
		var chunk streamChunk
		if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
			malformedChunk = true
			continue
		}
		at := time.Now()
		measurement.outputWindow.Observe([]byte(data), at)
		measurement.completionTokens = max(measurement.completionTokens, chunk.Usage.CompletionTokens)
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" && measurement.firstContentAt.IsZero() {
				measurement.firstContentAt = at
			}
			if choice.FinishReason != nil {
				sawTerminal = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return measurement, errors.New("probe stream read failed")
	}
	if !sawTerminal || malformedChunk {
		return measurement, errors.New("probe stream ended without a valid terminal event")
	}
	if measurement.firstContentAt.IsZero() {
		return measurement, errors.New("probe completed without text output")
	}
	return measurement, nil
}

func newProbeLockValue() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func pollProbeCancellation(ctx context.Context, cancel context.CancelFunc, key, expectedValue string, stop <-chan struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			probeCtx, probeCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			value, err := common.RDB.Get(probeCtx, key).Result()
			probeCancel()
			if err == nil && value == expectedValue {
				cancel()
				return
			}
		}
	}
}

func releaseProbeLock(key, cancelKey, value string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := releaseProbeLockScript.Run(ctx, common.RDB, []string{key, cancelKey}, value).Err(); err != nil {
		common.SysLog(common.LogText("channel status probe lock cleanup failed: %v", err))
	}
}

// ValidateProbeToken checks the least-privilege requirements for a monitor
// token. It is exported so the administrator settings API can reject invalid
// tokens before persisting a target that could never run.
func ValidateProbeToken(token *model.Token, target Target) error {
	if token == nil || token.UserId <= 0 || token.Status != common.TokenStatusEnabled {
		return errors.New("probe token must belong to an enabled non-admin user")
	}
	if token.ExpiredTime != -1 && token.ExpiredTime <= common.GetTimestamp() {
		return errors.New("probe token is expired")
	}
	if token.UnlimitedQuota || token.RemainQuota <= 0 {
		return errors.New("probe token must have a finite positive quota")
	}
	if token.Group != target.Group {
		return errors.New("probe token must be fixed to its target group")
	}
	if !token.ModelLimitsEnabled || !token.GetModelLimitsMap()[target.Model] {
		return errors.New("probe token must be limited to its target model")
	}
	if token.AllowIps == nil || len(token.GetIpLimits()) != 1 {
		return errors.New("probe token must be restricted to loopback only")
	}
	allowedIP := strings.TrimSpace(token.GetIpLimits()[0])
	if allowedIP != "127.0.0.1" && allowedIP != "127.0.0.1/32" {
		return errors.New("probe token must be restricted to loopback only")
	}
	user, err := model.GetUserById(token.UserId, false)
	if err != nil || user == nil || user.Status != common.UserStatusEnabled {
		return errors.New("probe token user is unavailable")
	}
	if user.Role >= common.RoleAdminUser {
		return errors.New("probe token must belong to an enabled non-admin user")
	}
	if !service.IsUserSelectableGroup(user.Group, target.Group) {
		return errors.New("probe token user cannot use the target group")
	}
	return nil
}
