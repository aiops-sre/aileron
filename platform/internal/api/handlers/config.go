package handlers

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ConfigHandler handles configuration endpoints
type ConfigHandler struct {
	db *sql.DB
}

// NewConfigHandler creates a new config handler
func NewConfigHandler(db *sql.DB) *ConfigHandler {
	return &ConfigHandler{
		db: db,
	}
}

// GetLDAPConfig returns LDAP configuration from system_config table.
func (h *ConfigHandler) GetLDAPConfig(c *gin.Context) {
	keys := []string{"enabled", "server", "port", "base_dn", "bind_dn", "use_tls", "user_filter", "group_filter"}
	cfg := gin.H{
		"enabled":      false,
		"server":       "",
		"port":         636,
		"base_dn":      "",
		"bind_dn":      "",
		"use_tls":      true,
		"user_filter":  "(uid=%s)",
		"group_filter": "(member=%s)",
	}

	if h.db != nil {
		rows, err := h.db.QueryContext(c.Request.Context(),
			`SELECT key, value FROM system_config WHERE category = 'ldap' AND key = ANY($1)`,
			keys)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var key, value string
				if rows.Scan(&key, &value) == nil {
					switch key {
					case "enabled":
						cfg[key] = value == "true"
					case "port":
						if p, err := strconv.Atoi(value); err == nil {
							cfg[key] = p
						}
					case "use_tls":
						cfg[key] = value == "true"
					default:
						// Mask bind password on reads
						if key == "bind_password" {
							cfg[key] = "********"
						} else {
							cfg[key] = value
						}
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": cfg})
}

// UpdateLDAPConfig persists LDAP configuration to system_config table.
func (h *ConfigHandler) UpdateLDAPConfig(c *gin.Context) {
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request body"})
		return
	}

	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Database unavailable"})
		return
	}

	allowedKeys := map[string]bool{
		"enabled": true, "server": true, "port": true, "base_dn": true,
		"bind_dn": true, "bind_password": true, "use_tls": true,
		"user_filter": true, "group_filter": true,
	}

	tx, err := h.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal error"})
		return
	}
	defer tx.Rollback()

	for key, val := range req {
		if !allowedKeys[key] {
			continue
		}
		var strVal string
		switch v := val.(type) {
		case bool:
			if v {
				strVal = "true"
			} else {
				strVal = "false"
			}
		case float64:
			strVal = strconv.Itoa(int(v))
		case string:
			strVal = v
		default:
			continue
		}
		if _, err := tx.ExecContext(c.Request.Context(), `
			INSERT INTO system_config (id, category, key, value, created_at, updated_at)
			VALUES (gen_random_uuid(), 'ldap', $1, $2, NOW(), NOW())
			ON CONFLICT (category, key) DO UPDATE SET value = $2, updated_at = NOW()
		`, key, strVal); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal error"})
			return
		}
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "LDAP configuration updated"})
}

// TestLDAP tests the LDAP connection using the stored configuration.
func (h *ConfigHandler) TestLDAP(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Database unavailable"})
		return
	}

	// Load current config from DB
	rows, err := h.db.QueryContext(c.Request.Context(),
		`SELECT key, value FROM system_config WHERE category = 'ldap'`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "internal error"})
		return
	}
	defer rows.Close()

	cfg := map[string]string{}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			cfg[k] = v
		}
	}

	server := cfg["server"]
	if server == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "LDAP server not configured"})
		return
	}

	port := 636
	if p, err := strconv.Atoi(cfg["port"]); err == nil {
		port = p
	}
	useTLS := cfg["use_tls"] != "false"

	// Attempt TCP connection to validate server reachability.
	// Use net.JoinHostPort so IPv6 addresses are correctly bracketed.
	addr := net.JoinHostPort(server, strconv.Itoa(port))
	var dialErr error
	if useTLS {
		conn, err := tls.DialWithDialer(
			&net.Dialer{Timeout: 5 * time.Second},
			"tcp", addr,
			&tls.Config{ServerName: server},
		)
		if err == nil {
			conn.Close()
		}
		dialErr = err
	} else {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err == nil {
			conn.Close()
		}
		dialErr = err
	}

	if dialErr != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("LDAP connection failed: %v", dialErr),
			"server":  addr,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("LDAP server %s is reachable", addr),
		"server":  addr,
	})
}

// GetGeneralConfig returns general configuration
func (h *ConfigHandler) GetGeneralConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"system_name":           "AlertHub Enterprise",
			"session_timeout":       24,
			"auto_refresh_interval": 30,
			"mfa_required":          false,
		},
	})
}

// UpdateGeneralConfig updates general configuration
func (h *ConfigHandler) UpdateGeneralConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "General configuration updated",
	})
}

// GetSMTPConfig returns SMTP configuration
func (h *ConfigHandler) GetSMTPConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"enabled": false,
			"host":    "",
			"port":    587,
		},
	})
}

// UpdateSMTPConfig updates SMTP configuration
func (h *ConfigHandler) UpdateSMTPConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "SMTP configuration updated",
	})
}

// GetSlackConfig returns Slack configuration
func (h *ConfigHandler) GetSlackConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"enabled":     false,
			"webhook_url": "",
			"channel":     "#alerthub",
		},
	})
}

// UpdateSlackConfig updates Slack configuration
func (h *ConfigHandler) UpdateSlackConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Slack configuration updated",
	})
}

// GetAIConfig returns AI configuration
func (h *ConfigHandler) GetAIConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"enabled":  false,
			"provider": "openai",
			"model":    "gpt-4",
		},
	})
}

// UpdateAIConfig updates AI configuration
func (h *ConfigHandler) UpdateAIConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "AI configuration updated",
	})
}

// GetSAMLConfig returns SAML configuration
func (h *ConfigHandler) GetSAMLConfig(c *gin.Context) {
	// Check if user is authenticated
	_, authenticated := c.Get("user_id")

	var samlConfig struct {
		ID          uuid.UUID `json:"id"`
		Enabled     bool      `json:"enabled"`
		EntityID    string    `json:"entity_id"`
		SSOURL      string    `json:"sso_url"`
		Certificate string    `json:"certificate"`
		PrivateKey  string    `json:"private_key"`
		IdPMetadata string    `json:"idp_metadata_url"`
	}

	err := h.db.QueryRow(`
		SELECT id, enabled, entity_id, sso_url, certificate, private_key, idp_metadata_url
		FROM saml_config
		LIMIT 1
	`).Scan(
		&samlConfig.ID,
		&samlConfig.Enabled,
		&samlConfig.EntityID,
		&samlConfig.SSOURL,
		&samlConfig.Certificate,
		&samlConfig.PrivateKey,
		&samlConfig.IdPMetadata,
	)

	if err == sql.ErrNoRows {
		// Return default empty config
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"enabled":          false,
				"entity_id":        "",
				"sso_url":          "",
				"certificate":      "",
				"private_key":      "",
				"idp_metadata_url": "",
			},
		})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to fetch SAML configuration",
		})
		return
	}

	// If not authenticated, only return public info (enabled, entity_id, sso_url)
	if !authenticated {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"enabled":          samlConfig.Enabled,
				"entity_id":        samlConfig.EntityID,
				"sso_url":          samlConfig.SSOURL,
				"idp_metadata_url": samlConfig.IdPMetadata,
			},
		})
		return
	}

	// Authenticated - return full config including sensitive data
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    samlConfig,
	})
}

// UpdateSAMLConfig updates SAML configuration
func (h *ConfigHandler) UpdateSAMLConfig(c *gin.Context) {
	var req struct {
		Enabled        bool   `json:"enabled"`
		EntityID       string `json:"entity_id"`
		SSOURL         string `json:"sso_url"`
		IdPMetadataURL string `json:"idp_metadata_url"`
		Certificate    string `json:"certificate"` // User can provide their own
		PrivateKey     string `json:"private_key"` // User can provide their own
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	// Handle certificate/key logic
	cert := req.Certificate
	privKey := req.PrivateKey

	// IMPORTANT: Certificate and Private Key must be a matching pair
	// If user provides only one, we auto-generate a new matching pair
	if (cert == "" && privKey != "") || (cert != "" && privKey == "") {
		// Mismatched - auto-generate both
		generatedCert, generatedKey, err := h.generateNewSAMLCertificate()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Failed to generate SAML certificate: " + err.Error(),
			})
			return
		}
		cert = generatedCert
		privKey = generatedKey
	} else if cert == "" && privKey == "" {
		// Both empty - check if we have existing, otherwise generate
		existingCert, existingKey, err := h.getExistingSAMLCertificate()
		if err != nil || existingCert == "" {
			// Generate new pair
			cert, privKey, err = h.generateNewSAMLCertificate()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"success": false,
					"message": "Failed to generate SAML certificate: " + err.Error(),
				})
				return
			}
		} else {
			cert = existingCert
			privKey = existingKey
		}
	}
	// else: both provided, use them as-is

	// Check if config exists
	var existingID uuid.UUID
	err := h.db.QueryRow("SELECT id FROM saml_config LIMIT 1").Scan(&existingID)

	if err == sql.ErrNoRows {
		// Create new config
		_, err = h.db.Exec(`
			INSERT INTO saml_config (id, enabled, entity_id, sso_url, certificate, private_key, idp_metadata_url, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
		`, uuid.New(), req.Enabled, req.EntityID, req.SSOURL, cert, privKey, req.IdPMetadataURL)
	} else {
		// Update existing config
		_, err = h.db.Exec(`
			UPDATE saml_config
			SET enabled = $1, entity_id = $2, sso_url = $3, certificate = $4, private_key = $5, idp_metadata_url = $6, updated_at = NOW()
			WHERE id = $7
		`, req.Enabled, req.EntityID, req.SSOURL, cert, privKey, req.IdPMetadataURL, existingID)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to save SAML configuration: " + err.Error(),
		})
		return
	}

	// Log the configuration change
	userID, _ := c.Get("user_id")
	h.logConfigChange(c, userID, "saml_config_updated", gin.H{
		"enabled":         req.Enabled,
		"entity_id":       req.EntityID,
		"has_custom_cert": req.Certificate != "",
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "SAML configuration saved successfully",
		"data": gin.H{
			"certificate": cert,
			"private_key": privKey,
		},
	})
}

// getExistingSAMLCertificate retrieves existing SAML certificate from database
func (h *ConfigHandler) getExistingSAMLCertificate() (string, string, error) {
	var existingCert, existingKey string
	err := h.db.QueryRow("SELECT certificate, private_key FROM saml_config LIMIT 1").Scan(&existingCert, &existingKey)

	if err == sql.ErrNoRows {
		return "", "", nil
	}

	return existingCert, existingKey, err
}

// generateNewSAMLCertificate generates a new SAML certificate and private key pair
func (h *ConfigHandler) generateNewSAMLCertificate() (string, string, error) {

	// Generate new RSA private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"AlertHub"},
			CommonName:   "AlertHub SAML SP",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour * 10), // 10 years
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// Create self-signed certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return "", "", err
	}

	// Encode certificate to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key to PEM
	privKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	return string(certPEM), string(privKeyPEM), nil
}

// GenerateSAMLCertificate generates new SAML certificate and private key
func (h *ConfigHandler) GenerateSAMLCertificate(c *gin.Context) {
	cert, privKey, err := h.generateNewSAMLCertificate()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to generate SAML certificate: " + err.Error(),
		})
		return
	}

	// Save to database
	var existingID uuid.UUID
	err = h.db.QueryRow("SELECT id FROM saml_config LIMIT 1").Scan(&existingID)

	if err == sql.ErrNoRows {
		_, err = h.db.Exec(`
			INSERT INTO saml_config (id, enabled, entity_id, sso_url, certificate, private_key, idp_metadata_url, created_at, updated_at)
			VALUES ($1, false, '', '', $2, $3, '', NOW(), NOW())
		`, uuid.New(), cert, privKey)
	} else {
		_, err = h.db.Exec(`
			UPDATE saml_config
			SET certificate = $1, private_key = $2, updated_at = NOW()
			WHERE id = $3
		`, cert, privKey, existingID)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "New SAML certificate pair generated. Use the certificate for your IdP, and keep the private key secure in AlertHub.",
		"data": gin.H{
			"certificate": cert,
			"private_key": privKey,
			"note":        "IMPORTANT: You need BOTH certificate and matching private key. If you only have a certificate, generate a new pair.",
		},
	})
}

func (h *ConfigHandler) logConfigChange(c *gin.Context, userID interface{}, action string, details interface{}) {
	detailsJSON, _ := json.Marshal(details)

	var uid *uuid.UUID
	if userID != nil {
		switch v := userID.(type) {
		case uuid.UUID:
			uid = &v
		case string:
			parsed, err := uuid.Parse(v)
			if err == nil {
				uid = &parsed
			}
		}
	}

	h.db.Exec(`
		INSERT INTO audit_logs (id, user_id, action, resource_type, resource_id, details, ip_address, user_agent, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
	`, uuid.New(), uid, action, "config", nil, detailsJSON, c.ClientIP(), c.Request.UserAgent())
}

// RegisterRoutes registers config routes
// publicRouter is for unauthenticated access (login page needs SAML config)
// protectedRouter requires authentication
func (h *ConfigHandler) RegisterRoutes(publicRouter *gin.RouterGroup, protectedRouter *gin.RouterGroup) {
	// Public routes (no auth required)
	public := publicRouter.Group("/config")
	{
		// Public SAML config (returns only non-sensitive info)
		public.GET("/saml", h.GetSAMLConfig)
	}

	// Protected routes (auth required)
	config := protectedRouter.Group("/config")
	{
		config.GET("/general", h.GetGeneralConfig)
		config.POST("/general", h.UpdateGeneralConfig)

		config.GET("/smtp", h.GetSMTPConfig)
		config.POST("/smtp", h.UpdateSMTPConfig)

		config.GET("/slack", h.GetSlackConfig)
		config.POST("/slack", h.UpdateSlackConfig)

		config.GET("/ai", h.GetAIConfig)
		config.POST("/ai", h.UpdateAIConfig)

		ldap := config.Group("/ldap")
		{
			ldap.GET("", h.GetLDAPConfig)
			ldap.POST("", h.UpdateLDAPConfig)
			ldap.POST("/test", h.TestLDAP)
		}

		saml := config.Group("/saml")
		{
			// POST and generate-cert require auth
			saml.POST("", h.UpdateSAMLConfig)
			saml.POST("/generate-cert", h.GenerateSAMLCertificate)
		}
	}
}
