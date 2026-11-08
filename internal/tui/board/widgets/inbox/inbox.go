// Package inbox renders pending operator decisions in the Board.
package inbox

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/rivo/uniseg"
)

type Decision struct {
	RequestKey string
	Canonical  string
	Blast      string
	Action     rune
	Allow      bool
	Reason     string
}

// View owns selection and the unfinished confirmation. Pending requests stay
// with the caller until its decision succeeds.
type View struct {
	items       []model.InboxItem
	selected    string
	action      rune
	input       string
	idConfirmed bool
	inspect     bool
}

func (v *View) Update(items []model.InboxItem) {
	v.items = slices.Clone(items)
	if len(v.items) == 0 {
		v.selected = ""
		v.Cancel()
		return
	}
	if slices.IndexFunc(v.items, func(i model.InboxItem) bool { return i.Request.Key() == v.selected }) < 0 {
		v.selected = v.items[0].Request.Key()
		v.Cancel()
	}
}

func (v *View) Selected() string { return v.selected }

func (v *View) Current() (model.InboxItem, bool) {
	for _, item := range v.items {
		if item.Request.Key() == v.selected {
			return item, true
		}
	}
	return model.InboxItem{}, false
}

func (v *View) Move(delta int) {
	i := slices.IndexFunc(v.items, func(item model.InboxItem) bool { return item.Request.Key() == v.selected })
	if i < 0 {
		return
	}
	v.selected = v.items[max(0, min(len(v.items)-1, i+delta))].Request.Key()
	v.Cancel()
}

func (v *View) Begin(action rune) bool {
	if _, ok := v.Current(); !ok {
		return false
	}
	switch action {
	case 'a', 'A', 'd', 'r':
		v.action, v.input, v.inspect, v.idConfirmed = action, "", false, false
	case 'i':
		v.inspect = !v.inspect
	case 'x':
		v.Cancel()
	default:
		return false
	}
	return true
}

// Input adds typed text to the confirmation when it reads text: the
// canonical id of a hard gate, or a return reason. A plain confirmation
// reads no text, so a key typed into it is dropped, never kept unseen.
func (v *View) Input(s string) {
	if v.WantsText() {
		v.input += s
	}
}

// WantsText reports whether the confirmation in progress reads typed text.
func (v *View) WantsText() bool {
	if v.action == 0 {
		return false
	}
	if v.action == 'r' {
		return true
	}
	item, ok := v.Current()
	return ok && item.Gate == model.GateTyped && !v.idConfirmed
}
func (v *View) ClearInput()    { v.input = "" }
func (v *View) Backspace() {
	r := []rune(v.input)
	if len(r) > 0 {
		v.input = string(r[:len(r)-1])
	}
}
func (v *View) Cancel()          { v.action, v.input, v.inspect, v.idConfirmed = 0, "", false, false }
func (v *View) Confirming() bool { return v.action != 0 }

// Inspecting reports whether the selected item's reason is open.
func (v *View) Inspecting() bool { return v.inspect }

func (v *View) Confirm() (Decision, bool) {
	item, ok := v.Current()
	if !ok || v.action == 0 {
		return Decision{}, false
	}
	if item.Gate == model.GateTyped && !v.idConfirmed {
		if v.input != item.Canonical {
			return Decision{}, false
		}
		if v.action == 'r' {
			v.idConfirmed, v.input = true, ""
			return Decision{}, false
		}
	}
	if v.action == 'r' && strings.TrimSpace(v.input) == "" {
		return Decision{}, false
	}
	d := Decision{RequestKey: item.Request.Key(), Canonical: item.Canonical, Blast: Blast(item), Action: v.action, Allow: v.action == 'a' || v.action == 'A'}
	if v.action == 'r' {
		d.Reason = v.input
	}
	v.Cancel()
	return d, true
}

// Blast is the authority scope actually present on a permission request.
// The Board does not infer task, file or ref counts it has not observed.
func Blast(item model.InboxItem) string {
	return clean(item.Request.Object) + " · " + clean(item.Request.Scope)
}

func Reason(item model.InboxItem) string { return clean(item.Request.Reason) }

func (v *View) Render(width, height int, density layout.Density) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	lines := []string{fit(fmt.Sprintf(" Inbox  %d pending", len(v.items)), width)}
	if density == layout.Strip || height == 1 {
		return lines
	}
	visible := max(0, height-2)
	selected := slices.IndexFunc(v.items, func(item model.InboxItem) bool { return item.Request.Key() == v.selected })
	start := max(0, selected-visible+1)
	for _, item := range v.items[start:min(len(v.items), start+visible)] {
		mark := "  "
		if item.Request.Key() == v.selected {
			mark = "> "
		}
		lines = append(lines, fit(mark+item.Canonical+"  "+Blast(item), width))
	}
	if prompt := v.Prompt(); prompt != "" && len(lines) < height {
		lines = append(lines, fit(prompt, width))
	}
	return lines
}

// Prompt is the line under the selected item: the unfinished confirmation
// with what has been typed so far, the reason while inspecting, or the keys.
// It is empty when nothing is selected.
func (v *View) Prompt() string {
	item, ok := v.Current()
	switch {
	case !ok:
		return ""
	case v.action != 0:
		prompt := "Enter to confirm " + string(v.action)
		if item.Gate == model.GateTyped && !v.idConfirmed {
			prompt = "Type " + item.Canonical + " then Enter: " + v.input
		} else if v.action == 'r' {
			prompt = "Reason then Enter: " + v.input
		}
		return prompt
	case v.inspect:
		return "Reason: " + Reason(item)
	default:
		return "a allow · A run · d deny · i inspect · r return · x cancel"
	}
}

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
}

func fit(s string, width int) string {
	if uniseg.StringWidth(s) <= width {
		return s
	}
	var b strings.Builder
	graphemes := uniseg.NewGraphemes(s)
	n := 0
	for graphemes.Next() {
		g := graphemes.Str()
		w := uniseg.StringWidth(g)
		if n+w > width {
			break
		}
		b.WriteString(g)
		n += w
	}
	return b.String()
}
