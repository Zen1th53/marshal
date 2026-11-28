// Shared browser demo and interaction fixture: two areas, six leaf components.
const finding=(title,severity,evidence,source='conformance')=>({title,severity,evidence,source,status:'open'});
const node=(id,x,y,width=200,height=90)=>({id,x,y,width,height});
const edge=(id,from,to,caption)=>({id,points:[from,to],label_at:[(from[0]+to[0])/2,(from[1]+to[1])/2],caption});
const componentFinding=finding('Checkout imports payment storage directly','error','web/checkout/cart.ts:42');
const edgeFinding=finding('Charge calls bypass the declared retry policy','warning','services/api/charge.go:81','reviewer');
module.exports={title:'Commerce platform',notice:'Accepted revision 3 · current HEAD · 5 open findings',
 members:{'':['experience','services'],experience:['web','checkout','account'],services:['api','payments','ledger']},
 layouts:{architecture:{width:820,height:260,nodes:[node('experience',30,65,300,130),node('services',470,65,300,130)],edges:[edge('experience--services',[330,130],[470,130],'HTTPS')]},
 'sub:experience':{width:820,height:220,nodes:[node('web',30,60),node('checkout',310,60),node('account',590,60)],edges:[edge('web--checkout',[230,105],[310,105],'cart')]},
 'sub:services':{width:820,height:220,nodes:[node('api',30,60),node('payments',310,60),node('ledger',590,60)],edges:[edge('api--payments',[230,105],[310,105],'charge'),edge('payments--ledger',[510,105],[590,105],'receipt')]}},
 findings:{checkout:[componentFinding],'api--payments':[edgeFinding]},
 areas:{experience:[componentFinding,finding('Shared session policy differs across clients','warning','web/session.md','reviewer')],services:[edgeFinding,finding('Service timeout budget is incomplete','info','services/README.md','reviewer')],'':[finding('Project boundary documentation needs an owner','warning','OPERATIONS.md','reviewer')]}};
