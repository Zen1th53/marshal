package settingscatalog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// DocsTable renders the project settings of this build as a Markdown table.
// The output depends only on the catalogue, so it is identical on every run.
func DocsTable() string {
	var b strings.Builder
	b.WriteString("| Setting | Choices or units | Default | Applies | Notes |\n|---|---|---|---|---|\n")
	defaults := marshal.DefaultSettings()
	for _, d := range Catalogue() {
		def := Effective(defaults, d.Key)
		if def == "" {
			def = "none"
		}
		choices := strings.Join(d.Values, ", ")
		if choices == "" {
			choices = d.Units
		}
		if choices == "" {
			choices = d.Kind
		}
		notes := d.Help
		if d.LockReason != "" {
			notes = "Locked: " + d.LockReason + ". " + notes
		}
		if len(d.LockedValues) > 0 {
			keys := make([]string, 0, len(d.LockedValues))
			for k := range d.LockedValues {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				notes += fmt.Sprintf(" Value %s is locked: %s.", k, d.LockedValues[k])
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", cell("`"+d.Key+"`"), cell(choices), cell("`"+def+"`"), cell(d.Applies), cell(notes))
	}
	return b.String()
}

func cell(s string) string {
	s = strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
	return strings.TrimSpace(s)
}
