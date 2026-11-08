//go:build governed

package marshal

func buildDefaultSettings() Settings {
	return Settings{ExecutionRights: RightsReadOnly, AcceptanceMode: AcceptMarshalThenUser, ReworkLimit: 2, UltraConcurrency: 3, Control: ControlFree}
}
