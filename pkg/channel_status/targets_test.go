package channelstatus

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadTargetsUsesDatabaseOptionOverEnvironment(t *testing.T) {
	previous := common.OptionMap
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{
		TargetsOption: `[{"group":"database-group","model":"db-model","probe_token_id":7}]`,
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
	t.Setenv(TargetsEnv, `[{"group":"environment-group","model":"env-model","probe_token_id":8}]`)

	targets, source, err := LoadTargetsWithSource()
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "database", source)
	assert.Equal(t, "database-group", targets[0].Group)
	assert.Equal(t, 300, targets[0].IntervalSeconds)
	assert.Equal(t, 30, targets[0].TimeoutSeconds)
	assert.True(t, IsConfigured())
}

func TestEmptyDatabaseOptionDisablesEnvironmentFallback(t *testing.T) {
	previous := common.OptionMap
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{TargetsOption: "[]"}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
	t.Setenv(TargetsEnv, `[{"group":"environment-group","model":"env-model","probe_token_id":8}]`)

	targets, source, err := LoadTargetsWithSource()
	require.NoError(t, err)
	assert.Empty(t, targets)
	assert.Equal(t, "database", source)
	assert.False(t, IsConfigured())
}

func TestEffectiveConfigurationControlsObservation(t *testing.T) {
	previous := common.OptionMap
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
	for _, test := range []struct {
		name        string
		options     map[string]string
		environment string
		configured  bool
	}{
		{"none", map[string]string{}, "", false},
		{"database only", map[string]string{TargetsOption: `[{"group":"default","model":"test","probe_token_id":1}]`}, "", true},
		{"environment fallback", map[string]string{}, `[{"group":"default","model":"test","probe_token_id":1}]`, true},
		{"empty environment", map[string]string{}, `[]`, false},
		{"invalid database blocks environment", map[string]string{TargetsOption: `invalid`}, `[{"group":"default","model":"test","probe_token_id":1}]`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			common.OptionMapRWMutex.Lock()
			common.OptionMap = test.options
			common.OptionMapRWMutex.Unlock()
			t.Setenv(TargetsEnv, test.environment)
			assert.Equal(t, test.configured, IsConfigured())
		})
	}
}

func TestValidateTargetsRejectsInvalidConfiguration(t *testing.T) {
	for _, test := range []struct {
		name string
		json string
	}{
		{"automatic group", `[{"group":"auto","model":"test","probe_token_id":1}]`},
		{"missing group", `[{"model":"test","probe_token_id":1}]`},
		{"missing model", `[{"group":"default","probe_token_id":1}]`},
		{"invalid token", `[{"group":"default","model":"test","probe_token_id":0}]`},
		{"short interval", `[{"group":"default","model":"test","probe_token_id":1,"interval_seconds":59}]`},
		{"long timeout", `[{"group":"default","model":"test","probe_token_id":1,"timeout_seconds":121}]`},
		{"duplicate target", `[{"group":"default","model":"test","probe_token_id":1},{"group":" default ","model":"test","probe_token_id":2}]`},
		{"reused token", `[{"group":"default","model":"test","probe_token_id":1},{"group":"default","model":"other","probe_token_id":1}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var targets []Target
			require.NoError(t, common.UnmarshalJsonStr(test.json, &targets))
			require.Error(t, ValidateTargets(targets))
		})
	}
}
