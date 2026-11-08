package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/hostwatch"
)

// The host watcher samples the machine Marshal runs on for as long as the
// TUI runs, whether or not the Host view is open: Marshal is meant to know
// the host's state at all times. A sample reads /proc and /sys only; the
// GPU probe runs at most every hostGPUEvery and the go build cache is
// measured at most every hostCacheEvery, off the sampling path. Nothing is
// written to the store per sample.
const (
	hostSampleEvery = time.Second
	hostRingSize    = 120
	hostLogMax      = 50
	hostGPUEvery    = 5 * time.Second
	hostFactsEvery  = time.Minute
	hostCacheEvery  = 5 * time.Minute
	// hostPolicyWindow is the window the policy is evaluated over. The
	// thermal rule averages its last 30 s and the throttle rule turns the
	// window's counter delta into a rate per minute.
	hostPolicyWindow = time.Minute
)

// Rule names as hostwatch.Policy reports them.
const (
	ruleThermal  = "thermal_package_temp"
	ruleDisk     = "disk_free_low"
	ruleMemory   = "memory_available_low"
	ruleThrottle = "cpu_throttle_rate_high"
	ruleGPU      = "gpu_temp_high"
	ruleNVMe     = "nvme_temp_high"
)

// hostSampler is what the watcher reads the host through; tests give it a
// fake.
type hostSampler interface {
	Read(context.Context) (hostwatch.Sample, error)
}

// hostLogEntry is one line of the HOST LOG: what the watcher noticed or did.
type hostLogEntry struct {
	At   time.Time
	Kind string // thermal, disk, memory, throttle, gpu, nvme, info
	Msg  string
}

// hostWatcher owns the ring of samples, the policy state and the HOST LOG.
type hostWatcher struct {
	sampler   hostSampler
	policy    hostwatch.Policy
	ring      *hostwatch.Ring
	every     time.Duration
	now       func() time.Time
	facts     func() hostFacts                     // nil: no slow facts
	cacheSize func(context.Context) (uint64, bool) // nil: not measured
	guard     *hostLaneGuard                       // nil: no lane-cap action
	changed   func(chipChanged bool)               // after each sample; may be nil

	mu      sync.Mutex
	state   hostwatch.State
	log     []hostLogEntry // newest first
	notices map[string]string
	hf      hostFacts
	factsAt time.Time
	cache   struct {
		bytes   uint64
		known   bool
		at      time.Time
		running bool
	}
	net struct {
		day      string
		rx, tx   uint64 // counters at the start of the day's baseline
		since    time.Time
		baseline bool
	}
	chip string
	wg   sync.WaitGroup
	done chan struct{}
}

func newHostWatcher(s hostSampler) *hostWatcher {
	return &hostWatcher{
		sampler: s,
		policy:  hostwatch.DefaultPolicy(),
		ring:    hostwatch.NewRing(hostRingSize),
		every:   hostSampleEvery,
		now:     time.Now,
		notices: map[string]string{},
		done:    make(chan struct{}),
	}
}

// run samples every h.every until ctx ends, then waits for a cache
// measurement still in flight and closes done.
func (h *hostWatcher) run(ctx context.Context) {
	defer close(h.done)
	defer h.wg.Wait()
	h.addLog("info", fmt.Sprintf("watching every %s · GPU every %s · policy below", shortDur(h.every), shortDur(hostGPUEvery)))
	t := time.NewTicker(h.every)
	defer t.Stop()
	for {
		h.step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// step takes one sample, evaluates the policy over the last minute and acts
// on its edges.
func (h *hostWatcher) step(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	s, err := h.sampler.Read(ctx)
	if err != nil {
		return
	}
	if s.At.IsZero() {
		s.At = h.now()
	}
	h.ring.Push(s)
	h.refreshSlow(ctx, s.At)
	h.noteNet(s)
	window := h.window()
	h.mu.Lock()
	prev := h.state
	h.mu.Unlock()
	next, actions := h.policy.Evaluate(window, prev)
	h.mu.Lock()
	h.state = next
	h.mu.Unlock()
	for _, a := range actions {
		h.act(a, window)
	}
	chip := h.snapshot().chipText()
	h.mu.Lock()
	changed := chip != h.chip
	h.chip = chip
	h.mu.Unlock()
	if h.changed != nil {
		h.changed(changed)
	}
}

// window is the samples of the last hostPolicyWindow, oldest first.
func (h *hostWatcher) window() []hostwatch.Sample {
	all := h.ring.Samples()
	if len(all) == 0 {
		return nil
	}
	cut := all[len(all)-1].At.Add(-hostPolicyWindow)
	i := 0
	for i < len(all)-1 && all[i].At.Before(cut) {
		i++
	}
	return all[i:]
}

// act records one edge of the policy in the HOST LOG and, for the rules
// that have one, takes their action or raises their notice.
func (h *hostWatcher) act(a hostwatch.Action, window []hostwatch.Sample) {
	trigger := a.Kind == hostwatch.ActionTrigger
	switch a.Rule {
	case ruleThermal:
		avg, _ := avgPackage(window, 30*time.Second)
		var msg, outcome string
		if trigger {
			msg = fmt.Sprintf("GUARD · package 30 s avg %.1f °C > %s", avg, trimFloat(h.policy.PackageTempTriggerC))
			if h.guard != nil {
				outcome = h.guard.trigger(avg)
			}
		} else {
			msg = fmt.Sprintf("RELEASE · package 30 s avg %.1f °C < %s", avg, trimFloat(h.policy.PackageTempReleaseC))
			if h.guard != nil {
				outcome = h.guard.release()
			}
		}
		if outcome != "" {
			msg += " → " + outcome
		}
		h.addLog("thermal", msg)
		if trigger {
			h.setNotice(a.Rule, "host: thermal guard · "+firstNonEmpty(outcome, msg))
		} else {
			h.setNotice(a.Rule, "")
		}
	case ruleDisk:
		h.addLog("disk", degrees(a.Reason)+noticeSuffix(trigger))
		if trigger {
			h.setNotice(a.Rule, "host: "+degrees(a.Reason)+". No automatic action.")
		} else {
			h.setNotice(a.Rule, "")
		}
	case ruleMemory:
		h.addLog("memory", degrees(a.Reason)+noticeSuffix(trigger))
		if trigger {
			h.setNotice(a.Rule, "host: "+degrees(a.Reason)+". No automatic action.")
		} else {
			h.setNotice(a.Rule, "")
		}
	default:
		h.addLog(ruleKind(a.Rule), degrees(a.Reason))
	}
}

func noticeSuffix(trigger bool) string {
	if trigger {
		return " → notice only, no automatic action"
	}
	return ""
}

func ruleKind(rule string) string {
	switch rule {
	case ruleThermal:
		return "thermal"
	case ruleDisk:
		return "disk"
	case ruleMemory:
		return "memory"
	case ruleThrottle:
		return "throttle"
	case ruleGPU:
		return "gpu"
	case ruleNVMe:
		return "nvme"
	}
	return "info"
}

// degrees writes the policy's plain " C" unit as °C.
func degrees(s string) string {
	s = strings.ReplaceAll(s, " C ", " °C ")
	if strings.HasSuffix(s, " C") {
		s = strings.TrimSuffix(s, " C") + " °C"
	}
	return s
}

func (h *hostWatcher) addLog(kind, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.log = append([]hostLogEntry{{At: h.now(), Kind: kind, Msg: msg}}, h.log...)
	if len(h.log) > hostLogMax {
		h.log = h.log[:hostLogMax]
	}
}

func (h *hostWatcher) setNotice(rule, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if text == "" {
		delete(h.notices, rule)
		return
	}
	h.notices[rule] = text
}

// refreshSlow re-reads the host facts once a minute and starts a go build
// cache measurement at most every hostCacheEvery, in the background.
func (h *hostWatcher) refreshSlow(ctx context.Context, at time.Time) {
	h.mu.Lock()
	factsDue := h.facts != nil && (h.factsAt.IsZero() || at.Sub(h.factsAt) >= hostFactsEvery)
	cacheDue := h.cacheSize != nil && !h.cache.running && (h.cache.at.IsZero() || at.Sub(h.cache.at) >= hostCacheEvery)
	if factsDue {
		h.factsAt = at
	}
	if cacheDue {
		h.cache.running = true
		h.cache.at = at
	}
	h.mu.Unlock()
	if factsDue {
		f := h.facts()
		h.mu.Lock()
		h.hf = f
		h.mu.Unlock()
	}
	if cacheDue {
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			n, ok := h.cacheSize(ctx)
			h.mu.Lock()
			h.cache.running = false
			if ok {
				h.cache.bytes, h.cache.known = n, true
			}
			h.mu.Unlock()
		}()
	}
}

// noteNet keeps the counters at the start of today, or at the watcher's
// first sample when it started later, so the view can show today's traffic.
func (h *hostWatcher) noteNet(s hostwatch.Sample) {
	if !s.NetRxBytesKnown || !s.NetTxBytesKnown {
		return
	}
	day := s.At.Format("2006-01-02")
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.net.baseline || h.net.day != day || s.NetRxBytes < h.net.rx || s.NetTxBytes < h.net.tx {
		h.net.day, h.net.rx, h.net.tx, h.net.since, h.net.baseline = day, s.NetRxBytes, s.NetTxBytes, s.At, true
	}
}

// hostSnapshot is everything the chip and the Host view paint, copied so
// painting holds no lock.
type hostSnapshot struct {
	Now     time.Time
	Every   time.Duration
	Samples []hostwatch.Sample // oldest first
	Active  map[string]bool
	Policy  hostwatch.Policy
	Log     []hostLogEntry // newest first
	Notice  string
	Facts   hostFacts
	GoCache struct {
		Bytes uint64
		Known bool
	}
	Net struct {
		Since  time.Time
		Rx, Tx uint64
		Known  bool
	}
	Guard hostGuardView
}

func (h *hostWatcher) snapshot() hostSnapshot {
	var s hostSnapshot
	s.Samples = h.ring.Samples()
	h.mu.Lock()
	defer h.mu.Unlock()
	s.Now = h.now()
	s.Every = h.every
	s.Policy = h.policy
	s.Active = map[string]bool{}
	for k, v := range h.state.ActiveRules {
		s.Active[k] = v
	}
	s.Log = append([]hostLogEntry(nil), h.log...)
	for _, rule := range []string{ruleThermal, ruleDisk, ruleMemory} {
		if n := h.notices[rule]; n != "" {
			s.Notice = n
			break
		}
	}
	s.Facts = h.hf
	s.GoCache.Bytes, s.GoCache.Known = h.cache.bytes, h.cache.known
	if h.guard != nil {
		s.Guard = h.guard.view()
	}
	if last, ok := s.latest(); ok && h.net.baseline && last.NetRxBytesKnown && last.NetTxBytesKnown {
		s.Net.Since, s.Net.Rx, s.Net.Tx, s.Net.Known = h.net.since, last.NetRxBytes-h.net.rx, last.NetTxBytes-h.net.tx, true
	}
	return s
}

func (s hostSnapshot) latest() (hostwatch.Sample, bool) {
	if len(s.Samples) == 0 {
		return hostwatch.Sample{}, false
	}
	return s.Samples[len(s.Samples)-1], true
}

// avgPackage averages the package temperature over the last d of window.
func avgPackage(window []hostwatch.Sample, d time.Duration) (float64, bool) {
	if len(window) == 0 {
		return 0, false
	}
	cut := window[len(window)-1].At.Add(-d)
	var sum float64
	var n int
	for _, s := range window {
		if !s.At.Before(cut) && s.CPUPackageTempCKnown {
			sum += s.CPUPackageTempC
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}

// root is the root mount of the latest sample.
func (s hostSnapshot) root() (hostwatch.DiskMountUsage, bool) {
	last, ok := s.latest()
	if !ok {
		return hostwatch.DiskMountUsage{}, false
	}
	for _, m := range last.DiskMounts {
		if m.MountPoint == "/" && m.Known && m.TotalBytes > 0 {
			return m, true
		}
	}
	return hostwatch.DiskMountUsage{}, false
}

// throttleRate is package throttle events per minute over the last minute.
func (s hostSnapshot) throttleRate() (float64, bool) {
	return s.rate(func(x hostwatch.Sample) (uint64, bool) {
		return uint64(max(0, x.CPUPackageThrottleCount)), x.CPUPackageThrottleKnown
	}, 60)
}

// rate is the per-second (scale 1) or per-minute (scale 60) rate of a
// counter over the last minute of samples.
func (s hostSnapshot) rate(get func(hostwatch.Sample) (uint64, bool), scale float64) (float64, bool) {
	last, ok := s.latest()
	if !ok {
		return 0, false
	}
	lv, lok := get(last)
	if !lok {
		return 0, false
	}
	cut := last.At.Add(-hostPolicyWindow)
	for _, x := range s.Samples {
		if x.At.Before(cut) {
			continue
		}
		v, ok := get(x)
		secs := last.At.Sub(x.At).Seconds()
		if !ok || secs <= 0 {
			continue
		}
		if lv < v {
			return 0, true
		}
		return float64(lv-v) / secs * scale, true
	}
	return 0, false
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func trimFloat(f float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", f), "0"), ".")
}

func shortDur(d time.Duration) string {
	if d%time.Second == 0 {
		return fmt.Sprintf("%d s", int(d/time.Second))
	}
	return d.String()
}

// startHostWatch starts the screen's host watcher on the live host.
func (w *Workspace) startHostWatch(ctx context.Context) {
	s := hostwatch.NewSampler()
	s.GPUEvery = hostGPUEvery
	s.NetInterface = defaultRouteIface(liveHostRoots.Proc + "/net/route")
	h := newHostWatcher(s)
	h.facts = func() hostFacts { return readHostFacts(liveHostRoots, time.Now()) }
	h.cacheSize = goBuildCacheSize
	w.startHostWatchWith(ctx, h)
}

// startHostWatchWith runs h as workspace background work, so Close stops
// it with the rest. The lane guard acts through this workspace's inbox.
func (w *Workspace) startHostWatchWith(ctx context.Context, h *hostWatcher) bool {
	if h.guard == nil {
		h.guard = &hostLaneGuard{sink: workspaceLaneCapSink{w}}
	}
	h.changed = func(chip bool) {
		// The Board repaints for a changed chip; the Host view for
		// every sample.
		if (chip || w.hostView.Load()) && w.boardVisible() {
			w.requestRepaint()
		}
	}
	w.hostWatch.Store(h)
	return w.startBackground(ctx, h.run)
}
