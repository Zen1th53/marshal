package store

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestConcurrentClaimAndReadContention(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "concurrency.db")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID:            "PRJ-CONCURRENCY",
		Repository:    dir,
		DefaultBranch: "main",
		PackVersion:   "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	// Create 10 tasks
	taskCount := 10
	var tasks []model.Task
	for i := 0; i < taskCount; i++ {
		tasks = append(tasks, model.Task{
			ID:     fmt.Sprintf("TASK-CONC-%03d", i),
			Title:  fmt.Sprintf("Task %d", i),
			Status: model.TaskReady,
			Risk:   model.R1,
		})
	}
	if _, err := st.ImportTasks(ctx, tasks); err != nil {
		t.Fatal(err)
	}

	// Register 5 agents and sessions
	agentCount := 10
	var sessions []model.Session
	for i := 0; i < agentCount; i++ {
		agentID := fmt.Sprintf("agent-%d", i)
		err := st.RegisterAgent(ctx, model.Agent{
			ID:          agentID,
			ProjectID:   "PRJ-CONCURRENCY",
			DisplayName: agentID,
			Role:        model.RoleDeveloper,
			Status:      model.AgentActive,
		})
		if err != nil {
			t.Fatal(err)
		}

		sessID := fmt.Sprintf("sess-%d", i)
		sess, err := st.StartSession(ctx, model.SessionStart{
			ID:        sessID,
			AgentID:   agentID,
			ProjectID: "PRJ-CONCURRENCY",
			Branch:    "main",
			Worktree:  "/tmp/wt",
		})
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, sess)
	}

	// 20 concurrent goroutines competing to claim tasks and read state
	goroutines := 20
	var wg sync.WaitGroup
	var claimedCount int32
	var readCount int32
	var conflictCount int32

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			sess := sessions[workerID%len(sessions)]

			for i := 0; i < 20; i++ {
				taskIdx := rand.Intn(taskCount)
				taskID := fmt.Sprintf("TASK-CONC-%03d", taskIdx)

				// Attempt claim
				_, err := st.ClaimTask(ctx, model.ClaimRequest{
					TaskID:           taskID,
					AgentID:          sess.AgentID,
					SessionID:        sess.ID,
					ExpectedRevision: 0,
					ExpiresAt:        time.Now().UTC().Add(time.Minute),
				})
				if err == nil {
					atomic.AddInt32(&claimedCount, 1)
				} else {
					atomic.AddInt32(&conflictCount, 1)
				}

				// Concurrent read
				list, err := st.ListTasks(ctx)
				if err == nil && len(list) == taskCount {
					atomic.AddInt32(&readCount, 1)
				}
			}
		}(g)
	}

	wg.Wait()

	// Exactly taskCount (10) claims should have succeeded
	if claimedCount != int32(taskCount) {
		t.Fatalf("expected exactly %d successful claims, got %d", taskCount, claimedCount)
	}

	// PRAGMA integrity_check to verify zero corruption
	meta, err := VerifyBackup(ctx, dbPath, "PRJ-CONCURRENCY", 67)
	if err != nil {
		t.Fatalf("post-concurrency integrity verification failed: %v", err)
	}
	_ = meta
}

func TestConcurrentReadUnderHeavyWriteLoad(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "heavy_read.db")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	st, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID:            "PRJ-HEAVY",
		Repository:    dir,
		DefaultBranch: "main",
		PackVersion:   "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	var writeOps int64
	var readOps int64
	done := make(chan struct{})

	// Writer goroutines
	var wg sync.WaitGroup
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			var counter int
			for {
				select {
				case <-done:
					return
				default:
					counter++
					eventID := fmt.Sprintf("EV-HEAVY-%d-%d", id, counter)
					_ = st.AppendEvent(ctx, nil, model.Event{
						ID:                eventID,
						Type:              "TEST_EVENT",
						ProjectID:         "PRJ-HEAVY",
						AggregateRevision: 0,
						Timestamp:         time.Now().UTC(),
						Data:              map[string]any{"seq": counter},
					})
					atomic.AddInt64(&writeOps, 1)
					time.Sleep(2 * time.Millisecond)
				}
			}
		}(w)
	}

	// Reader goroutines
	for r := 0; r < 5; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_, err := st.ListEvents(ctx)
					if err == nil {
						atomic.AddInt64(&readOps, 1)
					}
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}

	time.Sleep(500 * time.Millisecond)
	close(done)
	wg.Wait()

	if writeOps == 0 || readOps == 0 {
		t.Fatalf("expected non-zero reads and writes, got writes=%d reads=%d", writeOps, readOps)
	}
}
