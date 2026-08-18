package authz

import (
	"context"
	"strings"
)

// RoleCatalog is the configuration boundary for role definitions. It only
// resolves names; authority evaluation remains the canonical Can path.
type RoleCatalog struct {
	roles map[string]Role
}

// NewRoleCatalog validates configured role definitions once at composition
// time. Custom role names use the same bounded identifier grammar as durable
// role bindings and cannot introduce new authority values.
func NewRoleCatalog(roles []Role) (RoleCatalog, error) {
	catalog := RoleCatalog{roles: make(map[string]Role, len(roles))}
	for _, role := range roles {
		if err := validateRole(role, true); err != nil {
			return RoleCatalog{}, err
		}
		name := strings.ToLower(strings.TrimSpace(role.Name))
		if _, exists := catalog.roles[name]; exists {
			return RoleCatalog{}, ErrRoleInvalid
		}
		role.Name = name
		catalog.roles[name] = role
	}
	return catalog, nil
}

// Can resolves a configured custom role and delegates the decision to the
// same fail-closed authority evaluation used by built-in roles.
func (c RoleCatalog) Can(ctx context.Context, subject Principal, authority Authority, resource string) (Decision, error) {
	name := strings.ToLower(strings.TrimSpace(subject.Role.Name))
	if configured, ok := c.roles[name]; ok {
		subject.Role = configured
		return can(ctx, subject, authority, resource, true)
	}
	return Can(ctx, subject, authority, resource)
}

func validateRole(role Role, allowCustom bool) error {
	if allowCustom {
		if !validRoleBindingName(role.Name) {
			return ErrUnknownRole
		}
	} else if !validRoleName(role.Name) {
		return ErrUnknownRole
	}
	if len(role.Authorities) == 0 {
		return ErrRoleInvalid
	}
	seen := make(map[Authority]struct{}, len(role.Authorities))
	for _, authority := range role.Authorities {
		if _, ok := knownAuthorities[authority]; !ok {
			return ErrUnknownAuthority
		}
		if _, duplicate := seen[authority]; duplicate {
			return ErrRoleInvalid
		}
		seen[authority] = struct{}{}
	}
	return nil
}
