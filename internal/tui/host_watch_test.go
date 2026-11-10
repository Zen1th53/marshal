package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/hostwatch"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// fakeClock is the watcher's and the fake sampler's time.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) tick()          { c.mu.Lock(); c.t = c.t.Add(time.Second); c.mu.Unlock() }

// fakeHost is a sampler whose readings the test sets.
type fakeHost struct {
	mu    sync.Mutex
	clock *fakeClock
	n     int
	pkg   float64
	free  uint64 // root free bytes
	avail uint64 // MemAvailable bytes
	reads int
}

const gibBytes = 1 << 30

func newFakeHost(c *fakeClock) *fakeHost {
	return &fakeHost{clock: c, pkg: 66, free: 116e9, avail: 419 * gibBytes / 10}
}

func (f *fakeHost) set(fn func(*fakeHost)) { f.mu.Lock(); fn(f); f.mu.Unlock() }

func (f *fakeHost) Read(context.Context) (hostwatch.Sample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	f.n++
	n := f.n
	jitter := float64((n*7)%5) - 2
	pkg := f.pkg + jitter
	s := hostwatch.Sample{
		At:                     f.clock.now(),
		CPUModelName:           "Intel(R) Core(TM) i7-14700K",
		CPUCores:               20,
		CPUThreads:             28,
		CPULoadAvg1:            2.65,
		CPULoadAvg5:            2.79,
		CPULoadAvg15:           2.82,
		CPULoadAvgKnown:        true,
		CPUAverageFreqMHz:      4200,
		CPUAverageFreqMHzKnown: true,
		// One throttle event every other second while hot.
		CPUPackageThrottleCount: 234751 + int64(n/2),
		CPUPackageThrottleKnown: true,
		CPUPackageTempC:         pkg,
		CPUPackageTempCKnown:    true,
		CPUCoreTempC:            map[string]float64{},
		MemTotalBytes:           62 * gibBytes,
		MemTotalBytesKnown:      true,
		MemAvailableBytes:       f.avail,
		MemAvailableBytesKnown:  true,
		MemCachedBytes:          46 * gibBytes,
		MemCachedBytesKnown:     true,
		SwapTotalBytes:          64 * gibBytes,
		SwapTotalBytesKnown:     true,
		SwapFreeBytes:           621 * gibBytes / 10,
		SwapFreeBytesKnown:      true,
		MemPressureSomeKnown:    true,
		MemPressureFullKnown:    true,
		DiskMounts:              []hostwatch.DiskMountUsage{{MountPoint: "/", TotalBytes: 1806e9, FreeBytes: f.free, UsedBytes: 1806e9 - f.free, Known: true}},
		NVMeTempC:               map[string]float64{"hwmon2 (Composite)": 48.9, "hwmon3 (Composite)": 50.9},
		NetInterface:            "eno1",
		NetRxBytes:              9_000_000_000 + uint64(n)*120_000,
		NetRxBytesKnown:         true,
		NetTxBytes:              2_000_000_000 + uint64(n)*12_000,
		NetTxBytesKnown:         true,
		GPUs:                    []hostwatch.GPUSample{{Name: "NVIDIA GeForce RTX 4060 Ti", TempC: 53, UtilizationPercent: 33, MemoryUsedBytes: 600 << 20, MemoryTotalBytes: 16380 << 20}},
	}
	for i := range 20 {
		s.CPUCoreTempC["Core "+strconv.Itoa(i*4)] = pkg - 4 + float64((i*3)%7)
	}
	return s, nil
}

var hostEpoch = time.Date(2026, 11, 2, 14, 2, 31, 0, time.Local)

func testWatcher(t testing.TB, sink laneCapSink) (*hostWatcher, *fakeHost, *fakeClock) {
	t.Helper()
	c := &fakeClock{t: hostEpoch.Add(-3 * time.Minute)}
	f := newFakeHost(c)
	h := newHostWatcher(f)
	h.now = c.now
	h.facts = func() hostFacts {
		return hostFacts{Hostname: "Asusworkstation", OS: "Arch Linux", Kernel: "7.2.11-arch1-1", BootAt: hostEpoch.Add(-8*time.Hour - 3*time.Minute), Iface: "eno1", IP: "192.168.3.21", MaxGHz: 5.6, Swap: "zram 64 GiB", Storage: "2 × Lexar SSD NM610 PRO 2TB 2.0 TB NVMe", CacheDir: "~/.cache/go-build"}
	}
	h.cacheSize = func(context.Context) (uint64, bool) { return 24e9, true }
	if sink != nil {
		h.guard = &hostLaneGuard{sink: sink}
	}
	return h, f, c
}

// steps samples n times, a second apart.
func steps(h *hostWatcher, c *fakeClock, n int) {
	for range n {
		c.tick()
		h.step(context.Background())
	}
	h.wg.Wait()
}

func logHas(h *hostWatcher, kind, part string) bool {
	for _, e := range h.snapshot().Log {
		if e.Kind == kind && strings.Contains(e.Msg, part) {
			return true
		}
	}
	return false
}

// demoHost is the watcher the Host view goldens paint: a hot spell that
// tripped the thermal guard and released it, with / at 116 GB free.
func demoHost(t *testing.T) hostSnapshot {
	sink := &fakeCapSink{cap: 12, accept: true}
	h, f, c := testWatcher(t, sink)
	h.addLog("info", "watching every 1 s · GPU every 5 s · policy below")
	steps(h, c, 110)
	f.set(func(f *fakeHost) { f.pkg = 95 })
	steps(h, c, 40)
	f.set(func(f *fakeHost) { f.pkg = 66 })
	steps(h, c, 30)
	if c.now() != hostEpoch {
		t.Fatalf("clock %v", c.now())
	}
	return h.snapshot()
}

func TestHostChipTextAndColours(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("BOARD_TRUECOLOR", "")
	v := boardVisual{theme: NewTheme(ThemeDefault, true, true)}
	cases := []struct {
		name string
		c    hostChip
		want []string
		not  []string
	}{
		{"cool", hostChip{Temp: 65, TempKnown: true, DiskPct: 60, DiskKnown: true}, []string{"\x1b[38;5;243mhost", "\x1b[38;5;114m65°C", "\x1b[38;5;243mdisk 60%"}, []string{"GUARD"}},
		{"warm", hostChip{Temp: 74, TempKnown: true, DiskPct: 60, DiskKnown: true}, []string{"\x1b[38;5;179m74°C"}, nil},
		{"hot", hostChip{Temp: 84, TempKnown: true, DiskPct: 60, DiskKnown: true}, []string{"\x1b[38;5;179m84°C"}, nil},
		{"guard", hostChip{Temp: 92, TempKnown: true, Guard: true, DiskPct: 94, DiskKnown: true, LowDisk: true}, []string{"\x1b[38;5;203m92°C", "\x1b[38;5;203mGUARD", "\x1b[38;5;179mdisk 94%"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := v.hostChip(tc.c)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("chip %q lacks %q", got, w)
				}
			}
			for _, w := range tc.not {
				if strings.Contains(got, w) {
					t.Errorf("chip %q has %q", got, w)
				}
			}
		})
	}
	snap := demoHost(t)
	c := snap.chip()
	if !c.TempKnown || c.Guard || c.LowDisk || c.DiskPct != 94 {
		t.Fatalf("chip from snapshot = %+v", c)
	}
	if got := StripANSI(v.hostChip(c)); !strings.HasPrefix(got, "host ") || !strings.HasSuffix(got, "°C disk 94%") {
		t.Fatalf("plain chip %q", got)
	}
}

// The host chip stays when the header is narrow: the clock, the budget
// and the run's other parts give way first, and the chip's compact form
// keeps a low disk. At 100 columns or fewer it shows only while the host
// is warm or guarded (owner decision 2026-11-06).
func TestHostChipKeptWhenHeaderNarrow(t *testing.T) {
	now := time.Date(2026, 11, 2, 14, 2, 31, 0, time.UTC)
	snap := demoHost(t)
	v := boardVisual{board: demoBoard(now), now: now, theme: NewTheme(ThemeNoColor, true, true), edition: "Marshal", host: &snap}
	wide := v.header(220)
	if !strings.Contains(wide, "host ") || !strings.Contains(wide, "disk 94%") || !strings.Contains(wide, "14:02:31") {
		t.Fatalf("wide header lacks chip or clock: %q", wide)
	}
	_, board, _ := layout.SplitWidths(120)
	for _, w := range []int{135, 120, 100, 80, board} {
		narrow := v.header(w)
		if w > 100 && (!strings.Contains(narrow, "host ") || !strings.Contains(narrow, "disk 94%")) {
			t.Fatalf("header %d dropped the chip: %q", w, narrow)
		}
		if w <= 100 && strings.Contains(narrow, "host") {
			t.Fatalf("header %d shows a cool host: %q", w, narrow)
		}
		if strings.Contains(narrow, "14:02:31") {
			t.Fatalf("header %d kept the clock before the chip: %q", w, narrow)
		}
	}
	// A warm host shows on a narrow header too, with the run's counts.
	sink := &fakeCapSink{cap: 12, accept: true}
	h, f, c := testWatcher(t, sink)
	f.set(func(f *fakeHost) { f.pkg = 76 })
	steps(h, c, 5)
	warm := h.snapshot()
	v.host = &warm
	for _, w := range []int{100, 80} {
		narrow := v.header(w)
		if !strings.Contains(narrow, "host 7") || !strings.Contains(narrow, "°C") || !strings.Contains(narrow, "12/53") {
			t.Fatalf("header %d hides a warm host or the done count: %q", w, narrow)
		}
	}
}

func TestHostViewGoldens(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("BOARD_TRUECOLOR", "")
	now := hostEpoch
	snap := demoHost(t)
	b := demoBoard(now)
	th := NewTheme(ThemeDefault, true, true)
	for _, tc := range []struct {
		name string
		w, h int
	}{{"split-120x40", 120, 40}, {"split-80x24", 80, 24}} {
		t.Run(tc.name, func(t *testing.T) {
			v := boardVisual{board: b, now: now, theme: th, frame: anim.Frame{}, edition: "Marshal", health: map[string]string{"codex": "●", "claude": "●", "agy": "◐", "opencode": "●"}, host: &snap, hostView: true}
			ui := &boardUI{}
			frame := paintHost(layout.Compute(tc.w, tc.h, layout.Split), th, v, ui)
			got := strings.Join(frame, "\n") + "\n"
			path := filepath.Join("testdata", "board", "host-"+tc.name+".golden")
			if *updateBoardGolden {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("%s differs; run with -update-board-golden", path)
			}
			if *writeBoardANS {
				out := filepath.Join("..", "..", "out", "host-"+strings.TrimPrefix(tc.name, "split-")+".ans")
				if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(out, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for i, line := range frame {
				if n := VisibleLen(line); n != tc.w {
					t.Errorf("row %d width %d, want %d", i, n, tc.w)
				}
			}
			plain := StripANSI(got)
			order := []string{"HOST", "CPU", "MEMORY", "DISK", "GPU", "NETWORK", "WATCH POLICY", "HOST LOG"}
			if tc.w < layout.TwoColumns {
				// One column scrolls: the top shows the first sections and
				// says how much more there is.
				order = order[:2]
				if !strings.Contains(plain, "j/k scroll") {
					t.Error("narrow Host view does not say it scrolls")
				}
			}
			at := 0
			for _, s := range order {
				i := strings.Index(plain[at:], "\n"+s+" ")
				if i < 0 && !strings.Contains(plain[at:], "  "+s+" ") {
					t.Errorf("section %s missing or out of order", s)
					continue
				}
				if i >= 0 {
					at += i
				}
			}
		})
	}
	// Scrolling the narrow view reaches the policy and the log.
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true), host: &snap, hostView: true}
	lines, maxScroll := v.hostRender(snap, 80, 22, 1000)
	text := strings.Join(lines, "\n")
	if maxScroll == 0 || !strings.Contains(text, "WATCH POLICY") || !strings.Contains(text, "HOST LOG") {
		t.Fatalf("scrolled to %d:\n%s", maxScroll, text)
	}
}

func TestHostViewContent(t *testing.T) {
	snap := demoHost(t)
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true)}
	text := strings.Join(v.hostLines(snap, 160, 60), "\n")
	for _, want := range []string{
		"Asusworkstation · Arch Linux · kernel 7.2.11-arch1-1 · up 8h03m · eno1 192.168.3.21",
		"Intel Core i7-14700K · 20 cores / 28 threads · max 5.6 GHz",
		"62 GiB · swap: zram 64 GiB",
		"NVIDIA GeForce RTX 4060 Ti 16 GB",
		"last 40 s · marks: resume 85, guard 90, crit 100",
		"4.2 GHz avg",
		"since boot",
		"/ 94% used · 116 GB free",
		"24 GB · ~/.cache/go-build",
		"RTX 4060 Ti · 53 °C · 33% busy",
		"eno1 192.168.3.21",
		"last minute",
		"CPU package > 90 °C (30 s avg) OK",
		"root free < 10 GB or 5% OK",
		"available < 8 GiB OK",
		"what Marshal noticed and did",
		"GUARD · package 30 s avg",
		"asked owner: lane cap 12 → 6 (inbox)",
		"RELEASE · package 30 s avg",
		"asked owner: restore lane cap 6 → default (inbox)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Host view lacks %q", want)
		}
	}
	ascii := hostASCII(v.hostLines(snap, 100, 60), 100)
	for i, l := range ascii {
		for _, r := range l {
			if r > 127 {
				t.Fatalf("ASCII line %d has %q: %s", i, r, l)
			}
		}
	}
}

func TestHostPolicyEdgesLogAndNotice(t *testing.T) {
	h, f, c := testWatcher(t, nil)
	steps(h, c, 5)
	if s := h.snapshot(); s.Notice != "" || len(s.Log) != 0 {
		t.Fatalf("calm host logged %+v notice %q", s.Log, s.Notice)
	}
	f.set(func(f *fakeHost) { f.free = 4e9 })
	steps(h, c, 3)
	if !logHas(h, "disk", "Disk almost full on /: 4 GB left") {
		t.Fatalf("no disk trigger in log: %+v", h.snapshot().Log)
	}
	if n := h.snapshot().Notice; n != "Disk almost full: 4 GB left. Marshal keeps running." {
		t.Fatalf("notice %q", n)
	}
	// Edge-triggered: staying low adds nothing.
	before := len(h.snapshot().Log)
	steps(h, c, 5)
	if got := len(h.snapshot().Log); got != before {
		t.Fatalf("log grew from %d to %d while the rule stayed active", before, got)
	}
	f.set(func(f *fakeHost) { f.free = 200e9; f.avail = 4 * gibBytes })
	steps(h, c, 2)
	if !logHas(h, "disk", "Disk space is fine again") || !logHas(h, "memory", "Available memory 4.00 GiB < threshold 8.00 GiB") {
		t.Fatalf("log %+v", h.snapshot().Log)
	}
	if n := h.snapshot().Notice; !strings.Contains(n, "Available memory") {
		t.Fatalf("notice %q", n)
	}
	f.set(func(f *fakeHost) { f.avail = 40 * gibBytes; f.pkg = 96 })
	steps(h, c, 35)
	if !logHas(h, "thermal", "GUARD · package 30 s avg") || !h.snapshot().Active[ruleThermal] {
		t.Fatalf("no thermal trigger: %+v", h.snapshot().Log)
	}
	if !strings.Contains(h.snapshot().Notice, "thermal guard") {
		t.Fatalf("notice %q", h.snapshot().Notice)
	}
	f.set(func(f *fakeHost) { f.pkg = 60 })
	steps(h, c, 35)
	if !logHas(h, "thermal", "RELEASE · package 30 s avg") || h.snapshot().Active[ruleThermal] {
		t.Fatalf("no thermal release: %+v", h.snapshot().Log)
	}
	if n := h.snapshot().Notice; n != "" {
		t.Fatalf("notice after every rule released: %q", n)
	}
	// The log keeps at most hostLogMax entries, newest first.
	for i := range hostLogMax + 10 {
		h.addLog("info", strconv.Itoa(i))
	}
	if l := h.snapshot().Log; len(l) != hostLogMax || l[0].Msg != strconv.Itoa(hostLogMax+9) {
		t.Fatalf("log len %d first %q", len(l), l[0].Msg)
	}
}

func TestHostLaneGuardLowersAndRestores(t *testing.T) {
	heat := func(t *testing.T, sink *fakeCapSink) (*hostWatcher, *fakeHost, *fakeClock) {
		h, f, c := testWatcher(t, sink)
		steps(h, c, 3)
		f.set(func(f *fakeHost) { f.pkg = 96 })
		steps(h, c, 35)
		return h, f, c
	}
	cool := func(h *hostWatcher, f *fakeHost, c *fakeClock) {
		f.set(func(f *fakeHost) { f.pkg = 60 })
		steps(h, c, 35)
	}
	t.Run("accepted then restored", func(t *testing.T) {
		sink := &fakeCapSink{cap: 12, project: 0, accept: true}
		h, f, c := heat(t, sink)
		if len(sink.requests) != 1 || sink.requests[0].To != 6 || sink.requests[0].From != 0 || sink.requests[0].RunID != "R-7f3a" || sink.requests[0].Restore {
			t.Fatalf("requests %+v", sink.requests)
		}
		if g := h.snapshot().Guard; !g.Active || g.Cap != 12 || g.Lowered != 6 {
			t.Fatalf("guard view %+v", g)
		}
		cool(h, f, c)
		if len(sink.requests) != 2 || !sink.requests[1].Restore || sink.requests[1].To != 0 || sink.requests[1].From != 6 {
			t.Fatalf("restore %+v", sink.requests)
		}
		if !logHas(h, "thermal", "asked owner: restore lane cap 6 → default") {
			t.Fatalf("log %+v", h.snapshot().Log)
		}
	})
	t.Run("released before a decision", func(t *testing.T) {
		sink := &fakeCapSink{cap: 8, project: 8}
		h, f, c := heat(t, sink)
		if len(sink.requests) != 1 || sink.requests[0].To != 4 || sink.requests[0].From != 8 {
			t.Fatalf("requests %+v", sink.requests)
		}
		cool(h, f, c)
		if len(sink.withdrawn) != 1 || len(sink.requests) != 1 {
			t.Fatalf("withdrawn %v requests %+v", sink.withdrawn, sink.requests)
		}
		if !logHas(h, "thermal", "lowering to 4 withdrawn before a decision") {
			t.Fatalf("log %+v", h.snapshot().Log)
		}
	})
	t.Run("declined", func(t *testing.T) {
		sink := &fakeCapSink{cap: 12, project: 0}
		h, f, c := heat(t, sink)
		delete(sink.pending, "req-1") // the owner said no
		cool(h, f, c)
		if len(sink.requests) != 1 || !logHas(h, "thermal", "lowering was not applied; nothing to restore") {
			t.Fatalf("requests %+v log %+v", sink.requests, h.snapshot().Log)
		}
	})
	t.Run("minimum two", func(t *testing.T) {
		sink := &fakeCapSink{cap: 3, accept: true}
		heat(t, sink)
		if len(sink.requests) != 1 || sink.requests[0].To != 2 {
			t.Fatalf("requests %+v", sink.requests)
		}
		sink = &fakeCapSink{cap: 2, accept: true}
		h, _, _ := heat(t, sink)
		if len(sink.requests) != 0 || !logHas(h, "thermal", "lane cap already 2; nothing to lower") {
			t.Fatalf("requests %+v", sink.requests)
		}
	})
	t.Run("no run dispatching", func(t *testing.T) {
		sink := &fakeCapSink{why: "no Marshal run is dispatching"}
		h, _, _ := heat(t, sink)
		if len(sink.requests) != 0 || !logHas(h, "thermal", "no Marshal run is dispatching; lane cap unchanged") {
			t.Fatalf("requests %+v log %+v", sink.requests, h.snapshot().Log)
		}
	})
	t.Run("cap unreadable", func(t *testing.T) {
		sink := &fakeCapSink{cap: 12, projectErr: errors.New("store closed")}
		h, _, _ := heat(t, sink)
		if len(sink.requests) != 0 || !logHas(h, "thermal", "could not read the lane cap: store closed") {
			t.Fatalf("requests %+v", sink.requests)
		}
	})
}

func TestHostWatcherStopsOnExit(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	h, _, _ := testWatcher(t, &fakeCapSink{why: "test"})
	h.now = time.Now
	h.every = 10 * time.Millisecond
	h.facts = nil
	if !w.startHostWatchWith(context.Background(), h) {
		t.Fatal("watcher not started")
	}
	deadline := time.Now().Add(5 * time.Second)
	for h.ring.Len() < 3 {
		if time.Now().After(deadline) {
			t.Fatal("watcher did not sample")
		}
		time.Sleep(5 * time.Millisecond)
	}
	w.Close()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher still running after Close")
	}
	n := h.ring.Len()
	time.Sleep(50 * time.Millisecond)
	if h.ring.Len() != n {
		t.Fatal("watcher sampled after Close")
	}
}

func TestHostViewKeys(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	press := func(r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }
	if press('h') || w.hostView.Load() {
		t.Fatal("h opened a Host view with no watcher")
	}
	h, _, _ := testWatcher(t, nil)
	w.hostWatch.Store(h)
	for _, r := range []rune{'h', '2'} {
		if !press(r) || !w.hostView.Load() {
			t.Fatalf("%c did not open the Host view", r)
		}
		if !press(r) || w.hostView.Load() {
			t.Fatalf("%c did not return to the Board", r)
		}
	}
	press('h')
	if !w.handleBoardKey(KeyEvent{Type: KeyEsc}) || w.hostView.Load() {
		t.Fatal("Esc did not return to the Board")
	}
	w.composer.SetText("h")
	if press('2') || w.hostView.Load() {
		t.Fatal("2 opened the Host view while the composer held text")
	}
	w.composer.SetText("")
	w.boardUI.inspect = "T-017"
	if press('h') || w.hostView.Load() {
		t.Fatal("h opened the Host view over the inspector")
	}
}

func TestHostNoticeNeverDisplacesRunNote(t *testing.T) {
	snap := demoHost(t)
	snap.Notice = "host: thermal guard · asked owner: lane cap 12 → 6 (inbox)"
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true), host: &snap, note: "Approve with /marshal approve-task app-d71a119c-7b4b-4696-8ef3-2dfbf194f0aa"}
	narrow := v.footer(100)
	if !strings.Contains(narrow, "app-d71a119c-7b4b-4696-8ef3-2dfbf194f0aa") || strings.Contains(narrow, "thermal guard") {
		t.Fatalf("narrow footer %q", narrow)
	}
	wide := v.footer(200)
	if !strings.Contains(wide, "app-d71a119c-7b4b-4696-8ef3-2dfbf194f0aa") || !strings.Contains(wide, "▲ host: thermal guard") {
		t.Fatalf("wide footer %q", wide)
	}
	v.note = ""
	if got := v.footer(100); !strings.Contains(got, "▲ host: thermal guard") {
		t.Fatalf("notice alone %q", got)
	}
}

func TestHostCPUBusy(t *testing.T) {
	tick := func(total, idle uint64) hostwatch.Sample {
		return hostwatch.Sample{CPUTicksTotal: total, CPUTicksIdle: idle, CPUTicksKnown: true}
	}
	noTick := hostwatch.Sample{}

	cases := []struct {
		name    string
		samples []hostwatch.Sample
		wantPct float64
		wantOK  bool
	}{
		{"two samples", []hostwatch.Sample{tick(10000, 8500), tick(12000, 10000)}, 25, true},
		{"newest without ticks skipped", []hostwatch.Sample{tick(10000, 8500), tick(12000, 10000), noTick}, 25, true},
		{"one sample", []hostwatch.Sample{tick(10000, 8500)}, 0, false},
		{"decreasing total", []hostwatch.Sample{tick(12000, 10000), tick(10000, 8500)}, 0, false},
		{"fewer than two with ticks", []hostwatch.Sample{noTick, tick(12000, 10000), noTick}, 0, false},
		{"empty ring", nil, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := hostSnapshot{Samples: c.samples}
			pct, ok := s.cpuBusy()
			if ok != c.wantOK || (ok && pct != c.wantPct) {
				t.Fatalf("cpuBusy() = (%v, %v), want (%v, %v)", pct, ok, c.wantPct, c.wantOK)
			}
		})
	}
}
