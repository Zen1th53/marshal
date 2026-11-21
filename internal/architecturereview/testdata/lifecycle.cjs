// A small DOM double exercises the shipped script, including click handlers.
// Keep this independent of browser downloads so local Go tests can run it.
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
class Element {
  constructor(tag='div'){this.tagName=tag;this.children=[];this.attributes={};this.dataset={};this.hidden=false;this.value='';this.textContent='';this.className='';this.options=[{},{}];this.classList={remove:name=>{this.className=this.className.split(' ').filter(n=>n!==name).join(' ');},toggle:(name,on)=>{const names=new Set(this.className.split(' '));if(on)names.add(name);else names.delete(name);this.className=[...names].join(' ');}};}
  setAttribute(k,v){this.attributes[k]=String(v);if(k==='class')this.className=v;if(k.startsWith('data-'))this.dataset[k.slice(5)]=v;}
  append(...items){this.children.push(...items);} replaceChildren(...items){this.children=items;} focus(){} remove(){} querySelector(){return null;} getComputedTextLength(){return 0;}
}
const elements=new Map(),get=id=>{if(!elements.has(id))elements.set(id,new Element());return elements.get(id);};
const document={getElementById:get,createElement:tag=>new Element(tag),createElementNS:(_,tag)=>new Element(tag),createTextNode:text=>({textContent:text}),documentElement:{},body:new Element(),querySelector:selector=>get(selector),querySelectorAll:()=>[]};
const page=fs.readFileSync(process.argv[2],'utf8'),fixture=JSON.parse(fs.readFileSync(process.argv[3],'utf8'));
const context=vm.createContext({document,history:{replaceState(){}},CSS:{escape:x=>x},confirm:()=>true,fetch:()=>{throw Error('Unexpected network call');}});
let script=page.split('<script>')[1].split('</script>')[0].replace('__REVIEW_TOKEN__',"'fixture-token'");
script=script.slice(0,script.indexOf('Promise.allSettled([loadBuilders()'));
vm.runInContext(script,context);context.fixture=fixture;
const run=code=>vm.runInContext(code,context);
run('state=fixture;draw()');
assert.equal(get('revision-bar').textContent,'rev 3 → 4 · proposed');
const diagram=()=>get('diagram').children,part=id=>diagram().find(n=>n.dataset.id===id);
for(const [id,status] of [['current','existing'],['added','new'],['edited','changed'],['removed','removed'],['existing-edge','existing'],['new-edge','new'],['changed-edge','changed'],['removed-edge','removed'],['implicit-edge','new']])assert.ok(part(id).className.split(' ').includes(status),id);
assert.ok(part('edited').children.some(n=>n.className==='change-outline'));
assert.ok(part('added').children.some(n=>n.textContent==='built'));
assert.ok(part('edited').children.some(n=>n.textContent==='differs from the architecture'));
// Focus: a part and its direct neighbours stay bright; the panel lists links.
run("openPart('current')");
assert.ok(get('diagram').className.split(' ').includes('focusing'));
assert.ok(part('current').className.split(' ').includes('lit'));assert.ok(part('edited').className.split(' ').includes('lit'));
assert.ok(!part('untouched').className.split(' ').includes('lit'));
assert.ok(get('incoming').children.length>=3);assert.ok(get('outgoing').children.length>=2);
run('closeEditor()');assert.ok(!get('diagram').className.split(' ').includes('focusing'));
// Search highlights matching parts across drawings.
get('search').value='changed';get('search').oninput();assert.ok(part('edited').className.split(' ').includes('match'));
get('search').value='';get('search').oninput();assert.ok(!part('edited').className.split(' ').includes('match'));
run("openElement('component','edited')");
assert.ok(get('change-detail').children[0].children.some(row=>row.children.some(n=>n.textContent==='Before')));
assert.ok(get('change-detail').children[0].children.some(row=>row.children.some(n=>n.textContent==='After')));
run("openElement('connection','changed-edge')");
assert.ok(get('change-detail').children[0].children.some(row=>row.children.some(n=>n.textContent==='Before traffic')));
get('only-changes').checked=true;get('only-changes').onchange();
assert.ok(part('current'));assert.equal(part('untouched'),undefined);assert.ok(part('added'));
// Selecting an archived drawing never replaces the live proposal used by POSTs.
run(`state.history=[{...JSON.parse(JSON.stringify(fixture)),revision:3,base_revision:2,approved:true,date:'2026-11-19',goal:'Goal A',run:'Run A',accepted_by:'Owner'}];renderHistory()`);
assert.ok(get('history-list').children[0].children[0].children[0].textContent.includes('Accepted by: Owner'));
get('history-list').children[0].children[0].onclick();
assert.equal(get('revision-bar').textContent,'rev 3 · current');assert.equal(get('approve').disabled,true);assert.equal(run('state.revision'),4);
run("openElement('component','edited')");assert.equal(get('submit').hidden,true);
get('history-live').onclick();assert.equal(get('revision-bar').textContent,'rev 3 → 4 · proposed');
run('state.approved=true;state.revision=9;draw()');assert.equal(get('revision-bar').textContent,'rev 4 · current');
run('state.revision=4;state.architecture.base_revision=state.base_revision;delete state.base_revision;draw()');assert.equal(get('revision-bar').textContent,'rev 4 · current');
run('delete state.architecture.base_revision');
// An older seed has no changes, history, conformance or baseline metadata.
run(`delete state.base_revision;delete state.history;for(const list of [state.architecture.components,state.architecture.connections])for(const item of list){delete item.change;delete item.conformance;}state.approved=false;draw()`);
assert.equal(part('added'),undefined); // all parts are untouched while filtered
get('only-changes').checked=false;get('only-changes').onchange();
assert.ok(part('added').className.includes('existing'));assert.ok(part('implicit-edge').className.includes('existing'));
assert.equal(get('history-list').children[0].textContent,'No revisions recorded yet.');
// A supplied previous snapshot also supplies before fields when the item has no inline copy.
run(`state.base_revision=3;state.architecture.components.find(c=>c.id==='edited').change='changed';delete state.architecture.components.find(c=>c.id==='edited').before;state.history=[{revision:3,architecture:{components:[{id:'edited',responsibility:'Previous snapshot'}]}}];openElement('component','edited')`);
assert.ok(get('change-detail').children[0].children.some(row=>row.children.some(n=>n.textContent==='Previous snapshot')));
// Containers stay visible when a changed child belongs to a nested diagram.
run(`state.architecture.components.find(c=>c.id==='edited').parent='untouched';$('only-changes').checked=true;draw()`);
assert.ok(part('untouched'));

// The runtime's own seed: the contract revision is source_contract.revision
// (state.revision counts review updates), and history and build status come
// from /api/history and /api/conformance.
(async()=>{
run(`state.architecture.components.find(c=>c.id==='edited').parent=undefined;delete state.history;state.approved=false;state.revision=17;state.base_revision=3;state.architecture.source_contract={id:'project',revision:4,digest:'d'};$('only-changes').checked=false;draw()`);
assert.equal(get('revision-bar').textContent,'rev 3 → 4 · proposed');
// The part panel names the contract revision too, never the review-state counter.
run("target={kind:'component',id:'edited'};openEditor()");assert.equal(get('selection-meta').textContent,'Revision 4 · edited');run('closeEditor()');
// The whole connection carries its full label as a tooltip; the caption may be cut.
assert.ok(part('existing-edge').children.some(n=>n.tagName==='title'&&n.textContent==='Current traffic'));
const replies={'/api/history':{revisions:[{revision:3,date:'2026-11-18',goal:'Goal B',run:'Run B',accepted_by:'Owner B',digest:'sha256:x'}]},
 '/api/history?revision=3':{revision:3,base_revision:2,date:'2026-11-18',goal:'Goal B',run:'Run B',accepted_by:'Owner B',architecture:fixture.architecture,layouts:fixture.layouts,read_only:true},
 '/api/conformance':{present:true,revision:4,components:[{id:'current',status:'built'},{id:'untouched',status:'differs from the architecture'},{id:'removed',status:'not built'}]}};
const asked=[];context.fetch=async path=>{asked.push(path);return {ok:true,json:async()=>JSON.parse(JSON.stringify(replies[path]))};};
await run('historyAt=0;progressAt=0;Promise.all([loadHistory(),loadProgress()])');
assert.ok(part('current').children.some(n=>n.textContent==='built'));
assert.ok(part('untouched').children.some(n=>n.textContent==='differs from the architecture'));
assert.ok(!part('removed').children.some(n=>n.textContent==='built'));
const entry=get('history-list').children[0].children[0];
assert.ok(entry.children[0].textContent.includes('Accepted by: Owner B'));assert.equal(entry.disabled,false);
entry.onclick();await new Promise(r=>setTimeout(r,0));await new Promise(r=>setTimeout(r,0));
assert.ok(asked.includes('/api/history?revision=3'));
assert.equal(get('revision-bar').textContent,'rev 3 · current');assert.equal(run('state.revision'),17);
get('history-live').onclick();assert.equal(get('revision-bar').textContent,'rev 3 → 4 · proposed');
run('state.current=true;state.read_only=true;state.base_revision=3;draw()');assert.equal(get('revision-bar').textContent,'rev 4 · current');
console.log('Lifecycle rendering and interactions passed');
})().catch(e=>{console.error(e);process.exit(1);});
