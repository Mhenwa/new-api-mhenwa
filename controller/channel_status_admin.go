package controller

import (
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	channelstatus "github.com/QuantumNous/new-api/pkg/channel_status"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

type channelStatusMonitorRequest struct {
	Targets []channelstatus.Target `json:"targets"`
}

type channelStatusProbeToken struct {
	Id     int      `json:"id"`
	Name   string   `json:"name"`
	UserId int      `json:"user_id"`
	Group  string   `json:"group"`
	Models []string `json:"models"`
}

// GetChannelStatusMonitor returns the effective monitor settings plus the
// concrete groups, models, and least-privilege tokens an administrator may
// select. Token secrets are deliberately never included.
func GetChannelStatusMonitor(c *gin.Context) {
	targets, source, err := channelstatus.LoadTargetsWithSource()
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage(err.Error()))
		return
	}
	metadata, err := channelStatusMonitorMetadata()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"targets":         targets,
			"source":          source,
			"groups":          metadata.Groups,
			"models_by_group": metadata.ModelsByGroup,
			"probe_tokens":    metadata.ProbeTokens,
		},
	})
}

// UpdateChannelStatusMonitor persists the complete target list in the normal
// Option table. model.UpdateOptionsBulk updates the local option map immediately;
// the existing option synchronizer propagates it to the other cluster node,
// so no container restart is required.
func UpdateChannelStatusMonitor(c *gin.Context) {
	var request channelStatusMonitorRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	if err := channelstatus.ValidateTargets(request.Targets); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage(err.Error()))
		return
	}
	for _, target := range request.Targets {
		models := model.GetGroupEnabledModels(target.Group)
		if !slices.Contains(models, target.Model) {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("The selected model is not enabled in the selected group"))
			return
		}
		token, err := model.GetTokenById(target.ProbeTokenID)
		if err != nil || token == nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("The selected probe token is unavailable"))
			return
		}
		if err := channelstatus.ValidateProbeToken(token, target); err != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage(err.Error()))
			return
		}
	}
	encoded, err := common.Marshal(request.Targets)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOptionsBulk(map[string]string{channelstatus.TargetsOption: string(encoded)}); err != nil {
		common.ApiError(c, err)
		return
	}
	GetChannelStatusMonitor(c)
}

type channelStatusMonitorMetadataResult struct {
	Groups        []string
	ModelsByGroup map[string][]string
	ProbeTokens   []channelStatusProbeToken
}

func channelStatusMonitorMetadata() (channelStatusMonitorMetadataResult, error) {
	groups := make([]string, 0)
	modelsByGroup := make(map[string][]string)
	for group := range ratio_setting.GetGroupRatioCopy() {
		if group == "auto" {
			continue
		}
		models := model.GetGroupEnabledModels(group)
		if len(models) == 0 {
			continue
		}
		slices.Sort(models)
		groups = append(groups, group)
		modelsByGroup[group] = models
	}
	slices.Sort(groups)

	var tokens []model.Token
	if err := model.DB.Where("status = ? AND unlimited_quota = ? AND remain_quota > ? AND model_limits_enabled = ?", common.TokenStatusEnabled, false, 0, true).Find(&tokens).Error; err != nil {
		return channelStatusMonitorMetadataResult{}, err
	}
	probeTokens := make([]channelStatusProbeToken, 0, len(tokens))
	for i := range tokens {
		token := &tokens[i]
		if token.UserId <= 0 || token.Group == "" || token.Group == "auto" || token.ExpiredTime != -1 && token.ExpiredTime <= common.GetTimestamp() {
			continue
		}
		user, err := model.GetUserById(token.UserId, false)
		if err != nil || user == nil || user.Status != common.UserStatusEnabled || model.IsAdmin(token.UserId) {
			continue
		}
		ips := token.GetIpLimits()
		if len(ips) != 1 || (ips[0] != "127.0.0.1" && ips[0] != "127.0.0.1/32") {
			continue
		}
		modelNames := make([]string, 0)
		for name, enabled := range token.GetModelLimitsMap() {
			if enabled {
				modelNames = append(modelNames, name)
			}
		}
		slices.Sort(modelNames)
		if len(modelNames) == 0 || !service.GroupInUserUsableGroups(user.Group, token.Group) {
			continue
		}
		probeTokens = append(probeTokens, channelStatusProbeToken{Id: token.Id, Name: strings.TrimSpace(token.Name), UserId: token.UserId, Group: token.Group, Models: modelNames})
	}
	slices.SortFunc(probeTokens, func(a, b channelStatusProbeToken) int {
		if a.Group != b.Group {
			return strings.Compare(a.Group, b.Group)
		}
		return a.Id - b.Id
	})
	return channelStatusMonitorMetadataResult{Groups: groups, ModelsByGroup: modelsByGroup, ProbeTokens: probeTokens}, nil
}
