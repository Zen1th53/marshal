// Every typed choice of a decision card is drawn and sent by its own id.
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const script = fs.readFileSync(process.argv[2] || 'page/app.js', 'utf8');
const sent = [];
const context = vm.createContext({ terminalOnly: { c: 1, 7: 1, e: 1, q: 1, esc: 1 }, cap: s => s, act: (body, label) => { sent.push(body); return Promise.resolve(); } });
vm.runInContext(script.slice(script.indexOf('  function cardButtons('), script.indexOf('  function screenKey(')), context);

const ask = { kind: 'ask', revision: 'ask:1', actions: [], choices: ['a', 'b', 'c', 'd', 'e'].map((k, i) => ({ id: 'card.choose', value: 'ask:' + i, key: k, label: 'option ' + i, local: false })) };
context.frame = { decision: ask };
let buttons = vm.runInContext('cardButtons(frame)', context);
assert.equal(buttons.length, 5, 'an ask choice was dropped');
assert.ok(buttons.every(b => !b.local), 'an ask choice became local');

const change = { kind: 'change', revision: 'change:r:1', actions: [], choices: [{ id: 'change.apply', value: '', key: '⏎', label: 'apply this change' }, { id: 'change.keep', value: '', key: 'k', label: 'keep the current goal' }] };
context.frame = { decision: change };
buttons = vm.runInContext('cardButtons(frame)', context);
assert.equal(buttons[1].choice.id, 'change.keep');
assert.equal(buttons[1].local, false, 'keep the current goal became the local comment button');
vm.runInContext('decide(cardButtons(frame)[1].choice)', context);
assert.equal(sent[0].action, 'decide');
assert.equal(sent[0].choice, 'change.keep');
assert.equal(sent[0].cardRevision, 'change:r:1');

const inbox = { kind: 'inbox', revision: 'inbox:A-1', actions: [], choices: [
  { id: 'inbox.decide', value: 'allow', key: 'a', label: 'allow' }, { id: 'inbox.decide', value: 'deny', key: 'd', label: 'deny' },
  { id: 'inbox.inspect', value: '', key: '⏎', label: 'inspect the request' }, { id: 'inbox.return', value: '', key: 'r', label: 'return with a reason' }] };
context.frame = { decision: inbox };
assert.deepEqual(Array.from(vm.runInContext('cardButtons(frame)', context), b => b.label), ['allow', 'deny', 'inspect the request', 'return with a reason']);

const plan = { kind: 'plan', revision: 'plan:r:p', plan: { digest: 'D1', tasks: [] }, actions: [], choices: [
  { id: 'plan.approve', value: '', key: '⏎', label: 'approve' }, { id: 'plan.read', value: '', key: '7', label: 'open the plan' }, { id: 'plan.comment', value: '', key: 'k', label: 'comment', local: true }] };
context.frame = { decision: plan };
buttons = vm.runInContext('cardButtons(frame)', context);
assert.equal(buttons.length, 3, 'the plan cannot be opened');
assert.equal(buttons[2].local, true);
vm.runInContext('decide(cardButtons(frame)[0].choice)', context);
assert.equal(sent[1].planDigest, 'D1', 'the approval does not carry the plan digest');

// A paused card keeps all its buttons, the raw error too.
context.frame = { paused: { actions: [{ key: '⏎', label: 'retry' }, { key: 'w', label: 'wait' }, { key: 'd', label: 'raw error' }, { key: 'x', label: 'stop the run' }] } };
assert.equal(vm.runInContext('cardButtons(frame)', context).length, 4, 'a paused button was dropped');
