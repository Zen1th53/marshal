const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
class Element {
 constructor(tag) { this.tag=tag; this.children=[];this.attrs={};this.value='';this.checked=false;this.scrollHeight=150;this.style={setProperty(){}}; }
 appendChild(n) {n.parentNode=this;n.ownerSVGElement=this.tag==='svg'?this:this.ownerSVGElement;this.children.push(n);return n;}
 addEventListener(k,f) {this.listeners=this.listeners||{};this.listeners[k]=f;}
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
const context=vm.createContext({document,$,Set,window:{innerWidth:320,innerHeight:500,addEventListener(){}},evGet:()=>Promise.resolve({ok:true,d:{}})});
vm.runInContext(source.slice(source.indexOf('  // ---- Accepted architecture workspace'),source.indexOf('  // ---- Architecture ---')),context);
context.snapshot={notice:'HEAD',layouts:{architecture:{width:1200,height:700,nodes:[{id:'api',x:10,y:10,width:200,height:100}],edges:[]}}, findings:{api:[{title:'<script>unsafe</script>',severity:'error',source:'conformance',status:'open',evidence:'api.go:3'}]},areas:{}};
vm.runInContext('awSnapshot=snapshot;awDraw()',context);
const svg=$('aw-views').children[0].children[0];assert.equal(svg.attrs.width,1200,'natural size shrank');
const group=svg.children[1].children[0];assert.equal(group.attrs.tabindex,0);
group.onkeydown({key:'Enter',preventDefault(){},stopPropagation(){}});
assert.equal(svg.children.at(-1).tag,'foreignObject');
const popup=svg.children.at(-1);assert.ok(popup.attrs.x>=0);assert.ok(popup.attrs.x+popup.attrs.width<=320,'popover not clamped');
assert.equal(popup.children[0].children[2].children[0].children[0].textContent,'<script>unsafe</script>');
events.keydown({key:'Escape'});assert.equal(popup.removed,true);
group.onclick({stopPropagation(){}});events.click({target:new Element('div')});assert.equal(svg.children.at(-1).removed,true);
vm.runInContext('awSeverity="warning"',context);vm.runInContext('awDraw()',context);let current=$('aw-views').children[0].children[0].children[1].children[0];assert.equal(current.children.length,5,'severity count retained');
vm.runInContext('awOnly=true',context);vm.runInContext('awDraw()',context);assert.equal($('aw-views').children[0].children[0].children[0].children.length,0,'problem-only did not hide clean node');

// Severity and problem-place filtering also apply to exact edge markers.
vm.runInContext('awOnly=false;awSeverity=""',context);
context.snapshot.layouts.architecture.edges=[{id:'api--data',points:[[10,20],[300,20]],label_at:[100,20],caption:'queries'}];
context.snapshot.findings['api--data']=[{title:'edge mismatch',severity:'warning',source:'reviewer',status:'open',evidence:'api.go:8'}];
vm.runInContext('awDraw()',context);
const edgeDrawing=$('aw-views').children[0].children[0].children[1];
const edgeMarker=edgeDrawing.children.find(c=>c.attrs['aria-label']==='queries: 1 findings');
assert.ok(edgeMarker,'exact edge marker missing');edgeMarker.onkeydown({key:'Enter',preventDefault(){},stopPropagation(){}});
assert.equal($('aw-views').children[0].children[0].children.at(-1).children[0].children[1].textContent,'api--data');
$('aw-views').children[0].children[0].children.at(-1).children[0].children[0].onclick();
assert.equal(vm.runInContext('awPopup',context),null,'close button failed');

assert.equal(popup.attrs.height,154,'popover should fit content');
assert.equal(popup.children[0].attrs['data-side'],'left','popover should flip when right has no room');
$('aw-severity-error').onclick();assert.equal(vm.runInContext('awSeverity',context),'error');
$('aw-only').onclick();assert.equal(vm.runInContext('awOnly',context),true);

context.snapshot=require('./architecture-fixture.cjs');
vm.runInContext('awOnly=false;awSeverity="";awSnapshot=snapshot;awDraw()',context);
assert.equal($('aw-views').children.length,3);
const root=$('aw-views').children[0].children[0].children[0];
assert.equal(root.attrs['aria-label'],'Commerce platform: 1 findings');
assert.ok(!JSON.stringify($('aw-views').children.map(c=>c.children[0].attrs)).includes('Unplaced'));

// Geometry is measured at the clicked block, with left flip and viewport clamp.
const testSVG=new Element('svg'), viewport=new Element('div'), anchor=new Element('g');
viewport.appendChild(testSVG);
viewport.getBoundingClientRect=()=>({left:10,top:20,right:1010,bottom:720});
testSVG.getBoundingClientRect=()=>({left:10,top:20,width:1000,height:700});
anchor.getBoundingClientRect=()=>({left:50,top:140,right:250,bottom:230,height:90});
context.testSVG=testSVG;context.anchor=anchor;context.window.innerWidth=1200;context.window.innerHeight=800;
vm.runInContext('awOpen(testSVG,anchor,"checkout",snapshot.findings.checkout)',context);
assert.equal(testSVG.children.at(-1).attrs.x,248,'popup should open right of block');
assert.equal(testSVG.children.at(-1).attrs.y,120,'popup should align with block top');
anchor.getBoundingClientRect=()=>({left:800,top:650,right:1000,bottom:740,height:90});
vm.runInContext('awOpen(testSVG,anchor,"checkout",snapshot.findings.checkout)',context);
assert.equal(testSVG.children.at(-1).attrs.x,422,'popup should flip left');
assert.ok(testSVG.children.at(-1).attrs.y+testSVG.children.at(-1).attrs.height<=700,'popup should clamp vertically');
context.window.innerWidth=390;
viewport.getBoundingClientRect=()=>({left:10,top:20,right:370,bottom:720});
vm.runInContext('awOpen(testSVG,anchor,"checkout",snapshot.findings.checkout)',context);
assert.ok(testSVG.children.at(-1).attrs.x+testSVG.children.at(-1).attrs.width<=360,'popup should stay in visible scroll area');

const currentPopup=testSVG.children.at(-1);
$('aw-views').listeners.scroll({target:currentPopup.children[0]});
assert.equal(currentPopup.removed,undefined,'internal scrolling must keep the popover open');
$('aw-views').listeners.scroll({target:viewport});
assert.equal(currentPopup.removed,true,'diagram scrolling should dismiss stale positioning');

// Captions and findings badges fit between adjacent blocks at natural size.
const serviceDrawing=$('aw-views').children[2].children[0].children[1];
const charge=serviceDrawing.children.find(c=>c.attrs['aria-label']==='charge: 1 findings');
assert.equal(charge.children[0].attrs.class,'aw-edge-pill');
assert.equal(charge.children[0].attrs.x+charge.children[0].attrs.width/2,270);
assert.ok(charge.children[0].attrs.x>=230);
assert.ok(charge.children[0].attrs.x+charge.children[0].attrs.width<=310);
assert.ok(charge.children[charge.children.length-1].children[0].attrs.cy-11>=charge.children[0].attrs.y+charge.children[0].attrs.height);
context.snapshot.layouts.architecture.edges=[{id:'routed',points:[[330,130],[350,130],[350,230]],label_at:[340,130],caption:'route'}];
vm.runInContext('awDraw()',context);
const routed=$('aw-views').children[0].children[0].children[1].children.find(c=>c.attrs['aria-label']==='route: 0 findings');
assert.equal(routed.children[0].attrs.x+routed.children[0].attrs.width/2,350);
assert.equal(routed.children[0].attrs.y+13,180);

// A clean area still receives a plane, and findings use circular severity markers.
context.snapshot.areas={};
vm.runInContext('awSnapshot=snapshot;awDraw()',context);
const overview=$('aw-views').children[0].children[0];
const cleanArea=overview.children[1].children.find(c=>c.attrs['aria-label']==='experience: 0 findings');
assert.ok(cleanArea.attrs.class.includes('aw-zone'),'area styling must use membership, not findings');
assert.equal(cleanArea.children[0].attrs.class,'aw-plane');
assert.equal(cleanArea.children[1].attrs.class,'aw-tag');
const checkout=$('aw-views').children[1].children[0].children[1].children.find(c=>c.attrs['aria-label']==='checkout: 1 findings');
const marker=checkout.children.at(-1);
assert.ok(marker.attrs.class.includes('aw-error'));
assert.equal(marker.children[0].tag,'circle');
assert.equal(marker.children[1].textContent,'!');
assert.equal(marker.children[3].textContent,1);
checkout.onclick({stopPropagation(){}});
const card=$('aw-views').children[1].children[0].children.at(-1).children[0].children[2].children[0];
assert.equal(card.children[2].textContent,'error');
assert.equal(card.children[3].attrs['data-status'],'open');
assert.ok(card.children[4].textContent.includes('web/checkout/cart.ts:42'));
const serviceEdge=$('aw-views').children[2].children[0].children[1].children.find(c=>c.tag==='polyline');
assert.ok(serviceEdge.attrs.class.includes('aw-problem'));
assert.equal(serviceEdge.attrs['marker-end'],'url(#aw-arrow-sub:services-problem)');
