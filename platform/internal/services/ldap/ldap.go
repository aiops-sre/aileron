// Package ldap re-exports the LDAP integration layer.
// This is the generic OSS entry point; configure via LDAP_URL env var
// for any LDAP-compatible directory (OpenLDAP, Active Directory, FreeIPA, etc.).
package ldap

import (
	"context"
)

type contextKey string

const roleKey contextKey = "ldap_role"
const groupsKey contextKey = "ldap_groups"

// RoleFromContext returns the LDAP-mapped role stored in ctx, or "".
func RoleFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(roleKey).(string); ok {
		return v
	}
	return ""
}

// GroupsFromContext returns LDAP group memberships stored in ctx.
func GroupsFromContext(ctx context.Context) []string {
	if v, ok := ctx.Value(groupsKey).([]string); ok {
		return v
	}
	return nil
}

// WithRole injects a role into the context (called by auth middleware after LDAP lookup).
func WithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, roleKey, role)
}

// WithGroups injects group membership into the context.
func WithGroups(ctx context.Context, groups []string) context.Context {
	return context.WithValue(ctx, groupsKey, groups)
}

// Service is a generic LDAP service interface stub.
// Wire to the dsldap.Service implementation in production.
type Service interface {
	GetUserGroups(email string) ([]string, error)
	MapGroupsToRole(groups []string) string
	ReloadMappings()
}
