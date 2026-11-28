package chattransport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Durable records intent before sending. An interrupted send is uncertain and
// must be reconciled by the owner, never automatically replayed into a provider
// that offers no idempotency contract.
type Durable struct {
	Transport
	mu       sync.Mutex
	path     string
	state    outbox
	active   string
	prepared string
	events   chan Event
	done     chan struct{}
	once     sync.Once
}
type outbox struct {
	Version   int
	Pending   *Turn
	Completed []string
}

func NewTurn(text string) Turn {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return Turn{ID: hex.EncodeToString(b[:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" + hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:]), Text: text}
}
func Persisted(tr Transport, path string) (*Durable, error) {
	d := &Durable{Transport: tr, path: path, state: outbox{Version: 1}, events: make(chan Event, 512), done: make(chan struct{})}
	data, err := os.ReadFile(path)
	if err == nil {
		var saved outbox
		if err = json.Unmarshal(data, &saved); err != nil {
			return nil, fmt.Errorf("read chat outbox: %w", err)
		}
		if saved.Version != 1 {
			return nil, errors.New("unsupported chat outbox version")
		}
		if saved.Pending != nil && (saved.Pending.ID == "" || saved.Pending.Text == "") {
			return nil, errors.New("invalid pending chat message")
		}
		d.state = saved
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if tr != nil {
		go d.pump()
	}
	return d, nil
}
func (d *Durable) Events() <-chan Event { return d.events }
func (d *Durable) Pending() *Turn {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.Pending == nil {
		return nil
	}
	t := *d.state.Pending
	return &t
}

// Resolve acknowledges a pending message after the owner checked provider
// history. It does not send anything; resubmission must be a new owner action.
func (d *Durable) Resolve(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active != "" || d.prepared != "" {
		return errors.New("message is still active; switch to tmux before reconciling")
	}
	if d.state.Pending == nil || d.state.Pending.ID != id {
		return errors.New("pending message ID does not match")
	}
	return d.complete(id)
}
func (d *Durable) save() error {
	if err := os.MkdirAll(filepath.Dir(d.path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(d.state)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(d.path), ".chat-outbox-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), d.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(d.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// Prepare persists on the caller's thread before an asynchronous send is queued.
func (d *Durable) Prepare(t Turn) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.prepare(t)
}
func (d *Durable) prepare(t Turn) error {
	if t.ID == "" {
		return errors.New("owner message requires an ID")
	}
	for _, id := range d.state.Completed {
		if id == t.ID {
			return nil
		}
	}
	if d.state.Pending != nil {
		if d.state.Pending.ID == t.ID && (d.active == t.ID || d.prepared == t.ID) {
			if d.state.Pending.Text != t.Text {
				return errors.New("message ID already belongs to different text")
			}
			return nil
		}
		return fmt.Errorf("message %s has uncertain or pending delivery; /view chat pending shows its saved text; check provider history before /view chat resolve %s", d.state.Pending.ID, d.state.Pending.ID)
	}
	d.state.Pending = &t
	if err := d.save(); err != nil {
		d.state.Pending = nil
		return err
	}
	d.prepared = t.ID
	return nil
}
func (d *Durable) Send(ctx context.Context, t Turn) error {
	d.mu.Lock()
	if err := d.prepare(t); err != nil {
		d.mu.Unlock()
		return err
	}
	if d.prepared != t.ID {
		d.mu.Unlock()
		return nil
	} // active or completed retry
	d.prepared = ""
	d.active = t.ID
	d.mu.Unlock()
	// Drain provider events while stdin is backpressured. The durable intent
	// already fences concurrent retries before crossing the process boundary.
	if err := d.Transport.Send(ctx, t); err != nil {
		d.mu.Lock()
		if d.active == t.ID {
			d.active = ""
		}
		d.mu.Unlock()
		return fmt.Errorf("message %s retained in outbox; delivery uncertain: %w", t.ID, err)
	}
	return nil
}
func (d *Durable) complete(id string) error {
	old := d.state
	d.state.Pending = nil
	d.state.Completed = append(append([]string(nil), d.state.Completed...), id)
	// Retain the deduplication ledger across restarts.
	if err := d.save(); err != nil {
		d.state = old
		return err
	}
	return nil
}
func (d *Durable) pump() {
	defer close(d.events)
	for {
		select {
		case <-d.done:
			return
		case e, ok := <-d.Transport.Events():
			if !ok {
				return
			}
			d.mu.Lock()
			if d.active != "" && (e.TurnID == "" || e.TurnID == d.active) {
				e.TurnID = d.active
				if e.Kind == EventResult {
					if err := d.complete(d.active); err != nil {
						e.IsError = true
						e.Text = "complete chat outbox: " + err.Error()
					}
					d.active = ""
				} else if e.Kind == EventExit {
					d.active = ""
				}
			}
			d.mu.Unlock()
			select {
			case d.events <- e:
			case <-d.done:
				return
			}
		}
	}
}
func (d *Durable) Ask(ctx context.Context, a PermissionAsk) (Decision, error) {
	if broker, ok := d.Transport.(interface {
		Ask(context.Context, PermissionAsk) (Decision, error)
	}); ok {
		return broker.Ask(ctx, a)
	}
	return Decision{}, errors.New("provider has no structured permission broker")
}
func (d *Durable) Close() error {
	d.once.Do(func() { close(d.done) })
	if d.Transport != nil {
		return d.Transport.Close()
	}
	return nil
}
