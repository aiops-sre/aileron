package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/notifications"
)

// NotificationHandler handles notification endpoints
type NotificationHandler struct {
	svc *notifications.NotificationService
}

// NewNotificationHandler creates a new notification handler wired to the real service.
func NewNotificationHandler(db *sql.DB) *NotificationHandler {
	return &NotificationHandler{svc: notifications.NewNotificationService(db)}
}

// GetRecentNotifications returns recent notifications from notification_log.
func (h *NotificationHandler) GetRecentNotifications(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	filters := map[string]interface{}{"limit": limit}

	if alertID := c.Query("alert_id"); alertID != "" {
		if id, err := uuid.Parse(alertID); err == nil {
			filters["alert_id"] = id
		}
	}
	if incidentID := c.Query("incident_id"); incidentID != "" {
		if id, err := uuid.Parse(incidentID); err == nil {
			filters["incident_id"] = id
		}
	}
	if status := c.Query("status"); status != "" {
		filters["status"] = status
	}

	history, err := h.svc.GetNotificationHistory(c.Request.Context(), filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to fetch notifications",
			"error":   "internal error",
		})
		return
	}
	if history == nil {
		history = []*notifications.Notification{}
	}

	stats, _ := h.svc.GetNotificationStats(c.Request.Context())

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    history,
		"stats":   stats,
	})
}

// MarkAllRead acknowledges all pending notifications.
// notification_log is append-only so this is a no-op — returns 200 to unblock UI badge reset.
func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "All notifications marked as read",
	})
}

// RegisterRoutes registers notification routes
func (h *NotificationHandler) RegisterRoutes(router *gin.RouterGroup) {
	notifs := router.Group("/notifications")
	{
		notifs.GET("/recent", h.GetRecentNotifications)
		notifs.POST("/mark-all-read", h.MarkAllRead)
	}
}
