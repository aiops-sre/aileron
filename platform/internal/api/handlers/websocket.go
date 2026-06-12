package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/aileron-platform/aileron/platform/internal/cache"
)

// WebSocketHandler manages WebSocket connections
type WebSocketHandler struct {
	upgrader    websocket.Upgrader
	connections map[string]*WebSocketConnection
	mu          sync.RWMutex
	redisCache  *cache.RedisCache
	db          *sql.DB
}

// WebSocketConnection represents an active WebSocket connection
type WebSocketConnection struct {
	ID       string
	UserID   string
	Conn     *websocket.Conn
	Send     chan []byte
	Type     string // dashboard, ai-investigations, etc.
	LastPing time.Time
}

// WebSocketMessage represents a WebSocket message
type WebSocketMessage struct {
	Type      string      `json:"type"`
	Event     string      `json:"event"`
	Data      interface{} `json:"data"`
	Timestamp time.Time   `json:"timestamp"`
}

// NewWebSocketHandler creates a new WebSocket handler
func NewWebSocketHandler(redisCache *cache.RedisCache, db *sql.DB) *WebSocketHandler {
	rawAllowed := os.Getenv("ALLOWED_ORIGINS")
	var allowedOrigins []string
	for _, o := range strings.Split(rawAllowed, ",") {
		if t := strings.TrimSpace(o); t != "" {
			allowedOrigins = append(allowedOrigins, t)
		}
	}
	isProd := os.Getenv("ENV") == "production"

	return &WebSocketHandler{
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true // same-origin requests carry no Origin header
				}
				if len(allowedOrigins) > 0 {
					for _, o := range allowedOrigins {
						if o == origin {
							return true
						}
					}
					return false
				}
				// No allowlist configured: permit in dev, block in production.
				return !isProd
			},
		},
		connections: make(map[string]*WebSocketConnection),
		redisCache:  redisCache,
		db:          db,
	}
}

// HandleDashboardWebSocket handles dashboard WebSocket connections
func (h *WebSocketHandler) HandleDashboardWebSocket(c *gin.Context) {
	h.handleWebSocket(c, "dashboard")
}

// HandleAIInvestigationsWebSocket handles AI investigation WebSocket connections
func (h *WebSocketHandler) HandleAIInvestigationsWebSocket(c *gin.Context) {
	h.handleWebSocket(c, "ai-investigations")
}

// HandleCorrelationWebSocket handles correlation analysis WebSocket connections
func (h *WebSocketHandler) HandleCorrelationWebSocket(c *gin.Context) {
	h.handleWebSocket(c, "correlation")
}

// handleWebSocket handles the WebSocket upgrade and connection management
func (h *WebSocketHandler) handleWebSocket(c *gin.Context, connectionType string) {
	// Get user info from context (set by auth middleware)
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	// Upgrade HTTP connection to WebSocket
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	// Create connection object
	connectionID := uuid.New().String()
	wsConn := &WebSocketConnection{
		ID:       connectionID,
		UserID:   userID.(uuid.UUID).String(),
		Conn:     conn,
		Send:     make(chan []byte, 256),
		Type:     connectionType,
		LastPing: time.Now(),
	}

	// Register connection
	h.mu.Lock()
	h.connections[connectionID] = wsConn
	h.mu.Unlock()

	// Track in Redis if available
	if h.redisCache != nil {
		h.redisCache.TrackWebSocketConnection(connectionID, wsConn.UserID)
	}

	log.Printf("New %s WebSocket connection: %s (user: %s)", connectionType, connectionID, wsConn.UserID)

	// Send welcome message
	welcomeMsg := WebSocketMessage{
		Type:      connectionType,
		Event:     "connected",
		Data:      gin.H{"connection_id": connectionID, "message": "Connected successfully"},
		Timestamp: time.Now(),
	}
	h.sendMessage(wsConn, welcomeMsg)

	// Start goroutines for reading and writing
	go h.writePump(wsConn)
	go h.readPump(wsConn)

	// Send initial data based on connection type
	go h.sendInitialData(wsConn)
}

// sendInitialData sends initial data based on connection type
func (h *WebSocketHandler) sendInitialData(conn *WebSocketConnection) {
	time.Sleep(100 * time.Millisecond) // Brief delay to ensure connection is ready

	switch conn.Type {
	case "dashboard":
		h.sendDashboardData(conn)
	case "ai-investigations":
		h.sendAIInvestigationData(conn)
	case "correlation":
		h.sendCorrelationData(conn)
	}
}

// sendDashboardData sends real-time dashboard data
func (h *WebSocketHandler) sendDashboardData(conn *WebSocketConnection) {
	// Send Redis metrics if available
	if h.redisCache != nil {
		if stats, err := h.redisCache.GetStats(); err == nil {
			msg := WebSocketMessage{
				Type:      "dashboard",
				Event:     "redis_metrics",
				Data:      stats,
				Timestamp: time.Now(),
			}
			h.sendMessage(conn, msg)
		}

		// Send WebSocket connection count
		if count, err := h.redisCache.GetActiveWebSocketCount(); err == nil {
			msg := WebSocketMessage{
				Type:      "dashboard",
				Event:     "websocket_metrics",
				Data:      gin.H{"active_connections": count},
				Timestamp: time.Now(),
			}
			h.sendMessage(conn, msg)
		}
	}

	// Send live alert/incident counts from the DB.
	if h.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		var totalAlerts, critAlerts, warnAlerts, infoAlerts int
		h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE status != 'resolved'`).Scan(&totalAlerts)
		h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE status != 'resolved' AND severity = 'critical'`).Scan(&critAlerts)
		h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE status != 'resolved' AND severity = 'warning'`).Scan(&warnAlerts)
		h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE status != 'resolved' AND severity = 'info'`).Scan(&infoAlerts)

		var activeIncidents, resolvedIncidents int
		h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE status != 'resolved'`).Scan(&activeIncidents)
		h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE status = 'resolved'`).Scan(&resolvedIncidents)

		msg := WebSocketMessage{
			Type:  "dashboard",
			Event: "metrics_update",
			Data: gin.H{
				"alerts": gin.H{
					"total":    totalAlerts,
					"critical": critAlerts,
					"warning":  warnAlerts,
					"info":     infoAlerts,
				},
				"incidents": gin.H{
					"active":   activeIncidents,
					"resolved": resolvedIncidents,
				},
			},
			Timestamp: time.Now(),
		}
		h.sendMessage(conn, msg)
	}
}

// sendAIInvestigationData sends real AI investigation metrics from the database.
func (h *WebSocketHandler) sendAIInvestigationData(conn *WebSocketConnection) {
	activeInvestigations := 0
	completedToday := 0
	aiConfidenceAvg := float64(0)
	processingQueue := 0

	if h.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// Active investigations: incidents where rca_status is in progress
		h.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM incidents WHERE rca_status IN ('running','queued','in_progress')`).
			Scan(&activeInvestigations)

		// Completed today
		h.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM incidents WHERE rca_status = 'completed' AND updated_at >= CURRENT_DATE`).
			Scan(&completedToday)

		// Average AI confidence for completed investigations in last 24h
		h.db.QueryRowContext(ctx,
			`SELECT COALESCE(AVG(rca_confidence), 0) FROM incidents WHERE rca_status = 'completed' AND updated_at >= NOW() - INTERVAL '24 hours'`).
			Scan(&aiConfidenceAvg)

		// Processing queue: open alerts without an incident, created in last 5 min
		h.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM alerts WHERE status = 'open' AND incident_id IS NULL AND created_at >= NOW() - INTERVAL '5 minutes'`).
			Scan(&processingQueue)
	}

	msg := WebSocketMessage{
		Type:  "ai-investigations",
		Event: "investigation_status",
		Data: gin.H{
			"active_investigations": activeInvestigations,
			"completed_today":       completedToday,
			"ai_confidence_avg":     aiConfidenceAvg,
			"processing_queue":      processingQueue,
		},
		Timestamp: time.Now(),
	}
	h.sendMessage(conn, msg)
}

// sendCorrelationData sends real-time correlation strategy breakdown from the database.
func (h *WebSocketHandler) sendCorrelationData(conn *WebSocketConnection) {
	strategyCount := map[string]int{
		"temporal":       0,
		"topology":       0,
		"semantic":       0,
		"infrastructure": 0,
		"rules":          0,
	}
	autonomousDecisions := 0
	confidenceScore := float64(0)

	if h.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		rows, err := h.db.QueryContext(ctx, `
			SELECT COALESCE(dominant_strategy, 'unknown'), COUNT(*)
			FROM pipeline_correlation_results
			WHERE processed_at >= NOW() - INTERVAL '1 hour'
			GROUP BY dominant_strategy
		`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var strategy string
				var cnt int
				if rows.Scan(&strategy, &cnt) == nil {
					if _, ok := strategyCount[strategy]; ok {
						strategyCount[strategy] = cnt
					} else {
						// Map infrastructure variants
						strategyCount["infrastructure"] += cnt
					}
					autonomousDecisions += cnt
				}
			}
		}

		// Average confidence from recent correlation results
		h.db.QueryRowContext(ctx,
			`SELECT COALESCE(AVG(confidence_score), 0) FROM pipeline_correlation_results WHERE processed_at >= NOW() - INTERVAL '1 hour'`).
			Scan(&confidenceScore)
	}

	msg := WebSocketMessage{
		Type:  "correlation",
		Event: "correlation_update",
		Data: gin.H{
			"strategies_active": 4,
			"correlations_found": gin.H{
				"temporal":       strategyCount["temporal"],
				"topology":       strategyCount["topology"],
				"semantic":       strategyCount["semantic"],
				"infrastructure": strategyCount["infrastructure"],
				"rules":          strategyCount["rules"],
			},
			"autonomous_decisions": autonomousDecisions,
			"confidence_score":     confidenceScore,
		},
		Timestamp: time.Now(),
	}
	h.sendMessage(conn, msg)
}

// writePump handles writing messages to the WebSocket connection
func (h *WebSocketHandler) writePump(conn *WebSocketConnection) {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		conn.Conn.Close()
		h.removeConnection(conn.ID)
	}()

	for {
		select {
		case message, ok := <-conn.Send:
			conn.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				conn.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := conn.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				log.Printf("WebSocket write error: %v", err)
				return
			}

		case <-ticker.C:
			conn.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump handles reading messages from the WebSocket connection
func (h *WebSocketHandler) readPump(conn *WebSocketConnection) {
	defer func() {
		conn.Conn.Close()
		h.removeConnection(conn.ID)
	}()

	conn.Conn.SetReadLimit(512)
	conn.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.Conn.SetPongHandler(func(string) error {
		conn.LastPing = time.Now()
		conn.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := conn.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket read error: %v", err)
			}
			break
		}

		// Handle incoming message
		h.handleIncomingMessage(conn, message)
	}
}

// handleIncomingMessage processes incoming WebSocket messages
func (h *WebSocketHandler) handleIncomingMessage(conn *WebSocketConnection, message []byte) {
	var msg WebSocketMessage
	if err := json.Unmarshal(message, &msg); err != nil {
		log.Printf("Invalid WebSocket message: %v", err)
		return
	}

	// Handle different message types
	switch msg.Event {
	case "subscribe":
		// Handle subscription requests
		log.Printf("Client %s subscribed to %s", conn.ID, msg.Type)
		h.sendSubscriptionConfirmation(conn, msg.Type)
	case "ping":
		// Respond to ping
		pongMsg := WebSocketMessage{
			Type:      conn.Type,
			Event:     "pong",
			Data:      gin.H{"timestamp": time.Now()},
			Timestamp: time.Now(),
		}
		h.sendMessage(conn, pongMsg)
	case "request_update":
		// Send fresh data based on connection type
		h.sendInitialData(conn)
	}
}

// sendSubscriptionConfirmation sends confirmation of subscription
func (h *WebSocketHandler) sendSubscriptionConfirmation(conn *WebSocketConnection, subscriptionType string) {
	msg := WebSocketMessage{
		Type:      conn.Type,
		Event:     "subscription_confirmed",
		Data:      gin.H{"subscription": subscriptionType, "status": "active"},
		Timestamp: time.Now(),
	}
	h.sendMessage(conn, msg)
}

// sendMessage sends a message to a WebSocket connection
func (h *WebSocketHandler) sendMessage(conn *WebSocketConnection, msg WebSocketMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal WebSocket message: %v", err)
		return
	}

	select {
	case conn.Send <- data:
	default:
		close(conn.Send)
		h.removeConnection(conn.ID)
	}
}

// removeConnection removes a WebSocket connection
func (h *WebSocketHandler) removeConnection(connectionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if conn, exists := h.connections[connectionID]; exists {
		close(conn.Send)
		delete(h.connections, connectionID)

		// Remove from Redis if available
		if h.redisCache != nil {
			h.redisCache.RemoveWebSocketConnection(connectionID)
		}

		log.Printf("WebSocket connection removed: %s", connectionID)
	}
}

// BroadcastToAll broadcasts a message to all connections of a specific type
func (h *WebSocketHandler) BroadcastToAll(connectionType string, msg WebSocketMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, conn := range h.connections {
		if conn.Type == connectionType {
			h.sendMessage(conn, msg)
		}
	}
}

// BroadcastToUser broadcasts a message to all connections of a specific user
func (h *WebSocketHandler) BroadcastToUser(userID string, msg WebSocketMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, conn := range h.connections {
		if conn.UserID == userID {
			h.sendMessage(conn, msg)
		}
	}
}

// GetConnectionStats returns WebSocket connection statistics
func (h *WebSocketHandler) GetConnectionStats() map[string]interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()

	stats := make(map[string]interface{})
	typeCount := make(map[string]int)
	totalConnections := len(h.connections)

	for _, conn := range h.connections {
		typeCount[conn.Type]++
	}

	stats["total_connections"] = totalConnections
	stats["by_type"] = typeCount
	stats["last_updated"] = time.Now()

	return stats
}

// StartPeriodicUpdates starts sending periodic updates to connected clients
func (h *WebSocketHandler) StartPeriodicUpdates(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Send periodic updates to dashboard connections
			h.mu.RLock()
			for _, conn := range h.connections {
				if conn.Type == "dashboard" {
					h.sendPeriodicUpdate(conn)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// sendPeriodicUpdate sends periodic updates to dashboard connections
func (h *WebSocketHandler) sendPeriodicUpdate(conn *WebSocketConnection) {
	// Send Redis metrics if available
	if h.redisCache != nil {
		if stats, err := h.redisCache.GetStats(); err == nil {
			msg := WebSocketMessage{
				Type:      "dashboard",
				Event:     "periodic_update",
				Data:      gin.H{"redis": stats, "timestamp": time.Now()},
				Timestamp: time.Now(),
			}
			h.sendMessage(conn, msg)
		}
	}
}

// RegisterRoutes registers WebSocket routes
func (h *WebSocketHandler) RegisterRoutes(router *gin.RouterGroup) {
	ws := router.Group("/ws")
	{
		ws.GET("/dashboard", h.HandleDashboardWebSocket)
		ws.GET("/ai-investigations", h.HandleAIInvestigationsWebSocket)
		ws.GET("/correlation", h.HandleCorrelationWebSocket)
	}
}
