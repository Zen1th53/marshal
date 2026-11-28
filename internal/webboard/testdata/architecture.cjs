const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
class Element {
 constructor(tag) { this.tag=tag; this.children=[];this.attrs={};this.value='';this.checked=false; }
 appendChild(n) {n.parentNode=this;n.ownerSVGElement=this.tag==='svg'?this:this.ownerSVGElement;this.children.push(n);return n;}
 setAttribute(k,v) {this.attrs[k]=v;}
 getBoundingClientRect() {return {left:0,top:0,right:320,bottom:500,width:320,height:500};}
 replaceChildren() {this.children=[];}
 remove() {this.removed=true;}
 focus() {this.focused=true;}
 contains(n) {return n===this || this.children.some(c=>c.contains(n));}
}
const nodes=new Map(), events={};const $=id=>{if(!nodes.has(id))nodes.set(id,new Element('div'));return nodes.get(id);};
const document={createElement:tag=>new Element(tag),createElementNS:(_,tag)=>new Element(tag),addEventListener:(k,f)=>events[k]=f};
const source=fs.readFileSync('page/app.js','utf8');
const context=vm.createContext({document,$,Set,window:{innerWidth:320,innerHeight:500},evGet:()=>Promise.resolve({ok:true,d:{}})});
vm.runInContext(source.slice(source.indexOf('  // ---- Accepted architecture workspace'),source.indexOf('  // ---- Architecture ---')),context);
context.snapshot={notice:'HEAD',layouts:{architecture:{width:1200,height:700,nodes:[{id:'api',x:10,y:10,width:200,height:100}],edges:[]}}, findings:{api:[{title:'<script>unsafe</script>',severity:'error',source:'conformance',status:'open',evidence:'api.go:3'}]},areas:{}};
vm.runInContext('awSnapshot=snapshot;awDraw()',context);
const svg=$('aw-views').children[0].children[0];assert.equal(svg.attrs.width,1200,'natural size shrank');
const group=svg.children[1].children[0];assert.equal(group.attrs.tabindex,0);
group.onkeydown({key:'Enter',preventDefault(){},stopPropagation(){}});
assert.equal(svg.children.at(-1).tag,'foreignObject');
const popup=svg.children.at(-1);assert.ok(popup.attrs.x>=0);assert.ok(popup.attrs.x+popup.attrs.width<=320,'popover not clamped');
assert.equal(popup.children[0].children[2].children[0].textContent,'<script>unsafe</script>');
events.keydown({key:'Escape'});assert.equal(popup.removed,true);
group.onclick({stopPropagation(){}});events.click({target:new Element('div')});assert.equal(svg.children.at(-1).removed,true);
$('aw-severity').value='warning';vm.runInContext('awDraw()',context);let current=$('aw-views').children[0].children[0].children[1].children[0];assert.equal(current.children.length,2,'severity count retained');
$('aw-only').checked=true;vm.runInContext('awDraw()',context);assert.equal($('aw-views').children[0].children[0].children[0].children.length,0,'problem-only did not hide clean node');

// Severity and problem-place filtering also apply to exact edge markers.
$('aw-only').checked=false;$('aw-severity').value='';
context.snapshot.layouts.architecture.edges=[{id:'api--data',points:[[10,20],[300,20]],label_at:[100,20],caption:'queries'}];
context.snapshot.findings['api--data']=[{title:'edge mismatch',severity:'warning',source:'reviewer',status:'open',evidence:'api.go:8'}];
vm.runInContext('awDraw()',context);
const edgeDrawing=$('aw-views').children[0].children[0].children[1];
const edgeMarker=edgeDrawing.children.find(c=>c.attrs['aria-label']==='queries: 1 findings');
assert.ok(edgeMarker,'exact edge marker missing');edgeMarker.onkeydown({key:'Enter',preventDefault(){},stopPropagation(){}});
assert.equal($('aw-views').children[0].children[0].children.at(-1).children[0].children[1].textContent,'api--data');
$('aw-views').children[0].children[0].children.at(-1).children[0].children[0].onclick();
assert.equal(vm.runInContext('awPopup',context),null,'close button failed');
