import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';

// Shipped client and component with synthetic responses; no real model IO.
class ServiceError extends Error { constructor(message, code, status) { super(message); this.code=code; this.status=status; } }
const value={base_url:'https://api.deepseek.com',model:'deepseek-chat',daily_limit:50,key_configured:false,revision:0,configured:false,model_config_id:'',sending_enabled:false};
let response=structuredClone(value), code=200;
const requests=[];
const source=stripTypeScriptTypes(readFileSync(new URL('../apps/desktop/src/model-config-api.ts',import.meta.url),'utf8')).replace(/^import .* from '\.\/api';\r?\n/m,'const {SERVICE,ServiceError}=transport;\n').replace(/^export /gm,'')+'\nmodule.exports={getModelConfig,saveModelConfig};';
const module={exports:{}};
runInNewContext(source,{module,transport:{SERVICE:{url:'http://127.0.0.1:17890',error:null},ServiceError},AbortController,DOMException,window:{setTimeout:()=>1,clearTimeout(){}},fetch:async(url,options)=>{requests.push({url,...options});return {ok:code===200,status:code,json:async()=>structuredClone(response)}}});
const api=module.exports, signal=()=>new AbortController().signal;
assert.equal((await api.getModelConfig('synthetic-token',signal())).revision,0);
assert.equal(requests.at(-1).headers.Authorization,'Bearer synthetic-token');
assert.equal(requests.at(-1).method,'GET');
const input={expected_revision:0,base_url:value.base_url,model:value.model,daily_limit:50,api_key:'synthetic-ephemeral-key',confirmed:true};
await api.saveModelConfig('synthetic-token',signal(),{...input,platform_token:'must-not-enter-request'});
assert.equal(requests.at(-1).method,'PATCH');
assert.deepEqual(JSON.parse(requests.at(-1).body),input);
assert.equal(requests.at(-1).credentials,'omit');
assert.equal(new URL(requests.at(-1).url).search,'');
response={...value,api_key:'must-never-be-returned'};
await assert.rejects(api.getModelConfig('synthetic-token',signal()),e=>e.code==='INVALID_RESPONSE');
code=409;response={error:{code:'AI_CONFIG_STALE',message:'private provider raw data'}};
await assert.rejects(api.saveModelConfig('synthetic-token',signal(),input),e=>e.code==='AI_CONFIG_STALE'&&!e.message.includes('private'));

function harness(bindings){
 const slots=[];let cursor=0,dirty=true,tree,effects=[],unmounted=false;
 const hooks={
  useState(initial){const i=cursor++;if(!(i in slots))slots[i]=initial;return[slots[i],v=>{assert.equal(unmounted,false);const n=typeof v==='function'?v(slots[i]):v;if(!Object.is(n,slots[i])){slots[i]=n;dirty=true;}}]},
  useRef(initial){const i=cursor++;if(!(i in slots))slots[i]={current:initial};return slots[i]},
  useEffect(callback,deps){const i=cursor++,old=slots[i];if(!old||deps.some((v,j)=>!Object.is(v,old.deps[j])))effects.push(()=>{old?.cleanup?.();slots[i]={deps,cleanup:callback()};});},
 };
 const raw=readFileSync(new URL('../apps/desktop/src/AIModelPanel.tsx',import.meta.url),'utf8').replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm,'').replace(/^import ['"][^'"]+['"];?\r?$/gm,'').replace('export function AIModelPanel','function AIModelPanel');
 const transformed=transformSync('AIModelPanel.tsx',raw,{jsx:{runtime:'classic',pragma:'jsx',pragmaFrag:'Fragment'}});assert.equal(transformed.errors.length,0);
 const component={exports:{}};
 runInNewContext(transformed.code+'\nmodule.exports=AIModelPanel;',{module:component,...hooks,...bindings,ServiceError,AbortController,jsx:(type,supplied,...children)=>({type,props:{...(supplied??{}),children}}),Fragment:'fragment'});
 return {get tree(){return tree},async flush(){for(let i=0;i<25;i++){if(dirty){cursor=0;dirty=false;tree=component.exports({token:'synthetic-token',onAuthFailure(){throw Error('unexpected 401')}});const queue=effects;effects=[];for(const e of queue)e();}await Promise.resolve();}},unmount(){for(const slot of slots)slot?.cleanup?.();unmounted=true;}};
}
function nodes(t){if(!t||typeof t!=='object')return[];return[t,...t.props.children.flat(Infinity).flatMap(nodes)]}
function field(panel,id){const node=nodes(panel.tree).find(n=>n.props.id===id);assert.ok(node,'missing '+id);return node}
const saved=[];let failSave=false;
const panel=harness({getModelConfig:async()=>structuredClone(value),saveModelConfig:async(_token,_signal,input)=>{saved.push(structuredClone(input));if(failSave)throw new ServiceError('synthetic failure','AI_CONFIG_UNAVAILABLE');return{...value,configured:true,key_configured:true,revision:1,model_config_id:'synthetic-config'}}});
await panel.flush();
assert.equal(field(panel,'ai-model-key').props.type,'password');
field(panel,'ai-model-key').props.onChange({target:{value:'synthetic-ui-key'}});
await panel.flush();
nodes(panel.tree).find(n=>n.type==='input'&&n.props.type==='checkbox').props.onChange({target:{checked:true}});
await panel.flush();
await nodes(panel.tree).find(n=>n.type==='form').props.onSubmit({preventDefault(){}});
await panel.flush();
assert.equal(saved.at(-1).api_key,'synthetic-ui-key');
assert.equal(field(panel,'ai-model-key').props.value,'','successful save must clear key from component input');
failSave=true;
field(panel,'ai-model-key').props.onChange({target:{value:'synthetic-retry-key'}});
nodes(panel.tree).find(n=>n.type==='input'&&n.props.type==='checkbox').props.onChange({target:{checked:true}});
await panel.flush();await nodes(panel.tree).find(n=>n.type==='form').props.onSubmit({preventDefault(){}});await panel.flush();
assert.equal(field(panel,'ai-model-key').props.value,'synthetic-retry-key','failed save must preserve ephemeral retry input');
panel.unmount();
console.log('AI model configuration checks passed: authenticated GET/PATCH, write-only key, target consent, response validation, secret clearing, retry preservation and component lifecycle. Synthetic data only.');
