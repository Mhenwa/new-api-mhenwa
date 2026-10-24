package channelstatus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const TargetsEnv = "CHANNEL_STATUS_MONITOR_TARGETS"

// TargetsOption is the database-backed override for the monitor target list.
// An explicitly persisted empty list disables probing and intentionally takes
// precedence over the environment fallback.
const TargetsOption = "channel_status.targets"

const (
	defaultIntervalSeconds = 300
	defaultTimeoutSeconds  = 30
	maxTargets             = 100
)

type Target struct {
	Group           string `json:"group"`
	Model           string `json:"model"`
	ProbeTokenID    int    `json:"probe_token_id"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
}

func LoadTargets() ([]Target, error) {
	targets, _, err := LoadTargetsWithSource()
	return targets, err
}

// LoadTargetsWithSource returns the effective target list and its source. The
// option map is refreshed by the normal New API option synchronizer, so an
// administrator update is picked up by all nodes without a process restart.
func LoadTargetsWithSource() ([]Target, string, error) {
	raw, source := configuredTargetsRaw()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, source, nil
	}

	var targets []Target
	if err := common.UnmarshalJsonStr(raw, &targets); err != nil {
		return nil, source, fmt.Errorf("invalid channel status target JSON")
	}
	if err := ValidateTargets(targets); err != nil {
		return nil, source, err
	}
	return targets, source, nil
}

func configuredTargetsRaw() (string, string) {
	common.OptionMapRWMutex.RLock()
	value, configured := common.OptionMap[TargetsOption]
	common.OptionMapRWMutex.RUnlock()
	if configured {
		return value, "database"
	}
	envValue := os.Getenv(TargetsEnv)
	if strings.TrimSpace(envValue) == "" {
		return "", "none"
	}
	return envValue, "environment"
}

func ValidateTargets(targets []Target) error {
	if len(targets) > maxTargets {
		return fmt.Errorf("%s supports at most %d targets", TargetsEnv, maxTargets)
	}
	seen := make(map[string]struct{}, len(targets))
	seenProbeTokens := make(map[int]struct{}, len(targets))
	for i := range targets {
		targets[i].Group = strings.TrimSpace(targets[i].Group)
		targets[i].Model = strings.TrimSpace(targets[i].Model)
		if targets[i].Group == "" || targets[i].Group == "auto" || targets[i].Model == "" {
			return errors.New("each channel status target requires a concrete group and model")
		}
		if targets[i].ProbeTokenID <= 0 {
			return errors.New("each channel status target requires a positive probe token ID")
		}
		if _, exists := seenProbeTokens[targets[i].ProbeTokenID]; exists {
			return errors.New("each channel status probe token ID must be unique")
		}
		seenProbeTokens[targets[i].ProbeTokenID] = struct{}{}
		if targets[i].IntervalSeconds == 0 {
			targets[i].IntervalSeconds = defaultIntervalSeconds
		}
		if targets[i].IntervalSeconds < 60 || targets[i].IntervalSeconds > 3600 {
			return errors.New("channel status probe interval must be between 60 and 3600 seconds")
		}
		if targets[i].TimeoutSeconds == 0 {
			targets[i].TimeoutSeconds = defaultTimeoutSeconds
		}
		if targets[i].TimeoutSeconds < 5 || targets[i].TimeoutSeconds > 120 {
			return errors.New("channel status probe timeout must be between 5 and 120 seconds")
		}
		key := targetKey(targets[i].Group, targets[i].Model)
		if _, exists := seen[key]; exists {
			return errors.New("channel status group/model targets must be unique")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func targetKey(group, model string) string {
	sum := sha256.Sum256([]byte(group + "\x00" + model))
	return hex.EncodeToString(sum[:16])
}

func findTarget(targets []Target, group, model string) (Target, bool) {
	for _, target := range targets {
		if target.Group == group && target.Model == model {
			return target, true
		}
	}
	return Target{}, false
}
