package auth

import (
	"sync"
	"testing"
	"time"
)

func TestSessionTokensSurviveConcurrentManagers(t *testing.T) {
	dir := t.TempDir()
	const count = 12
	var workers sync.WaitGroup
	tokens := make(chan string, count)
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			token, _, err := NewManager(dir).CreateTokenWithTTL("mcp-session", KindMCPClient, []string{string(CapTaskRead), string(CapTaskExecute)}, time.Hour)
			if err != nil {
				t.Errorf("create session credential: %v", err)
				return
			}
			tokens <- token
		}()
	}
	workers.Wait()
	close(tokens)
	var authWorkers sync.WaitGroup
	for token := range tokens {
		authWorkers.Add(1)
		go func(tok string) {
			defer authWorkers.Done()
			if _, err := NewManager(dir).Authenticate(tok); err != nil {
				t.Errorf("concurrent launch lost credential: %v", err)
			}
		}(token)
	}
	authWorkers.Wait()
	records, err := NewManager(dir).ListTokens()
	if err != nil || len(records) != count {
		t.Fatalf("session credentials retained: %d, error %v", len(records), err)
	}
}
