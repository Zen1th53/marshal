package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
)

// Earlier releases saved an accepted thermal lowering as the project's
// global-cap setting and asked for these objects and scopes.
const (
	legacyThermalObject = "/marshal settings global-cap "
	legacyThermalScope  = "this project, next Marshal run"
	legacyThermalReason = "halve lanes"
)

// thermalRepairStore is the audit history needed to identify suspect caps.
type thermalRepairStore interface {
	GetMarshalSettings(ctx context.Context, projectID string) (store.MarshalRecord[marshal.Settings], error)
	MarshalSettingsChanges(ctx context.Context, projectID string) ([]store.SettingChange, error)
	ListEvents(ctx context.Context) ([]model.Event, error)
}

// repairLegacyThermalCap reports a global-cap that an earlier release may
// have saved for the thermal guard. Legacy receipts do not link the settings
// write to its permission decision, so matching values and timestamps cannot
// authorize a restoration. Keep ambiguous values and let the user review them;
// a later, distinct global-cap edit is kept without a notice.
func repairLegacyThermalCap(ctx context.Context, st thermalRepairStore, projectID string) (restored bool, notice string, err error) {
	rec, err := st.GetMarshalSettings(ctx, projectID)
	if err != nil {
		return false, "", err
	}
	cur := projectGlobalCap(rec.Value)
	if cur == 0 {
		return false, "", nil
	}
	all, err := st.MarshalSettingsChanges(ctx, projectID)
	if err != nil {
		return false, "", err
	}
	var caps []store.SettingChange // newest first
	for _, c := range all {
		if c.Key == "global-cap" {
			caps = append(caps, c)
		}
	}
	events, err := st.ListEvents(ctx)
	if err != nil {
		return false, "", err
	}
	var lowered []time.Time // owner-accepted thermal lowerings to this value
	for _, e := range events {
		if e.Type != "PERMISSION_DECIDED" || e.ProjectID != projectID || e.Data["requester"] != hostWatchActor || e.Data["allow"] != true {
			continue
		}
		object, _ := e.Data["object"].(string)
		scope, _ := e.Data["scope"].(string)
		reason, _ := e.Data["reason"].(string)
		if object == legacyThermalObject+strconv.Itoa(cur) && strings.HasPrefix(scope, legacyThermalScope) && strings.Contains(reason, legacyThermalReason) {
			lowered = append(lowered, e.Timestamp)
		}
	}
	if len(lowered) == 0 {
		return false, "", nil
	}
	notice = fmt.Sprintf("global-cap %d may be a lane cap the thermal guard saved in an earlier session; check it with /marshal settings global-cap", cur)
	if len(caps) == 0 || caps[0].New != strconv.Itoa(cur) {
		// No change record shows what the lowering replaced (releases
		// before settings changes were audited); a decision after the last
		// recorded change makes the value ambiguous.
		for _, at := range lowered {
			if len(caps) == 0 || at.After(caps[0].At) {
				return false, notice, nil
			}
		}
		return false, "", nil
	}
	latest := caps[0]
	thermal := false
	for _, at := range lowered {
		// The decision must come after the previous global-cap change and
		// before the change that set this value.
		if !at.After(latest.At) && (len(caps) == 1 || at.After(caps[1].At)) {
			thermal = true
		}
	}
	if !thermal {
		return false, "", nil
	}
	// Even a decision immediately before this receipt may have been followed
	// by a crash and an independent settings command. The receipt records no
	// decision identity, so it cannot prove which action wrote this value.
	return false, notice, nil
}

// thermalRepairNoticePath remembers the settings revision whose repair
// notice the owner has already seen.
func thermalRepairNoticePath(root string) string {
	return filepath.Join(root, statepath.DirName, "thermal-cap-notice")
}

// thermalRepairOnce reports notice once per settings revision.
func thermalRepairOnce(root string, revision int64, notice string) string {
	if notice == "" || root == "" {
		return ""
	}
	path := thermalRepairNoticePath(root)
	if statepath.CheckPath(root, path) != nil {
		return ""
	}
	mark := strconv.FormatInt(revision, 10)
	if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) == mark {
		return ""
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err := os.WriteFile(path, []byte(mark+"\n"), 0o600); err != nil {
		return ""
	}
	return notice
}

// repairThermalCap runs the legacy repair for this workspace's project and
// returns what the Host log and notice should say.
func (w *Workspace) repairThermalCap(ctx context.Context) string {
	if w.runtime == nil {
		return ""
	}
	service := w.runtime.Marshal()
	if service == nil || service.Store == nil {
		return ""
	}
	restored, notice, err := repairLegacyThermalCap(ctx, service.Store, service.ProjectID)
	if err != nil {
		return ""
	}
	if restored {
		return "restored global-cap saved by the thermal guard in an earlier session"
	}
	if notice == "" {
		return ""
	}
	rec, err := service.Store.GetMarshalSettings(ctx, service.ProjectID)
	if err != nil {
		return ""
	}
	return thermalRepairOnce(w.providerRoot(), rec.Revision, notice)
}
