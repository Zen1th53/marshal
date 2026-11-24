package app

import (
	"context"
	"testing"
)

func TestGrantIdleTaskAcceptanceRefusesWhileWriterHeld(t *testing.T) {
	s := &MarshalService{Repository: "idle-accept", ProjectID: "project"}
	w := s.runWriter("RUN-held")
	w.mu.Lock()
	granted := false
	_, err := s.GrantIdleTaskAcceptance(context.Background(), "RUN-held", "T.1", "", func(string) { granted = true })
	w.mu.Unlock()
	if err == nil || granted {
		t.Fatalf("acceptance granted while the run writer was held: %v", err)
	}
	w.startExecution("execution-test")
	_, err = s.GrantIdleTaskAcceptance(context.Background(), "RUN-held", "T.1", "", func(string) { granted = true })
	w.endExecution()
	if err == nil || granted {
		t.Fatalf("acceptance granted during an active execution: %v", err)
	}
}
