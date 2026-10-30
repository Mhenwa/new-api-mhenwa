package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	channelstatus "github.com/QuantumNous/new-api/pkg/channel_status"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelStatusHistoryAndEffectiveRatio(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	db := modelManagementDB(t, "sqlite", "")
	previousRatios := ratio_setting.GroupRatio2JSONString()
	previousSpecialRatios := ratio_setting.GroupGroupRatio2JSONString()
	previousUsableGroups := setting.UserUsableGroups2JSONString()
	previousSpecialGroups := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"workbuddy":0.035,"hidden":8}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"visible-model":7}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"workbuddy":"Workbuddy"}`))
	ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Clear()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(previousSpecialRatios))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsableGroups))
		ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.AddAll(previousSpecialGroups)
	})
	owner := &model.User{Username: "status-viewer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: "status-viewer"}
	require.NoError(t, db.Create(owner).Error)
	for _, ability := range []model.Ability{
		{Group: "workbuddy", Model: "visible-model", ChannelId: 1, Enabled: true},
		{Group: "hidden", Model: "hidden-model", ChannelId: 2, Enabled: true},
		{Group: "workbuddy", Model: "disabled-model", ChannelId: 1, Enabled: false},
	} {
		require.NoError(t, db.Create(&ability).Error)
	}
	targets := []channelstatus.Target{
		{Group: "workbuddy", Model: "visible-model", ProbeTokenID: 1},
		{Group: "hidden", Model: "hidden-model", ProbeTokenID: 2},
		{Group: "workbuddy", Model: "disabled-model", ProbeTokenID: 3},
	}
	encoded, err := common.Marshal(targets)
	require.NoError(t, err)
	t.Setenv(channelstatus.TargetsEnv, string(encoded))
	server := miniredis.RunT(t)
	previousRedis, previousClient := common.RedisEnabled, common.RDB
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		common.RedisEnabled, common.RDB = previousRedis, previousClient
	})
	request := func(query string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(response)
		context.Request = httptest.NewRequest(http.MethodGet, "/api/channel-status"+query, nil)
		context.Set("id", owner.Id)
		GetChannelStatus(context)
		return response
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			HistoryWindow string                        `json:"history_window"`
			Targets       []channelStatusTargetResponse `json:"targets"`
		} `json:"data"`
	}
	t.Run("global effective ratio and visible targets", func(t *testing.T) {
		response := request("")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
		require.True(t, result.Success)
		require.Len(t, result.Data.Targets, 1)
		assert.Equal(t, "workbuddy", result.Data.Targets[0].Group)
		assert.Equal(t, "visible-model", result.Data.Targets[0].Model)
		assert.Equal(t, 0.035, result.Data.Targets[0].EffectiveRatio)
		assert.Equal(t, "1h", result.Data.HistoryWindow)
		assert.NotContains(t, response.Body.String(), "hidden-model")
		assert.NotContains(t, response.Body.String(), "disabled-model")
	})
	for _, ratio := range []float64{0.2, 0} {
		t.Run(fmt.Sprintf("user group override %g replaces global ratio", ratio), func(t *testing.T) {
			require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(fmt.Sprintf(`{"default":{"workbuddy":%g}}`, ratio)))
			t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`)) })
			response := request("")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.Len(t, result.Data.Targets, 1)
			assert.Equal(t, ratio, result.Data.Targets[0].EffectiveRatio)
			assert.Contains(t, response.Body.String(), fmt.Sprintf(`"effective_ratio":%g`, ratio), "zero must remain an explicit response value")
		})
	}
	for _, window := range []struct {
		query, name string
		seconds     int
		buckets     int
	}{{"", "1h", 3600, 60}, {"?history_window=30m", "30m", 1800, 30}, {"?history_window=1h", "1h", 3600, 60}, {"?history_window=6h", "6h", 21600, 60}, {"?history_window=24h", "24h", 86400, 60}} {
		t.Run("history window "+window.name+window.query, func(t *testing.T) {
			response := request(window.query)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.Len(t, result.Data.Targets, 1)
			assert.Equal(t, window.name, result.Data.HistoryWindow)
			history := result.Data.Targets[0].History
			assert.EqualValues(t, window.seconds, history.WindowSeconds)
			assert.EqualValues(t, window.buckets, history.TotalBucketCount)
			assert.Len(t, history.Buckets, window.buckets)
			assert.EqualValues(t, window.seconds, history.EndAt-history.StartAt)
			for _, bucket := range history.Buckets {
				assert.Equal(t, "no_data", bucket.Status)
			}
		})
	}
	for _, query := range []string{"?history_window=", "?history_window=2h", "?history_window=1H", "?history_window=garbage"} {
		t.Run("invalid window "+query, func(t *testing.T) {
			response := request(query)
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.NotContains(t, response.Body.String(), "targets")
		})
	}
	t.Run("observed history is returned only for the visible route", func(t *testing.T) {
		observed := time.Now().Add(-time.Minute)
		require.NoError(t, channelstatus.RecordProbeSample("workbuddy", "visible-model", channelstatus.Sample{
			Status: "healthy", ObservedAt: observed.Unix(), OrderMS: observed.UnixMilli(), TTFTMs: 100,
		}))
		require.NoError(t, channelstatus.RecordProbeSample("workbuddy", "visible-model", channelstatus.Sample{
			Status: "error", ObservedAt: observed.Add(time.Second).Unix(), OrderMS: observed.Add(time.Second).UnixMilli(),
		}))
		require.NoError(t, channelstatus.RecordProbeSample("hidden", "hidden-model", channelstatus.Sample{
			Status: "healthy", ObservedAt: observed.Unix(), OrderMS: observed.UnixMilli(),
		}))
		response := request("?history_window=30m")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
		require.Len(t, result.Data.Targets, 1)
		history := result.Data.Targets[0].History
		assert.EqualValues(t, 2, history.SampleCount)
		assert.EqualValues(t, 1, history.SuccessCount)
		assert.EqualValues(t, 1, history.ErrorCount)
		require.NotNil(t, history.SuccessRate)
		assert.Equal(t, float64(50), *history.SuccessRate)
		assert.NotContains(t, response.Body.String(), "hidden-model")
	})
}

func TestChannelStatusMonitorDatabaseMatrix(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	for _, dialect := range []struct{ kind, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}} {
		t.Run(dialect.kind, func(t *testing.T) {
			if dialect.env != "" && os.Getenv(dialect.env) == "" {
				t.Skip("set " + dialect.env + " to run this database")
			}
			db := modelManagementDB(t, dialect.kind, os.Getenv(dialect.env))
			require.NoError(t, db.AutoMigrate(&model.Token{}, &model.UserSession{}, &model.UserAccessToken{}))
			previousRatios := ratio_setting.GroupRatio2JSONString()
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"auto":1}`))
			t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios)) })
			t.Setenv(channelstatus.TargetsEnv, "")

			owner := &model.User{Username: "monitor-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: "monitor-owner"}
			admin := &model.User{Username: "monitor-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: "monitor-admin"}
			disabled := &model.User{Username: "monitor-disabled", Role: common.RoleCommonUser, Status: common.UserStatusDisabled, Group: "default", AuthVersion: 1, AffCode: "monitor-disabled"}
			for _, user := range []*model.User{owner, admin, disabled} {
				require.NoError(t, db.Create(user).Error)
			}
			require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "monitor-model", ChannelId: 1, Enabled: true}).Error)
			require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "disabled-model", ChannelId: 1, Enabled: false}).Error)
			loopback := "127.0.0.1"
			valid := &model.Token{UserId: owner.Id, Key: "monitor-secret-never-returned", Status: common.TokenStatusEnabled, Name: " Probe token ", ExpiredTime: -1, RemainQuota: 100, Group: "default", ModelLimitsEnabled: true, ModelLimits: "monitor-model", AllowIps: &loopback}
			require.NoError(t, db.Create(valid).Error)

			request := func(method, body string) *httptest.ResponseRecorder {
				response := httptest.NewRecorder()
				context, _ := gin.CreateTestContext(response)
				context.Request = httptest.NewRequest(method, "/api/admin/channel-status/targets", strings.NewReader(body))
				if method == http.MethodGet {
					GetChannelStatusMonitor(context)
				} else {
					UpdateChannelStatusMonitor(context)
				}
				return response
			}
			var result struct {
				Success bool `json:"success"`
				Data    struct {
					Targets       []channelstatus.Target    `json:"targets"`
					Source        string                    `json:"source"`
					Groups        []string                  `json:"groups"`
					ModelsByGroup map[string][]string       `json:"models_by_group"`
					ProbeTokens   []channelStatusProbeToken `json:"probe_tokens"`
				} `json:"data"`
			}
			response := request(http.MethodGet, "")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			assert.True(t, result.Success)
			assert.NotNil(t, result.Data.Targets)
			assert.Empty(t, result.Data.Targets)
			assert.Equal(t, "none", result.Data.Source)
			assert.Equal(t, []string{"default"}, result.Data.Groups)
			assert.Equal(t, []string{"monitor-model"}, result.Data.ModelsByGroup["default"])
			require.Len(t, result.Data.ProbeTokens, 1)
			assert.Equal(t, "Probe token", result.Data.ProbeTokens[0].Name)
			assert.NotContains(t, response.Body.String(), valid.Key)
			assert.NotContains(t, response.Body.String(), `"key"`)

			body := fmt.Sprintf(`{"targets":[{"group":" default ","model":" monitor-model ","probe_token_id":%d}]}`, valid.Id)
			response = request(http.MethodPut, body)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())
			assert.Equal(t, "database", result.Data.Source)
			require.Len(t, result.Data.Targets, 1)
			assert.Equal(t, channelstatus.Target{Group: "default", Model: "monitor-model", ProbeTokenID: valid.Id, IntervalSeconds: 300, TimeoutSeconds: 30}, result.Data.Targets[0])
			assert.True(t, channelstatus.IsConfigured(), "database-only settings enable real request observation")
			var stored model.Option
			require.NoError(t, db.Where(&model.Option{Key: channelstatus.TargetsOption}).First(&stored).Error)
			original := stored.Value

			for _, invalid := range []string{
				`{}`, `null`, `{"targets":null}`, `{"targets":{}}`, `{"targets":[]} {"targets":[]}`, `{"targets":[]} trailing`,
				`{"targets":[{}]}`, `{"targets":[{"group":"auto","model":"monitor-model","probe_token_id":1}]}`,
				fmt.Sprintf(`{"targets":[{"group":"default","model":"disabled-model","probe_token_id":%d}]}`, valid.Id),
				fmt.Sprintf(`{"targets":[{"group":"default","model":"monitor-model","probe_token_id":%d,"interval_seconds":59}]}`, valid.Id),
				`{"targets":[{"group":"default","model":"monitor-model","probe_token_id":999999}]}`,
			} {
				response = request(http.MethodPut, invalid)
				assert.Equal(t, http.StatusBadRequest, response.Code, invalid)
				require.NoError(t, db.Where(&model.Option{Key: channelstatus.TargetsOption}).First(&stored).Error)
				assert.Equal(t, original, stored.Value, "rejected request must not write the database")
				common.OptionMapRWMutex.RLock()
				assert.Equal(t, original, common.OptionMap[channelstatus.TargetsOption], "rejected request must not change the local config")
				common.OptionMapRWMutex.RUnlock()
			}
			response = request(http.MethodPut, `{"targets":[],"padding":"`+strings.Repeat("x", 1024*1024)+`"}`)
			assert.Equal(t, http.StatusBadRequest, response.Code, "oversized input must not erase the existing settings")
			require.NoError(t, db.Where(&model.Option{Key: channelstatus.TargetsOption}).First(&stored).Error)
			assert.Equal(t, original, stored.Value)
			common.OptionMapRWMutex.Lock()
			common.OptionMap[channelstatus.TargetsOption] = "invalid-json"
			common.OptionMapRWMutex.Unlock()
			response = request(http.MethodGet, "")
			assert.Equal(t, http.StatusServiceUnavailable, response.Code)
			require.NoError(t, db.Where(&model.Option{Key: channelstatus.TargetsOption}).First(&stored).Error)
			assert.Equal(t, original, stored.Value, "reading invalid cached config must not modify persistent settings")
			common.OptionMapRWMutex.Lock()
			common.OptionMap[channelstatus.TargetsOption] = original
			common.OptionMapRWMutex.Unlock()

			for i, invalid := range []struct {
				name   string
				mutate func(*model.Token)
			}{
				{"disabled token", func(token *model.Token) { token.Status = common.TokenStatusDisabled }},
				{"unlimited", func(token *model.Token) { token.UnlimitedQuota = true }},
				{"exhausted", func(token *model.Token) { token.RemainQuota = 0 }},
				{"expired", func(token *model.Token) { token.ExpiredTime = 1 }},
				{"admin owner", func(token *model.Token) { token.UserId = admin.Id }},
				{"disabled owner", func(token *model.Token) { token.UserId = disabled.Id }},
				{"wrong group", func(token *model.Token) { token.Group = "auto" }},
				{"no model restriction", func(token *model.Token) { token.ModelLimitsEnabled = false }},
				{"wrong model", func(token *model.Token) { token.ModelLimits = "other-model" }},
				{"no IP restriction", func(token *model.Token) { token.AllowIps = nil }},
				{"public IP range", func(token *model.Token) { value := "0.0.0.0/0"; token.AllowIps = &value }},
			} {
				t.Run(invalid.name, func(t *testing.T) {
					token := *valid
					token.Id, token.Key = 0, fmt.Sprintf("invalid-probe-secret-%d", i)
					invalid.mutate(&token)
					require.NoError(t, db.Create(&token).Error)
					response := request(http.MethodPut, fmt.Sprintf(`{"targets":[{"group":"default","model":"monitor-model","probe_token_id":%d}]}`, token.Id))
					assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
					require.NoError(t, db.Where(&model.Option{Key: channelstatus.TargetsOption}).First(&stored).Error)
					assert.Equal(t, original, stored.Value)
					assert.NotContains(t, response.Body.String(), token.Key)
				})
			}
			retiredOwner := &model.User{Username: "retired-group-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "retired", AffCode: "retired-group-owner"}
			require.NoError(t, db.Create(retiredOwner).Error)
			retiredToken := *valid
			retiredToken.Id, retiredToken.Key, retiredToken.Group, retiredToken.UserId = 0, "retired-group-probe-secret", "retired", retiredOwner.Id
			require.NoError(t, db.Create(&retiredToken).Error)
			require.NoError(t, db.Create(&model.Ability{Group: "retired", Model: "monitor-model", ChannelId: 1, Enabled: true}).Error)
			response = request(http.MethodPut, fmt.Sprintf(`{"targets":[{"group":"retired","model":"monitor-model","probe_token_id":%d}]}`, retiredToken.Id))
			assert.Equal(t, http.StatusBadRequest, response.Code, "probe group must also pass the normal token admission rules")
			require.NoError(t, db.Where(&model.Option{Key: channelstatus.TargetsOption}).First(&stored).Error)
			assert.Equal(t, original, stored.Value)
			response = request(http.MethodGet, "")
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			for _, token := range result.Data.ProbeTokens {
				assert.NotEqual(t, retiredToken.Id, token.Id)
			}

			// Reload through the same database path used by the option synchronizer,
			// with fresh node-local state. Repeat to verify idempotent hot updates.
			t.Setenv(channelstatus.TargetsEnv, `[{"group":"environment","model":"environment-model","probe_token_id":999}]`)
			for range 2 {
				common.OptionMapRWMutex.Lock()
				common.OptionMap = map[string]string{}
				common.OptionMapRWMutex.Unlock()
				model.InitOptionMap()
				targets, source, err := channelstatus.LoadTargetsWithSource()
				require.NoError(t, err)
				assert.Equal(t, "database", source)
				require.Len(t, targets, 1)
				assert.Equal(t, "monitor-model", targets[0].Model)
				assert.True(t, channelstatus.IsConfigured())
			}

			router := gin.New()
			router.GET("/api/admin/channel-status/targets", middleware.AdminAuth(), GetChannelStatusMonitor)
			router.PUT("/api/admin/channel-status/targets", middleware.AdminAuth(), UpdateChannelStatusMonitor)
			_, ownerJWT := createAccessTokenTestSession(t, owner.Id, "monitor-owner-session")
			readOnly, _ := createScopedAccessToken(t, admin.Id, time.Now().Add(time.Hour).Unix(), "option:read")
			writer, _ := createScopedAccessToken(t, admin.Id, time.Now().Add(time.Hour).Unix(), "option:write")
			for _, method := range []string{http.MethodGet, http.MethodPut} {
				response = accessTokenRequest(router, method, "/api/admin/channel-status/targets", "", "", `{"targets":[]}`)
				assert.Equal(t, http.StatusUnauthorized, response.Code)
				response = accessTokenRequest(router, method, "/api/admin/channel-status/targets", ownerJWT, "", `{"targets":[]}`)
				assert.Equal(t, http.StatusForbidden, response.Code)
			}
			response = accessTokenRequest(router, http.MethodGet, "/api/admin/channel-status/targets", readOnly, "", "")
			assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), valid.Key)
			response = accessTokenRequest(router, http.MethodPut, "/api/admin/channel-status/targets", readOnly, "", `{"targets":[]}`)
			assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
			require.NoError(t, db.Where(&model.Option{Key: channelstatus.TargetsOption}).First(&stored).Error)
			assert.Equal(t, original, stored.Value, "insufficient privileges cannot change config")

			response = accessTokenRequest(router, http.MethodPut, "/api/admin/channel-status/targets", writer, "", `{"targets":[]}`)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())
			assert.NotNil(t, result.Data.Targets)
			assert.Empty(t, result.Data.Targets)
			assert.Equal(t, "database", result.Data.Source)
			require.NoError(t, db.Where(&model.Option{Key: channelstatus.TargetsOption}).First(&stored).Error)
			assert.Equal(t, "[]", stored.Value)
			model.InitOptionMap()
			assert.False(t, channelstatus.IsConfigured(), "explicit empty list remains disabled after another node reloads")
		})
	}
}
