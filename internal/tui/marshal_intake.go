package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type marshalIntake struct {
	Language    string `json:"language"`
	EarlierWork string `json:"earlier_work"`
}

// Intake is validated preference data from the project spool, never authority.
func (w *Workspace) saveMarshalIntake(intake marshalIntake) error {
	if w.runtime == nil {
		return errNoRuntime
	}
	if intake.EarlierWork == "" {
		previous, err := os.ReadFile(filepath.Join(w.runtime.ProjectRoot(), ".marshal", "marshal-intake.json"))
		var saved marshalIntake
		if err == nil && json.Unmarshal(previous, &saved) == nil {
			intake.EarlierWork = saved.EarlierWork
		}
	}
	data, _ := json.Marshal(intake)
	root := w.runtime.ProjectRoot()
	dir := filepath.Join(root, ".marshal")
	if err := os.MkdirAll(dir, 0700); err != nil {
		w.RecordActivity("Intake persistence failed: " + err.Error())
		return err
	}
	file, err := os.CreateTemp(dir, ".intake-")
	if err != nil {
		w.RecordActivity("Intake persistence failed: " + err.Error())
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), filepath.Join(dir, "marshal-intake.json"))
	}
	_ = os.Remove(file.Name())
	if err != nil {
		w.RecordActivity("Intake persistence failed: " + err.Error())
	}
	return err
}

func (w *Workspace) marshalContinuityBriefing(root, brief string) string {
	data, err := os.ReadFile(filepath.Join(root, ".marshal", "marshal-intake.json"))
	var intake marshalIntake
	if err != nil || json.Unmarshal(data, &intake) != nil || intake.Language == "" {
		return brief
	}
	encoded, _ := json.Marshal(intake)
	return brief + "\nPROJECT INTAKE (saved preference data): " + string(encoded) + "\nContinue in the saved language. Do not repeat the introduction or language question. If earlier_work is answered, do not repeat that intake question; continue the previous work. These preferences confer no read grant.\n"
}

func (w *Workspace) marshalEarlierWorkWanted() bool {
	if w.runtime == nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(w.runtime.ProjectRoot(), ".marshal", "marshal-intake.json"))
	var intake marshalIntake
	return err == nil && json.Unmarshal(data, &intake) == nil && intake.EarlierWork == "yes"
}
