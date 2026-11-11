package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// boardSeen is the Board state remembered at the last shutdown. It exists only
// so a resumed run can say how many tasks finished while the owner was away; it
// is written atomically and read best-effort, so a missing or bad file simply
// drops the "since you left" clause.
type boardSeen struct {
	Run  string `json:"run"`
	Done int    `json:"done"`
	At   string `json:"at"`
}

// welcomeLine is the single plain-English sentence shown when a run is resumed.
// It is built from the Board so the store, not the Marshal, decides what it
// says, and it never carries a run, plan or instance id.
func welcomeLine(b boardmodel.Board, seen *boardSeen) string {
	var sb strings.Builder
	sb.WriteString("Welcome back.")

	if seen != nil && seen.Run == b.Run.ID {
		if k := b.Run.Done - seen.Done; k > 0 {
			fmt.Fprintf(&sb, " %d more %s finished since you left;", k, plural(k, "task", "tasks"))
		}
	}

	fmt.Fprintf(&sb, " %d of %d %s done", b.Run.Done, b.Run.Total, isAre(b.Run.Total))

	sb.WriteString(welcomeClause(b))
	sb.WriteString(".")
	return sb.String()
}

// welcomeClause is the first matching detail about where the run stands, in the
// order parked, in review, working, paused. It is empty when none applies.
func welcomeClause(b boardmodel.Board) string {
	working := 0
	var parked, review *boardmodel.TaskRow
	for i := range b.Tasks {
		t := &b.Tasks[i]
		switch t.State {
		case boardmodel.TaskParked:
			if parked == nil {
				parked = t
			}
		case boardmodel.TaskReview:
			if review == nil {
				review = t
			}
		case boardmodel.TaskWorking:
			working++
		}
	}
	switch {
	case parked != nil:
		return "; " + welcomeTaskName(*parked, false) + " needs you"
	case review != nil:
		return "; " + welcomeTaskName(*review, true) + " is under review"
	case working > 0:
		return fmt.Sprintf("; %d working", working)
	case b.Run.State == marshal.AwaitingUser:
		return "; the run is paused and waits for you"
	}
	return ""
}

// welcomeTaskName names a task for the welcome line. A task in review is named
// by its first scope file when it has one, since that is what the owner reviews;
// otherwise the task's title stands in.
func welcomeTaskName(t boardmodel.TaskRow, preferScope bool) string {
	var candidates []string
	if preferScope {
		candidates = append(candidates, t.Scope...)
	}
	candidates = append(candidates, t.Title)
	for _, c := range candidates {
		if name := welcomeSafeName(c); name != "" {
			return name
		}
	}
	return "a task"
}

const welcomeNameMaxRunes = 60

var (
	// welcomeIDPrefix and welcomeHexDigest are what must never reach the
	// owner: run, plan, instance and constitution ids, and a hex digest.
	welcomeIDPrefix  = regexp.MustCompile(`(?i)\b(RUN|PLAN|INSTANCE|CONST)[-_]`)
	welcomeHexDigest = regexp.MustCompile(`(?i)\b[0-9a-f]{7,}\b`)
)

// welcomeSafeName is a task title or scope path cleaned the way the Board
// cleans what it paints (control and format characters out) and passed
// through the shared redaction. A name that redaction would change, or that
// still carries an internal id or a hex digest, is unusable and yields "".
func welcomeSafeName(s string) string {
	s = strings.Join(strings.Fields(cleanBoard(s)), " ")
	if s == "" || RedactContent(s, nil) != s || welcomeIDPrefix.MatchString(s) {
		return ""
	}
	for _, m := range welcomeHexDigest.FindAllString(s, -1) {
		// A hex-only word with a digit is a digest; a plain word such as
		// "defaced" is not.
		if strings.ContainsAny(m, "0123456789") {
			return ""
		}
	}
	if r := []rune(s); len(r) > welcomeNameMaxRunes {
		s = strings.TrimSpace(string(r[:welcomeNameMaxRunes])) + "…"
	}
	return s
}

func isAre(total int) string {
	if total == 1 {
		return "task is"
	}
	return "tasks are"
}

func boardSeenPath(root string) string {
	return filepath.Join(root, statepath.DirName, "board-seen.json")
}

// loadBoardSeen reads the remembered Board state. Any failure returns nil: the
// welcome line then simply omits the "since you left" clause.
func loadBoardSeen(root string) *boardSeen {
	data, err := os.ReadFile(boardSeenPath(root))
	if err != nil {
		return nil
	}
	var s boardSeen
	if json.Unmarshal(data, &s) != nil {
		return nil
	}
	return &s
}

// writeBoardSeen records the Board's current run atomically (temp + rename),
// mode 0600, so the next launch can tell what finished since.
func writeBoardSeen(root string, b boardmodel.Board) error {
	seen := boardSeen{Run: b.Run.ID, Done: b.Run.Done, At: time.Now().UTC().Format(time.RFC3339)}
	data, err := json.Marshal(seen)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, statepath.DirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "board-seen-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, boardSeenPath(root))
}

// showWelcome prints the one "Welcome back" notice for a resumed run and writes
// it to Activity, at most once in this process.
func (w *Workspace) showWelcome(root string, b boardmodel.Board) {
	if b.Run.ID == "" {
		return
	}
	if !w.welcomeShown.CompareAndSwap(false, true) {
		return
	}
	line := welcomeLine(b, loadBoardSeen(root))
	w.memoryAnswer(line, false)
	w.RecordActivity(line)
}

// saveBoardSeen remembers the Board's current run on shutdown so the next
// launch can report what finished. Failures are ignored: the clause is optional.
func (w *Workspace) saveBoardSeen() {
	p := w.marshalPanel()
	if p == nil || p.Board.Run.ID == "" {
		return
	}
	root := w.providerRoot()
	if root == "" {
		return
	}
	_ = writeBoardSeen(root, p.Board)
}
