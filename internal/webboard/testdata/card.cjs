// Exercise the shipped renderer with a stale architecture frame plus a new chat question.
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const script = fs.readFileSync(process.argv[2] || 'page/app.js', 'utf8');
const elements = new Map();
const get = id => { if (!elements.has(id)) elements.set(id, { hidden: false }); return elements.get(id); };
const card = get("card");
const context = vm.createContext({
  $: get, clear: () => {}, setText: () => {}, renderButtons: () => {}, changed: () => true, confirmStep: null, pending: false, armedStop: null,
});
for (const [start, end] of [['  function asksOwner(', '  // ---- small helpers'], ['  function renderCard(', '  // renderButtons']]) {
  vm.runInContext(script.slice(script.indexOf(start), script.indexOf(end)), context);
}
context.frame = { nowLine: { chatAsked: true }, decision: { kind: 'architecture', nodes: [], lines: [], title: 'Accept architecture' } };
vm.runInContext('renderCard(frame)', context);
assert.equal(card.hidden, true, 'architecture card hides the Marshal question');
