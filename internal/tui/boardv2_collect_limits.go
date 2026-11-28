package tui

import (
	"github.com/Zen1th53/marshal/internal/board/vm"
	"github.com/Zen1th53/marshal/internal/usage"
)

// buildV2Limits is every provider's limits as rows, one per model family and
// window, with the provider's dispatch state and the rows that decide it. It
// reads the same last usage reading the Activity "Limits:" line and the
// Workers pane read (board_limits.go, boardv2_collect_marshal.go), and the
// same per-model rule the dispatcher applies (usage.Quota.ModelBinding), so
// a provider is "out" here exactly when the model its workers run has no
// limit left. Nothing is estimated: a limit not reported is -1 with a source
// that says why.
func buildV2Limits(in v2MarshalIn) []vm.ProviderLimits {
	out := make([]vm.ProviderLimits, 0, len(v2ProviderOrder))
	for _, name := range v2ProviderOrder {
		p := in.provider(name)
		q, read := in.Quotas[name]
		l := vm.ProviderLimits{Provider: name, Model: p.ModelID, Reason: p.Reason, Rows: limitRows(q, read)}
		switch p.State {
		case "in use", "ready":
			l.State = vm.LimitUsable
		case "limit":
			l.State = vm.LimitOut
		case "not signed in":
			l.State = vm.LimitNotSignedIn
		case "not installed":
			l.State = vm.LimitNotInstalled
		case "checking":
			l.State, l.Reason = vm.LimitNotChecked, name+" is being checked"
		default:
			l.State = vm.LimitNotChecked
		}
		markDrivingRows(&l, p, q)
		out = append(out, l)
	}
	return out
}

// limitRows is a quota as rows. read is false before the first reading.
func limitRows(q usage.Quota, read bool) []vm.LimitRow {
	if !read {
		return []vm.LimitRow{{PercentLeft: -1, Source: usage.SourceNotChecked}}
	}
	if q.Reason == usage.ReasonNotSignedIn {
		return []vm.LimitRow{{PercentLeft: -1, Source: q.Source()}}
	}
	var rows []vm.LimitRow
	for _, win := range q.Windows {
		family, kind := usage.SplitLabel(win.Label)
		row := vm.LimitRow{Family: family, Window: usage.KindName(kind), PercentLeft: -1, ObservedAt: q.AsOf, Source: usage.SourceNotReported}
		if q.Known && win.Known() {
			row.PercentLeft, row.ResetsAt, row.Source = usageLeft(win), win.ResetsAt, q.Source()
		} else if !q.Known {
			row.Source = q.Source()
		}
		rows = append(rows, row)
	}
	if len(rows) > 0 {
		return rows
	}
	// agy without /usage figures: which families it offers is all there is.
	for _, g := range q.Offer {
		src := "agy models: offered"
		if !g.Offered {
			src = "agy models: not offered now"
		}
		rows = append(rows, vm.LimitRow{Family: g.Name, PercentLeft: -1, ObservedAt: q.AsOf, Source: src})
	}
	if len(rows) > 0 {
		return rows
	}
	return []vm.LimitRow{{PercentLeft: -1, Source: q.Source()}}
}

// markDrivingRows marks the rows that decide l.State: for a provider out of
// limit, every row that limits its model at 0% (or, when it offers no model
// family at all, every offer row); for a usable one, the model's tightest
// window. A cooldown the provider itself reported, sign-in and install
// states are not decided by a row.
func markDrivingRows(l *vm.ProviderLimits, p v2Provider, q usage.Quota) {
	if p.State == "limit" && q.OfferedNone() && len(q.Windows) == 0 {
		for i := range l.Rows {
			l.Rows[i].Drives = true
		}
		return
	}
	if p.Left < 0 {
		return
	}
	// The rows that limit the model, by the rule dispatch applies; every row
	// when no row can be placed for it.
	limiting := map[string]bool{}
	wins, matched := q.ModelWindows(p.ModelID)
	for _, w := range wins {
		family, kind := usage.SplitLabel(w.Label)
		limiting[family+"|"+usage.KindName(kind)] = true
	}
	applies := func(row vm.LimitRow) bool { return !matched || limiting[row.Family+"|"+row.Window] }
	switch {
	case p.State == "limit" && p.Left == 0:
		family, kind := usage.SplitLabel(p.Window.Label)
		for i, row := range l.Rows {
			if row.PercentLeft == 0 && (applies(row) || row.Family == family && row.Window == usage.KindName(kind)) {
				l.Rows[i].Drives = true
			}
		}
	case l.State == vm.LimitUsable:
		family, kind := usage.SplitLabel(p.Window.Label)
		for i, row := range l.Rows {
			if row.Family == family && row.Window == usage.KindName(kind) && row.PercentLeft == p.Left {
				l.Rows[i].Drives = true
				return
			}
		}
	}
}

// workerModelID is the model id a provider's workers run: its verified
// profile's default, else the model its usage report or CLI named.
func (in v2MarshalIn) workerModelID(name string) string {
	return firstNonEmpty(in.Providers[name].Model, in.Quotas[name].Model, in.Current[name])
}

// modelBinding is the window that decides whether a provider can run model:
// the model's tightest known window, or, when no window can be placed for
// that model, the provider's tightest — the rule dispatch applies
// (app.MarshalService.ModelQuotaLeft falls back to QuotaLeft the same way).
func modelBinding(q usage.Quota, model string) (usage.Window, bool) {
	if win, ok := q.ModelBinding(model); ok {
		return win, true
	}
	return q.Binding()
}

// modelQuota is q with only the windows that limit model, when its model
// binding can be placed (the rule modelBinding applies); q otherwise. The
// Workers row's "wk N%" and the readiness check read it, so a spent family
// the workers do not run is not shown as their weekly figure.
func modelQuota(q usage.Quota, model string) usage.Quota {
	if _, ok := q.ModelBinding(model); !ok {
		return q
	}
	q.Windows, _ = q.ModelWindows(model)
	return q
}
