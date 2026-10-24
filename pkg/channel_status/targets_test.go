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
}
