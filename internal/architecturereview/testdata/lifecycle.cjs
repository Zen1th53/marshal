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
// Generic columns include disconnected parts below their connected neighbours.
// This is the eight-part, three-column shape that used to overflow its bands.
{
const nodes=[
  ['cli',72,72,150],['marshal-config',72,200,200],['module',72,328,200],['project-docs',72,456,200],
  ['planner',352,72,180],['runner',352,200,180],['store',612,72,180],['provider',612,200,180]
].map(([id,x,y,width])=>({id,x,y,width,height:72}));
const edges=[
  {id:'plan',points:[[222,108],[352,108]]},
  {id:'save',points:[[532,108],[612,108]]},
  {id:'run',points:[[532,236],[612,236]]}
];
context.columnLayout={version:'generic-v3',width:864,height:600,nodes,edges};
const before=JSON.stringify(context.columnLayout);
const checkBands=()=>{
  const groups=run('bands(columnLayout)');
  assert.equal(groups.length,new Set(context.columnLayout.nodes.map(n=>n.x)).size);
  for(const n of context.columnLayout.nodes){
    const containing=groups.filter(g=>{
      const r=g.children[0].attributes;
      return +r.x<=n.x&&+r.y<=n.y&&+r.x+ +r.width>=n.x+n.width&&+r.y+ +r.height>=n.y+n.height;
    });
    assert.equal(containing.length,1,`${n.id} must be inside its column band`);
  }
};
checkBands();
assert.equal(JSON.stringify(context.columnLayout),before,'bands must preserve node spacing and edge geometry');
// A column without any connections, including a single-column drawing, still has a band.
context.columnLayout={...context.columnLayout,edges:[]};checkBands();
context.columnLayout={...context.columnLayout,nodes:nodes.slice(0,4)};checkBands();
}
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
// Advanced view routing: a part without sub-parts opens a derived code level
// only in Advanced view; unknown or stale segments fall back to the top.
assert.equal(run("hashFor(['a','~x/y'])"),'#/a/~x%2Fy');
assert.equal(run("advanced=false;path=['added'];draw();path.length"),0);assert.equal(run('view'),'architecture');
assert.equal(run("advanced=true;path=['added'];draw();view"),'code:added');
assert.equal(get('crumbs').hidden,false);assert.ok(run("shown()!==base()&&shown().layouts['code:added']!==undefined"));
assert.equal(run("path=['nope'];draw();path.length"),0);
assert.equal(run("path=['added','~a','~b'];draw();path.join('/')"),'added/~a');
assert.equal(run("advanced=false;path=[];derivedView=null;draw();view"),'architecture');assert.equal(get('crumbs').hidden,true);
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
// Click to open: one click on a part with sub-parts opens it (breadcrumb and
// #path follow) and shows that part's panel inside; a leaf click focuses; a pan
// drag that starts on a part never opens it; Back returns to the top.
const tick=()=>new Promise(r=>setTimeout(r,0)),pushed=[];context.setTimeout=setTimeout;context.history.pushState=(_s,_t,url)=>pushed.push(url);
run(`state.architecture.components.push({id:'inner',name:'Inner part',parent:'untouched',responsibility:'Inside'});state.layouts['sub:untouched']={version:'sub-v',width:400,height:200,nodes:[{id:'inner',x:20,y:20,width:200,height:80}],edges:[]};advanced=false;path=[];draw()`);
assert.ok(part('untouched').className.split(' ').includes('drillable'));assert.ok(!part('current').className.split(' ').includes('drillable'));
part('untouched').onpointerenter({clientX:1,clientY:1});assert.ok(get('tip').children.some(n=>String(n.textContent).includes('Click to open')));
const d=get('diagram'),onPart={closest:()=>null};
d.onpointerdown({pointerId:7,pointerType:'mouse',button:0,clientX:100,clientY:100,target:onPart});d.onpointermove({pointerId:7,clientX:150,clientY:110});d.onpointerup({pointerId:7});
part('untouched').onclick({detail:1});await tick();await tick();
assert.equal(run('path.length'),0,'a pan drag must not open the part');
part('current').onclick({detail:1});await tick();
assert.equal(run('path.length'),0);assert.ok(get('diagram').className.split(' ').includes('focusing'));assert.equal(run('target.id'),'current');
run('closeEditor()');
part('untouched').onclick({detail:1});await tick();await tick();
assert.equal(run("path.join('/')"),'untouched');assert.equal(run('view'),'sub:untouched');assert.equal(pushed.at(-1),'#/untouched');
assert.equal(get('crumbs').hidden,false);assert.ok(part('inner'));
assert.equal(get('editor').hidden,false);assert.equal(get('target-title').textContent,'Unconnected part');
assert.equal(run('target.diagram_id'),'architecture');assert.equal(get('open-sub').hidden,true);
context.location={hash:'#/'};context.onpopstate();await tick();await tick();
assert.equal(run('path.length'),0);assert.equal(get('crumbs').hidden,true);assert.ok(part('untouched'));
// Keyboard: Enter opens a part with an inside, Space only focuses it.
part('untouched').onkeydown({key:' ',target:part('untouched'),preventDefault(){}});await tick();assert.equal(run('path.length'),0);assert.equal(run('target.id'),'untouched');
part('untouched').onkeydown({key:'Enter',target:part('untouched'),preventDefault(){}});await tick();await tick();assert.equal(run("path.join('/')"),'untouched');
run(`closeEditor();path=[];delete state.layouts['sub:untouched'];state.architecture.components=state.architecture.components.filter(c=>c.id!=='inner');draw()`);
// Derived levels: Shift-click marks a file, the request names its path and
// binds to the enclosing reviewed part; area selection marks several files.
{
const code={kind:'files',dir:'pkg',part:'added',architecture:{components:[{id:'file-1',name:'a.go',responsibility:'File pkg/a.go',files:['pkg/a.go']},{id:'file-2',name:'b.go',responsibility:'File pkg/b.go',files:['pkg/b.go']}],connections:[{id:'use-1',source:'file-1',target:'file-2',label:'uses X',protocol:''}]},layout:{version:'derived-v1',width:600,height:200,nodes:[{id:'file-1',x:20,y:20,width:160,height:72},{id:'file-2',x:300,y:20,width:160,height:72}],edges:[{id:'use-1',source:'file-1',target:'file-2',points:[[180,56],[300,56]]}]}};
context.code=code;run(`advanced=true;path=['added'];derivedView={key:'code:added',data:code,snapshot:codeSnapshot('code:added',code)};draw()`);
assert.equal(run('view'),'code:added');assert.equal(get('area').disabled,false);
part('file-1').onclick({detail:1,shiftKey:true});
assert.equal(get('editor').hidden,false);assert.equal(get('decl-row').hidden,false);assert.ok(get('selection-meta').textContent.includes('a.go'));
const posts=[];context.fetch=async(url,opt)=>{const body=JSON.parse(opt.body);posts.push(body);const next=JSON.parse(JSON.stringify(run('state')));next.comments.push({id:'c'+posts.length,target:body.target,...(body.derived?{derived:{part:'added',elements:body.derived.items.map(i=>({kind:i.to?'connection':'file',...i})),breadcrumb:['added','pkg','a.go'],note:'derived from code, not reviewed'}}:{}),request_type:body.request_type,comment:body.comment,status:'queued',resolved:false});return {ok:true,json:async()=>next};};
get('request-text').value='Why does a.go use b.go?';get('decl-name').value='Serve';await get('submit').onclick();
const sent=posts.at(-1);
assert.deepEqual(sent.derived,{part:'added',items:[{path:'pkg/a.go'}],name:'Serve'});
assert.equal(sent.target.kind,'component');assert.equal(sent.target.id,'added');assert.equal(sent.target.diagram_id,'architecture');
assert.ok(part('file-1').children.some(n=>n.className==='feedback-dot'),'open derived request shows a dot on the file');
assert.ok(!part('file-2').children.some(n=>n.className==='feedback-dot'));
assert.ok(run("acceptBlocker()!==t('accept_wait_reply')&&acceptBlocker()!==t('accept_open_request')"),'an open derived request never blocks Accept');
run('closeEditor()');run(`state.comments=state.comments.map(c=>({...c,status:'answered',resolved:true}));target={kind:'area',diagram_id:view,layout_version:'derived-v1',rect:{x:0,y:0,width:600,height:200},component_ids:['file-1','file-2'],connection_ids:['use-1']};openEditor()`);
get('request-text').value='Explain these files';await get('submit').onclick();
assert.deepEqual(posts.at(-1).derived,{part:'added',items:[{path:'pkg/a.go'},{path:'pkg/b.go'},{path:'pkg/a.go',to:'pkg/b.go'}]});
run('renderHistory()');assert.ok(JSON.stringify(get('history-list').children.at(-1).children).includes('pkg/a.go'));
// A reviewed part opened into its derived level keeps a reviewed panel: no
// derived fields, the builder picker allowed, and a plain request on the part.
run(`closeEditor();state.comments=[];path=[];derivedView=null;draw()`);
const prevFetch=context.fetch;context.fetch=async(url,opt)=>String(url).startsWith('/api/codeview')?{ok:true,json:async()=>({...code,part:'current'})}:prevFetch(url,opt);
part('current').onclick({detail:1});for(let i=0;i<6;i++)await tick();
assert.equal(run('view'),'code:current');assert.equal(get('editor').hidden,false);assert.equal(run('target.id'),'current');assert.equal(run('target.diagram_id'),'architecture');
assert.equal(get('decl-row').hidden,true);assert.ok(!get('selection-meta').textContent.includes('Derived'));
get('request-text').value='Is this part still needed?';await get('submit').onclick();
assert.equal(posts.at(-1).derived,undefined);assert.equal(posts.at(-1).target.id,'current');assert.equal(posts.at(-1).target.diagram_id,'architecture');
context.fetch=prevFetch;
run(`closeEditor();state.comments=[];advanced=false;path=[];derivedView=null;draw()`);
}
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

// Every area has a label even when two columns share the Interfaces role.
context.areaColumns=[['entry'],['cli','tui'],['config','api','browser-html'],['store'],['foundation']];
const areaNames=run('bandNames(areaColumns)');
assert.equal(areaNames.length,5);
assert.ok(areaNames.every(n=>n.trim()),`unlabelled architecture area: ${JSON.stringify(areaNames)}`);
