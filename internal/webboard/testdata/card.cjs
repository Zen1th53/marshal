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
// Question choices and guard cancellation must remain server actions.
vm.runInContext('var terminalOnly = {c:1,e:1,q:1,esc:1}; function cap(s) { return s; }', context);
vm.runInContext(script.slice(script.indexOf('  function cardButtons('), script.indexOf('  function screenKey(')), context);
context.frame = {decision:{kind:'ask', actions:[{key:'⏎',label:'Yes'},{key:'c',label:'Choice'},{key:'e',label:'Edit choice'},{key:'k',label:'Keep'}]}};
let buttons = vm.runInContext('cardButtons(frame)', context);
assert.equal(buttons.length, 4, 'question choices were dropped');
assert.equal(buttons[3].local, false, 'choice became a local comment action');
context.frame = {decision:{kind:'guard',actions:[{key:'⏎',label:'Undo'},{key:'esc',label:'Cancel'}]}};
assert.equal(vm.runInContext('cardButtons(frame)', context).length, 2, 'guard cancel was hidden');
