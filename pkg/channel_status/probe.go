package channelstatus

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
	lastCheck, err := lastCheckAt(ctx, target.Group, target.Model)
	if err != nil {
		return err
	}
	interval := time.Duration(target.IntervalSeconds) * time.Second
	if lastCheck > 0 && now.Unix()-lastCheck < int64(interval.Seconds()) {
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

	lastCheck, err = lastCheckAt(ctx, target.Group, target.Model)
	if err != nil {
		return err
	}
	if lastCheck > 0 && now.Unix()-lastCheck < int64(interval.Seconds()) {
		return nil
	}
	latestRealStart, err := lastRealStartAt(ctx, target.Group, target.Model)
	if err != nil {
		return err
	}
	activeKey := redisKey(target.Group, target.Model, "real-active")
	err = common.RDB.ZRemRangeByScore(ctx, activeKey, "-inf", strconv.FormatInt(now.Add(-sampleTTL).UnixMilli(), 10)).Err()
	if err != nil {
		return err
	}
	activeCount, err := common.RDB.ZCard(ctx, activeKey).Result()
	if err != nil {
		return err
	}
	if activeCount > 0 || latestRealStart > 0 && now.UnixMilli()-latestRealStart < interval.Milliseconds() {
		return setLastCheck(ctx, target.Group, target.Model, now)
	}

	probeStarted := time.Now()
	sample, probeErr := runProbe(ctx, target, probeStarted, lockValue)
	if errors.Is(probeErr, errProbeCanceledByReal) {
		return setLastCheck(ctx, target.Group, target.Model, time.Now())
	}
	if probeErr != nil {
		sample.Status = "error"
	}
	if sample.ObservedAt == 0 {
		sample.ObservedAt = time.Now().Unix()
	}
	if sample.LatencyMs == 0 {
		sample.LatencyMs = time.Since(probeStarted).Milliseconds()
	}
	latestRealStart, err = lastRealStartAt(ctx, target.Group, target.Model)
	if err != nil {
		return err
	}
	if latestRealStart < probeStarted.UnixMilli() {
		if err := RecordProbeSample(target.Group, target.Model, sample); err != nil {
			return err
		}
	}
	if err := setLastCheck(ctx, target.Group, target.Model, time.Now()); err != nil {
		return err
	}
	return probeErr
}

func runProbe(parent context.Context, target Target, started time.Time, lockValue string) (Sample, error) {
	sample := Sample{Source: "probe", StartedAt: started.Unix()}
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
		sample.LatencyMs = time.Since(started).Milliseconds()
		return sample, fmt.Errorf("probe returned HTTP %d", response.StatusCode)
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	firstContentAt := time.Time{}
	completionTokens := int64(0)
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
		completionTokens = max(completionTokens, chunk.Usage.CompletionTokens)
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" && firstContentAt.IsZero() {
				firstContentAt = time.Now()
			}
			if choice.FinishReason != nil {
				sawTerminal = true
			}
		}
		if sawTerminal {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		sample.ObservedAt = time.Now().Unix()
		sample.LatencyMs = time.Since(started).Milliseconds()
		return sample, errors.New("probe stream read failed")
	}
	if !sawTerminal || malformedChunk {
		sample.ObservedAt = time.Now().Unix()
		sample.LatencyMs = time.Since(started).Milliseconds()
		return sample, errors.New("probe stream ended without a valid terminal event")
	}
	finished := time.Now()
	sample.ObservedAt = finished.Unix()
	sample.LatencyMs = finished.Sub(started).Milliseconds()
	if firstContentAt.IsZero() {
		return sample, errors.New("probe completed without text output")
	}
	sample.Status = "healthy"
	sample.TTFTMs = firstContentAt.Sub(started).Milliseconds()
	if completionTokens > 0 && finished.After(firstContentAt) {
		sample.TPS = float64(completionTokens) / finished.Sub(firstContentAt).Seconds()
	}
	return sample, nil
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
	if token.UserId <= 0 || token.Status != common.TokenStatusEnabled || model.IsAdmin(token.UserId) {
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
	user, err := model.GetUserCache(token.UserId)
	if err != nil || user == nil || user.Status != common.UserStatusEnabled {
		return errors.New("probe token user is unavailable")
	}
	if !service.GroupInUserUsableGroups(user.Group, target.Group) {
		return errors.New("probe token user cannot use the target group")
	}
	return nil
}
