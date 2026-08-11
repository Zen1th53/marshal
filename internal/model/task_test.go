package model

import (
	"strings"
	"testing"
)

func TestDecodeTasksAcceptsOneObjectOrArray(t *testing.T) {
	one := `{"id":"TASK-001","title":"one","status":"ready","risk":"R1","revision":0}`
	tasks, err := DecodeTasks(strings.NewReader(one))
	if err != nil {
		t.Fatalf("object: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "TASK-001" {
		t.Fatalf("object result = %#v", tasks)
	}

	many := `[
		{"id":"TASK-001","title":"one","status":"ready","risk":"R1","revision":0},
		{"id":"TASK-002","title":"two","status":"proposed","risk":"R0","revision":2}
	]`
	tasks, err = DecodeTasks(strings.NewReader(many))
	if err != nil {
		t.Fatalf("array: %v", err)
	}
	if len(tasks) != 2 || tasks[1].ID != "TASK-002" {
		t.Fatalf("array result = %#v", tasks)
	}
}

func TestDecodeTasksRejectsUnknownFieldAndTrailingJSON(t *testing.T) {
	fixtures := []string{
		`{"id":"TASK-001","title":"one","status":"ready","risk":"R1","revision":0,"owner":"fake"}`,
		`{"id":"TASK-001","title":"one","status":"ready","risk":"R1","revision":0} {}`,
	}
	for _, fixture := range fixtures {
		if _, err := DecodeTasks(strings.NewReader(fixture)); err == nil {
			t.Fatalf("accepted invalid JSON: %s", fixture)
		}
	}
}

func TestTaskValidationRejectsInvalidSchemaValues(t *testing.T) {
	fixtures := []Task{
		{ID: "bad", Title: "x", Status: TaskReady, Risk: R1},
		{ID: "TASK-001", Status: TaskReady, Risk: R1},
		{ID: "TASK-001", Title: "x", Status: "complete", Risk: R1},
		{ID: "TASK-001", Title: "x", Status: TaskReady, Risk: "R9"},
		{ID: "TASK-001", Title: "x", Status: TaskReady, Risk: R1, Revision: -1},
		{ID: "TASK-001", Title: "x", Status: TaskReady, Risk: R1, Dependencies: []string{"TASK-001"}},
		{ID: "TASK-001", Title: "x", Status: TaskReady, Risk: R1, Dependencies: []string{"TASK-002", "TASK-002"}},
	}
	for i, task := range fixtures {
		if err := task.Validate(); err == nil {
			t.Errorf("fixture %d accepted: %#v", i, task)
		}
	}
}
