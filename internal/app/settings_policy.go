package app

import (
	"github.com/Zen1th53/marshal/internal/settingscatalog"
)

// SettingsPolicies exposes runtime constants without inventing writable settings.
func SettingsPolicies() []settingscatalog.Row {
	rows := settingscatalog.PolicyRows()
	rows = append(rows, editionSettingsPolicies()...)
	rows = append(rows, settingscatalog.Row{Key: "role-turn-timeout", Value: defaultMarshalRoleTurnTimeout.String(), Effective: marshalRoleTurnTimeout().String(), Next: marshalRoleTurnTimeout().String(), Scope: "launch", Applies: "next role turn", Source: "environment or runtime default", Help: "Set MARSHAL_ROLE_TURN_TIMEOUT before launch.", LockedReason: "launch configuration"})
	return rows
}
