package controller

import (
	"net/http"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	channelstatus "github.com/QuantumNous/new-api/pkg/channel_status"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetChannelStatus(c *gin.Context) {
	targets, err := channelstatus.LoadTargets()
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Service status is unavailable"))
		return
	}
	userGroup, err := model.GetUserGroup(c.GetInt("id"), false)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Failed to load user groups"))
		return
	}

	modelsByGroup := make(map[string][]string)
	statuses := make([]channelstatus.TargetStatus, 0, len(targets))
	updatedAt := int64(0)
	for _, target := range targets {
		if !service.IsUserSelectableGroup(userGroup, target.Group) {
			continue
		}
		models, exists := modelsByGroup[target.Group]
		if !exists {
			models = model.GetGroupEnabledModels(target.Group)
			modelsByGroup[target.Group] = models
		}
		if !slices.Contains(models, target.Model) {
			continue
		}
		status, err := channelstatus.GetTargetStatus(c.Request.Context(), target)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Service status data is unavailable"))
			return
		}
		statuses = append(statuses, status)
		updatedAt = max(updatedAt, status.ObservedAt, status.RequestStartedAt)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"updated_at": updatedAt,
			"targets":    statuses,
		},
	})
}
