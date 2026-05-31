package management

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Quota exceeded toggles
func (h *Handler) GetSwitchProject(c *gin.Context) {
	c.JSON(200, gin.H{"switch-project": h.cfg.QuotaExceeded.SwitchProject})
}
func (h *Handler) PutSwitchProject(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.QuotaExceeded.SwitchProject = v })
}

func (h *Handler) GetSwitchPreviewModel(c *gin.Context) {
	c.JSON(200, gin.H{"switch-preview-model": h.cfg.QuotaExceeded.SwitchPreviewModel})
}
func (h *Handler) PutSwitchPreviewModel(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.QuotaExceeded.SwitchPreviewModel = v })
}

func (h *Handler) GetCodexFiveHourReservePercent(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"codex-five-hour-reserve-percent": h.cfg.QuotaExceeded.CodexFiveHourReservePercent})
}

func (h *Handler) PutCodexFiveHourReservePercent(c *gin.Context) {
	var body struct {
		Value *int `json:"value"`
	}
	if errBindJSON := c.ShouldBindJSON(&body); errBindJSON != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	value := clampManagementPercent(*body.Value)
	h.cfg.QuotaExceeded.CodexFiveHourReservePercent = value
	if h.persist(c) && h.authManager != nil {
		h.authManager.SetConfig(h.cfg)
	}
}

func clampManagementPercent(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
