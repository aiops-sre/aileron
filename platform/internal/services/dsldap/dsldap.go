// Package dsldap provides Apple DS-LDAP group lookup for RBAC.
// It binds as an application (not a user) and resolves a user's AD group
// memberships by email, then maps those groups to AlertHub roles.
// Grouprole mappings can be configured statically (via env vars) or dynamically
// (via DB-backed admin UI). DB mappings take priority over env-var defaults.
// Results are cached per-user to avoid hitting the directory on every request.
package dsldap

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	gldap "github.com/go-ldap/ldap/v3"
)

// Config holds all DS-LDAP configuration. Credentials are loaded from K8s secrets
// via environment variables LDAP_APP_ID and LDAP_APP_PASSWORD.
type Config struct {
	Enabled bool
	// ServerURL is the full ldaps:// URL, e.g. 
	ServerURL string
	// AppID is the IdMS application ID used to build the bind DN:
	//   appid=<AppID>,ou=applications,o=apple
	AppID string
	// AppPassword is the 16-character application password from the IdMS Portal.
	AppPassword string
	// UserSearchBase is the LDAP subtree to search for users, e.g. ou=people,o=apple
	UserSearchBase string
	// CacheTTL controls how long group memberships are cached. Default: 5 minutes.
	// DS-LDAP syncs AD group changes within 3-10 minutes, so 5 minutes is safe.
	CacheTTL time.Duration
	// AdminGroups lists LDAP group CNs that map to the 'admin' role.
	AdminGroups []string
	// OperatorGroups lists LDAP group CNs that map to the 'operator' role.
	OperatorGroups []string
	// ViewerGroups lists LDAP group CNs that map to the 'viewer' role.
	ViewerGroups []string
}

// DefaultConfig returns production-safe defaults for Apple DS-LDAP.
func DefaultConfig() Config {
	return Config{
		Enabled:        false,
		ServerURL:      "",
		UserSearchBase: "ou=people,o=apple",
		CacheTTL:       5 * time.Minute,
		AdminGroups:    []string{"aileron-admins"},
		OperatorGroups: []string{"aileron-operators", "aileron-operators"},
		ViewerGroups:   []string{"aileron-viewers"},
	}
}

type cacheEntry struct {
	groups    []string
	expiresAt time.Time
}

// dbGroupMapping is a single row from ldap_group_role_mappings.
type dbGroupMapping struct {
	ldapGroup string
	roleName  string
}

// Service is the DS-LDAP group-lookup client.
type Service struct {
	cfg      Config
	db       *sql.DB // optional; used for live mapping reloads from admin UI
	mu       sync.RWMutex
	cache    map[string]*cacheEntry
	dbMaps   []dbGroupMapping // DB-backed grouprole mappings (loaded on startup + refresh)
	mapsOnce sync.Once
}

// New creates a new DS-LDAP service. Returns nil if cfg.Enabled is false so
// callers can use a nil check as a feature flag.
func New(cfg Config) *Service {
	if !cfg.Enabled {
		return nil
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = 5 * time.Minute
	}
	return &Service{cfg: cfg, cache: make(map[string]*cacheEntry)}
}

// SetDB attaches a database connection so the service can load dynamic grouprole
// mappings configured via the Admin UI (ldap_group_role_mappings table).
// Call this immediately after New() if a DB is available.
func (s *Service) SetDB(db *sql.DB) {
	s.db = db
}

// ReloadMappings forces a reload of DB-backed grouprole mappings.
// Call this from the admin API after a mapping is created/updated/deleted.
func (s *Service) ReloadMappings() {
	if s.db == nil {
		return
	}
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT m.ldap_group, r.name
		FROM ldap_group_role_mappings m
		JOIN roles r ON m.role_id = r.id
		ORDER BY r.name  -- priority: admin < operator < viewer (last write wins in MapGroupsToRole)
	`)
	if err != nil {
		log.Printf("dsldap: failed to reload group mappings: %v", err)
		return
	}
	defer rows.Close()
	var maps []dbGroupMapping
	for rows.Next() {
		var m dbGroupMapping
		if err := rows.Scan(&m.ldapGroup, &m.roleName); err == nil {
			maps = append(maps, m)
		}
	}
	s.mu.Lock()
	s.dbMaps = maps
	s.mu.Unlock()
	log.Printf("dsldap: loaded %d grouprole mappings from database", len(maps))
}

// GetUserGroups returns the AD group CNs for a user identified by email.
// Results are cached for cfg.CacheTTL to avoid per-request LDAP round-trips.
func (s *Service) GetUserGroups(email string) ([]string, error) {
	if email == "" {
		return nil, nil
	}

	// Fast path: return cached value if still valid
	s.mu.RLock()
	if e, ok := s.cache[email]; ok && time.Now().Before(e.expiresAt) {
		groups := e.groups
		s.mu.RUnlock()
		return groups, nil
	}
	s.mu.RUnlock()

	groups, err := s.fetchGroups(email)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cache[email] = &cacheEntry{groups: groups, expiresAt: time.Now().Add(s.cfg.CacheTTL)}
	s.mu.Unlock()

	return groups, nil
}

// MapGroupsToRole maps a slice of AD group CNs to the highest-privilege
// AlertHub role. DB-backed mappings take priority over env-var defaults.
// Returns "" if no matching group is found.
func (s *Service) MapGroupsToRole(groups []string) string {
	// Load DB mappings on first call (lazy init)
	s.mapsOnce.Do(s.ReloadMappings)

	set := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		set[strings.ToLower(g)] = struct{}{}
	}

	// DB-backed mappings take priority (highest role wins: admin > operator > viewer)
	s.mu.RLock()
	dbMaps := s.dbMaps
	s.mu.RUnlock()

	bestRole := ""
	rolePriority := map[string]int{"admin": 3, "operator": 2, "viewer": 1}
	for _, m := range dbMaps {
		if _, ok := set[strings.ToLower(m.ldapGroup)]; ok {
			if rolePriority[m.roleName] > rolePriority[bestRole] {
				bestRole = m.roleName
			}
		}
	}
	if bestRole != "" {
		return bestRole
	}

	// Fallback: env-var configured groups
	for _, ag := range s.cfg.AdminGroups {
		if _, ok := set[strings.ToLower(ag)]; ok {
			return "admin"
		}
	}
	for _, og := range s.cfg.OperatorGroups {
		if _, ok := set[strings.ToLower(og)]; ok {
			return "operator"
		}
	}
	for _, vg := range s.cfg.ViewerGroups {
		if _, ok := set[strings.ToLower(vg)]; ok {
			return "viewer"
		}
	}
	return ""
}

// Ping verifies that the service can bind to DS-LDAP. Used for health checks.
func (s *Service) Ping() error {
	conn, err := s.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	return s.bind(conn)
}

// ServerURL returns the configured LDAP server URL (used for health check display).
func (s *Service) ServerURL() string { return s.cfg.ServerURL }

// InvalidateCache removes the cached groups for a user, forcing the next
// GetUserGroups call to re-query DS-LDAP.
func (s *Service) InvalidateCache(email string) {
	s.mu.Lock()
	delete(s.cache, email)
	s.mu.Unlock()
}

// SearchGroups returns up to 20 group CNs from ou=groups,o=apple whose CN
// contains the given prefix (case-insensitive substring match via *prefix*).
// Empty prefix returns the first 20 groups alphabetically for browse-on-focus UX.
func (s *Service) SearchGroups(prefix string) ([]string, error) {
	conn, err := s.dial()
	if err != nil {
		return nil, fmt.Errorf("dsldap dial: %w", err)
	}
	defer conn.Close()

	if err := s.bind(conn); err != nil {
		return nil, fmt.Errorf("dsldap bind: %w", err)
	}

	var filter string
	if prefix == "" {
		filter = "(cn=*)"
	} else {
		filter = fmt.Sprintf("(cn=*%s*)", gldap.EscapeFilter(prefix))
	}
	req := gldap.NewSearchRequest(
		"ou=groups,o=apple",
		gldap.ScopeWholeSubtree, gldap.NeverDerefAliases,
		0, 20, false,
		filter,
		[]string{"cn"},
		nil,
	)
	sr, err := conn.Search(req)
	if err != nil {
		return nil, fmt.Errorf("dsldap search groups: %w", err)
	}

	entries := sr.Entries
	if len(entries) > 20 {
		entries = entries[:20]
	}

	var groups []string
	for _, e := range entries {
		if cn := e.GetAttributeValue("cn"); cn != "" {
			groups = append(groups, cn)
		}
	}
	return groups, nil
}

// ---------- private helpers ----------

func (s *Service) fetchGroups(email string) ([]string, error) {
	conn, err := s.dial()
	if err != nil {
		return nil, fmt.Errorf("dsldap dial: %w", err)
	}
	defer conn.Close()

	if err := s.bind(conn); err != nil {
		return nil, fmt.Errorf("dsldap bind: %w", err)
	}

	// Step 1: fetch the user entry including the memberOf attribute.
	// Apple DS-LDAP stores memberOf values as "appledsid=N,ou=groups,o=apple"
	// (numeric IDs, not CN-based DNs), so we can't extract CNs directly from them.
	// Instead we collect the full DN set and resolve CNs in step 3.
	userReq := gldap.NewSearchRequest(
		s.cfg.UserSearchBase,
		gldap.ScopeWholeSubtree, gldap.NeverDerefAliases,
		0, 10, false,
		fmt.Sprintf("(mail=%s)", gldap.EscapeFilter(email)),
		[]string{"memberOf"},
		nil,
	)
	userSR, err := conn.Search(userReq)
	if err != nil {
		return nil, fmt.Errorf("dsldap user search: %w", err)
	}
	if len(userSR.Entries) == 0 {
		log.Printf("dsldap: user %s not found in directory", email)
		return nil, nil
	}

	// Build a lowercase set of every group DN the user belongs to
	rawMemberOf := userSR.Entries[0].GetAttributeValues("memberOf")
	log.Printf("dsldap: user %s has %d memberOf entries", email, len(rawMemberOf))
	memberOfSet := make(map[string]struct{}, len(rawMemberOf))
	for _, dn := range rawMemberOf {
		memberOfSet[strings.ToLower(dn)] = struct{}{}
	}

	// Step 2: collect every group CN we care about (configured + DB-backed).
	s.mapsOnce.Do(s.ReloadMappings)
	s.mu.RLock()
	dbMaps := s.dbMaps
	s.mu.RUnlock()

	targetGroups := make(map[string]struct{})
	for _, g := range s.cfg.AdminGroups {
		targetGroups[g] = struct{}{}
	}
	for _, g := range s.cfg.OperatorGroups {
		targetGroups[g] = struct{}{}
	}
	for _, g := range s.cfg.ViewerGroups {
		targetGroups[g] = struct{}{}
	}
	for _, m := range dbMaps {
		targetGroups[m.ldapGroup] = struct{}{}
	}

	// Step 3: for each target group CN, resolve its actual DN using an indexed
	// (cn=<group>) search, then check if that DN is in the user's memberOf set.
	// This avoids any unindexed attribute scan on the server side.
	var groups []string
	for groupCN := range targetGroups {
		req := gldap.NewSearchRequest(
			"ou=groups,o=apple",
			gldap.ScopeWholeSubtree, gldap.NeverDerefAliases,
			0, 1, false,
			fmt.Sprintf("(cn=%s)", gldap.EscapeFilter(groupCN)),
			[]string{"dn"},
			nil,
		)
		sr, err := conn.Search(req)
		if err != nil {
			log.Printf("dsldap: group DN lookup error for %s: %v", groupCN, err)
			continue
		}
		if len(sr.Entries) == 0 {
			log.Printf("dsldap: group %s not found in directory", groupCN)
			continue
		}
		groupDN := sr.Entries[0].DN
		if _, ok := memberOfSet[strings.ToLower(groupDN)]; ok {
			groups = append(groups, groupCN)
		}
	}
	log.Printf("dsldap: resolved %d groups for %s: %v", len(groups), email, groups)
	return groups, nil
}

func (s *Service) dial() (*gldap.Conn, error) {
	// Apple's corporate CA is not in the Alpine container trust store, so we skip
	// TLS certificate verification. The cluster is already inside Apple's internal
	// network; the connection to  is protected at the network
	// layer. Skip only cert verification — TLS encryption is still enforced.
	return gldap.DialURL(s.cfg.ServerURL, gldap.DialWithTLSConfig(&tls.Config{
		ServerName:         hostFromURL(s.cfg.ServerURL),
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: os.Getenv("INTERNAL_TLS_INSECURE") == "true", //nolint:gosec
	}))
}

func (s *Service) bind(conn *gldap.Conn) error {
	dn := fmt.Sprintf("appid=%s,ou=applications,o=apple", s.cfg.AppID)
	return conn.Bind(dn, s.cfg.AppPassword)
}

// extractCN pulls the CN value from an LDAP DN string.
// e.g. "cn=aileron-operators,ou=groups,o=apple" "aileron-operators"
func extractCN(dn string) string {
	parsed, err := gldap.ParseDN(dn)
	if err != nil {
		return ""
	}
	for _, rdn := range parsed.RDNs {
		for _, attr := range rdn.Attributes {
			if strings.EqualFold(attr.Type, "cn") {
				return attr.Value
			}
		}
	}
	return ""
}

// hostFromURL extracts the hostname from ldaps://host:port for TLS SNI.
func hostFromURL(u string) string {
	// strip scheme
	s := strings.TrimPrefix(u, "ldaps://")
	s = strings.TrimPrefix(s, "ldap://")
	// strip port
	if idx := strings.LastIndex(s, ":"); idx != -1 {
		s = s[:idx]
	}
	return s
}

// ContextKey is the type for values stored in request contexts by the auth middleware.
type ContextKey string

const (
	// ContextKeyRole is the key under which the LDAP-derived role is stored
	// in the request context. Read by rbac.CheckPermission.
	ContextKeyRole   ContextKey = "dsldap_role"
	// ContextKeyGroups is the key under which the raw LDAP groups are stored.
	ContextKeyGroups ContextKey = "dsldap_groups"
)

// WithRole returns a new context carrying the LDAP-derived role.
func WithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, ContextKeyRole, role)
}

// WithGroups returns a new context carrying the raw LDAP group list.
func WithGroups(ctx context.Context, groups []string) context.Context {
	return context.WithValue(ctx, ContextKeyGroups, groups)
}

// RoleFromContext extracts the LDAP-derived role from a context, or "" if absent.
func RoleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyRole).(string)
	return v
}

// GroupsFromContext extracts the raw LDAP groups from a context.
func GroupsFromContext(ctx context.Context) []string {
	v, _ := ctx.Value(ContextKeyGroups).([]string)
	return v
}
