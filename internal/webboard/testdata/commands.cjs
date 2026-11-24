const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const script = fs.readFileSync('page/app.js', 'utf8');
const requests = [];
const context = vm.createContext({
  crypto: require('node:crypto').webcrypto,
  Uint8Array,
  auth: () => ({}),
  fetch: (path, options) => { requests.push(JSON.parse(options.body)); return Promise.resolve({ok:true, json:()=>Promise.resolve({status:'preview'})}); }
});
vm.runInContext(script.slice(script.indexOf('  function workCapabilities('), script.indexOf('  function commandPanel(')), context);
vm.runInContext(script.slice(script.indexOf('  function newSettingRequest('), script.indexOf('  function rememberSetting(')), context);
vm.runInContext(script.slice(script.indexOf('  function settingPost('), script.indexOf('  function settingReceipt(')), context);
context.frame = {settingsActions:{projectId:'project', capabilities:[
  {op:'task/retry', target:{kind:'task',id:'selected'}, revision:3, runId:'run', attemptId:'attempt', token:'retry-token'},
  {op:'task/skip', target:{kind:'task',id:'selected'}, revision:3, runId:'run', attemptId:'attempt', token:'skip-token'},
  {op:'lane/restart', target:{kind:'lane',id:'pane'}, taskId:'selected', revision:3, runId:'run', attemptId:'pane-attempt', token:'lane-token'},
  {op:'task/retry', target:{kind:'task',id:'sibling'}, revision:3, runId:'run', attemptId:'sibling-attempt', token:'sibling-token'},
  {op:'run/stop-workers', target:{kind:'run',id:'run'}, revision:0, runId:'run', attemptId:'workers', token:'workers-token'},
  {op:'run/pause', target:{kind:'run',id:'run'}, revision:0, runId:'run', attemptId:'plan', token:'pause-token'}
]}};
(async () => {
  assert.equal(vm.runInContext('workCapabilities(frame,"selected").length',context),3);
  const request = vm.runInContext('newWorkRequest(frame.settingsActions.capabilities[1], {reason:"scope change"})',context);
  assert.equal(request.op,'task/skip'); assert.equal(request.runId,'run'); assert.equal(request.attemptId,'attempt');
  assert.equal(request.target.id,'selected'); assert.equal(request.payload.reason,'scope change');
  context.body=request;
  await vm.runInContext('settingPost(body)',context);
  assert.equal(requests[0].token,'skip-token');
  request.attemptId='replacement';
  await assert.rejects(vm.runInContext('settingPost(body)',context));
  assert.equal(requests.length,1);
  // Run-wide controls keep their own run binding and token.
  assert.equal(vm.runInContext('runCapabilities(frame).length',context),2);
  assert.equal(vm.runInContext('workCapabilities(frame,"run").length',context),0);
  context.body=vm.runInContext('newWorkRequest(runCapabilities(frame)[1], {reason:"inspect"})',context);
  assert.equal(context.body.target.kind,'run'); assert.equal(context.body.attemptId,'plan');
  await vm.runInContext('settingPost(body)',context);
  assert.equal(requests[1].token,'pause-token'); assert.equal(requests[1].op,'run/pause'); assert.equal(requests[1].payload.reason,'inspect');
  context.body.attemptId='changed-plan';
  await assert.rejects(vm.runInContext('settingPost(body)',context));
  assert.equal(requests.length,2);
})().catch(error=>{console.error(error);process.exitCode=1;});
