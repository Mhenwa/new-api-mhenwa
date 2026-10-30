package controller

import (
	"net/http"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	channelstatus "github.com/QuantumNous/new-api/pkg/channel_status"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type channelStatusTargetResponse struct {
	channelstatus.TargetStatus
	EffectiveRatio float64                     `json:"effective_ratio"`
	History        channelstatus.TargetHistory `json:"history"`
}

func GetChannelStatus(c *gin.Context) {
	historyWindow := c.DefaultQuery("history_window", "1h")
	windowSeconds := channelstatus.DefaultHistoryWindowSeconds
	switch historyWindow {
	case "30m":
		windowSeconds = 30 * 60
	case "1h":
	case "6h":
		windowSeconds = 6 * 60 * 60
	case "24h":
		windowSeconds = 24 * 60 * 60
	default:
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	asOf := time.Now()
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
	statuses := make([]channelStatusTargetResponse, 0, len(targets))
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
		history, err := channelstatus.GetTargetHistory(c.Request.Context(), target, windowSeconds, asOf)
		if err != nil {
			common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Service status data is unavailable"))
			return
		}
		statuses = append(statuses, channelStatusTargetResponse{
			TargetStatus:   status,
			EffectiveRatio: service.GetUserGroupRatio(userGroup, target.Group),
			History:        history,
		})
		updatedAt = max(updatedAt, status.ObservedAt, status.RequestStartedAt)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"updated_at":     updatedAt,
			"history_window": historyWindow,
			"targets":        statuses,
		},
	})
}
