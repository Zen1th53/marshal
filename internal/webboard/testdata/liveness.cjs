// Exercise the shipped liveness helpers with an injected clock: a worker
// that cannot be verified reads "cannot verify", never exited or dead; only
// a known quiet signal turns amber; approval ages progress and expire.
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const script = fs.readFileSync(process.argv[2] || 'page/app.js', 'utf8');
const start = script.indexOf('  // ---- liveness'), end = script.indexOf('  // ---- end liveness');
assert.ok(start > 0 && end > start, 'liveness helpers are marked in app.js');
const context = vm.createContext({
  dur: s => Math.floor(s) + 's',
  parse: iso => (iso ? new Date(iso) : null),
  hm: iso => iso.slice(11, 16),
});
vm.runInContext(script.slice(start, end), context);
const sv = (x, add) => vm.runInContext('signalView(x, add)', Object.assign(context, { x, add }));

const unverifiable = { lastSignalSec: 20, signal: { at: '2026-03-02T11:59:40Z', state: 'unverifiable', label: 'the process is in another namespace' } };
for (const add of [0, 600]) {
  const v = sv(unverifiable, add);
  assert.match(v.text, /^cannot verify/);
  assert.doesNotMatch(v.text, /dead|exited/i);
  assert.notEqual(v.tone, 'muted');
}
assert.equal(sv({ lastSignalSec: 300, signal: { at: 'x', state: 'stale', label: 'no heartbeat for 5m: cannot verify the worker' } }, 0).tone, 'bad');
const live = { lastSignalSec: 50, signal: { at: '2026-03-02T11:59:10Z', state: 'live' } };
assert.equal(sv(live, 0).tone, '');
assert.equal(sv(live, 11).tone, 'warn', 'amber after 60 s of known quiet');
assert.notEqual(sv(live, 3600).tone, 'bad', 'the local clock never turns a signal red');
assert.equal(sv({ lastSignalSec: 0, signal: { state: 'live' } }, 3600).tone, '', 'no observed signal: no amber');
assert.equal(sv({ lastSignalSec: 0, signal: { state: 'none', label: 'worker window: no heartbeat to read' } }, 90).tone, '');

const age = (a, nowIso, add) => vm.runInContext('approvalAge(a, nowIso, add)', Object.assign(context, { a, nowIso, add }));
const ap = { ageSec: 180, status: 'requested', expiresAt: '2026-03-02T12:07:00Z' };
assert.equal(age(ap, '2026-03-02T12:00:00Z', 0), 'waiting 180s · expires in 420s');
assert.equal(age(ap, '2026-03-02T12:00:00Z', 60), 'waiting 240s · expires in 360s');
assert.match(age(ap, '2026-03-02T12:00:00Z', 500), /expired, cannot be answered/);
assert.match(age({ ageSec: 5, status: 'superseded' }, '2026-03-02T12:00:00Z', 0), /superseded, cannot be answered/);
