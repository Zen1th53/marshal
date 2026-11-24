package settingscatalog

import "time"

// Change is a durable settings receipt.
type Change struct {
	Scope    string    `json:"scope,omitempty"`
	Applies  string    `json:"applies,omitempty"`
	ID       string    `json:"id"`
	Revision int64     `json:"revision"`
	Key      string    `json:"key"`
	Old      string    `json:"old"`
	New      string    `json:"new"`
	Actor    string    `json:"actor"`
	At       time.Time `json:"at"`
}

type Snapshot struct {
	Revision int64    `json:"revision"`
	Preset   string   `json:"preset"`
	Rows     []Row    `json:"rows"`
	Changes  []Change `json:"changes"`
}

// PolicyRows displays mandatory runtime policy without presenting an edit control.
func PolicyRows() []Row {
	out := []Row{}
	for _, p := range [][2]string{{"independent-verifier", "required"}, {"design-conformance", "approved plan"}, {"scheduler", "plan order and independent file scopes"}, {"preset", "custom"}} {
		out = append(out, Row{Key: p[0], Value: p[1], Effective: p[1], Next: p[1], Scope: "build", Applies: "this build", Source: "edition policy", LockedReason: "fixed runtime policy", Help: "About this build"})
	}
	return out
}
