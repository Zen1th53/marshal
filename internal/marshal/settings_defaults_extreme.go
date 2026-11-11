//go:build !governed

package marshal

// Fresh private projects delegate acceptance to the authenticated Marshal role.
// Saved settings, approved task scopes and running execution budgets remain
// authoritative; selecting this default never grants a worker extra authority.
func buildDefaultSettings() Settings {
	return Settings{ExecutionRights: RightsReadOnly, AcceptanceMode: AcceptMarshal, ReworkLimit: 2, UltraConcurrency: 4, Control: ControlFree, WorkerStallMinutes: 15}
}
