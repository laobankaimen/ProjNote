import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { api, ApiError, messageOf, ui } from './api'
export interface DraftValue {saved_at:string;value:Record<string,unknown>;base_version?:number;writer_id?:string;storage_key?:string;own?:boolean}
// Each page instance owns a separate key. A cloned tab may inherit sessionStorage,
// so the instance suffix also changes on reload; its previous copy remains recoverable.
function randomId(){return globalThis.crypto?.randomUUID?.()||`${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`}
let tabToken=''
try{tabToken=sessionStorage.getItem('projnote:tab-token')||randomId();sessionStorage.setItem('projnote:tab-token',tabToken)}catch{tabToken=randomId()}
const writerId=`${tabToken}:${randomId()}`
export function useDraft(scope:()=>string,form:object,active:()=>boolean=()=>true,version:()=>number|undefined=()=>undefined){
 const recovery=ref<Conflict>(),available=ref<DraftValue>(),values=ref<DraftValue[]>([]),baseline=ref(''),ready=ref(false),storageError=ref('')
 const snapshots=new Map<string,string>();let timer:ReturnType<typeof setTimeout>|undefined
 const key=()=>ui.datasetId?`projnote:draft:${ui.datasetId}:${scope()}`:''
 const ownKey=()=>key()?`${key()}:writer:${writerId}`:''
 function read(){const selectedKey=available.value?.storage_key;values.value=[];available.value=undefined;snapshots.clear();if(!key())return;try{for(let i=0;i<localStorage.length;i++){const candidateKey=localStorage.key(i);if(!candidateKey||(candidateKey!==key()&&!candidateKey.startsWith(`${key()}:writer:`)))continue;const raw=localStorage.getItem(candidateKey);if(!raw)continue;try{const item=JSON.parse(raw) as DraftValue;if(!item||typeof item.value!=='object'||!item.value||typeof item.saved_at!=='string')continue;values.value.push({...item,storage_key:candidateKey,own:item.writer_id===writerId});snapshots.set(candidateKey,raw)}catch{/* Keep unreadable copies intact. */}}values.value.sort((a,b)=>b.saved_at.localeCompare(a.saved_at));available.value=values.value.find(v=>v.storage_key===selectedKey)||values.value[0]}catch{storageError.value='浏览器草稿读取失败，请先复制重要内容。'}}
 function persist(force=false){if(!ready.value||!active()||!ownKey()||(!force&&JSON.stringify(form)===baseline.value))return;try{const value:DraftValue={saved_at:new Date().toISOString(),value:JSON.parse(JSON.stringify(form)),base_version:version(),writer_id:writerId};localStorage.setItem(ownKey(),JSON.stringify(value));storageError.value=''}catch{storageError.value='浏览器草稿空间不可用，请及时保存或复制内容。'}}
 async function reset(){ready.value=false;clearTimeout(timer);await nextTick();baseline.value=JSON.stringify(form);read();ready.value=true}
 watch([scope,active,()=>ui.datasetId],reset,{immediate:true})
 watch(form,()=>{if(ready.value&&active()){clearTimeout(timer);timer=setTimeout(persist,180)}},{deep:true})
 const dirty=computed(()=>ready.value&&active()&&JSON.stringify(form)!==baseline.value)
 function select(storageKey:string){available.value=values.value.find(v=>v.storage_key===storageKey);recovery.value=undefined}
 function restore(force=false){const item=available.value;if(!item)return;if(!force&&version()!==undefined&&item.base_version!==version()){recovery.value={local:item.value,remote:JSON.parse(JSON.stringify(form)),path:''};return}for(const field of Object.keys(form))if(field in item.value)(form as Record<string,unknown>)[field]=item.value[field];recovery.value=undefined;persist(true)}
 function removeSnapshot(storageKey:string,expected:string){if(localStorage.getItem(storageKey)===expected)localStorage.removeItem(storageKey)}
 // Saving only clears this writer. Other copies require an explicit user discard,
 // even if their content was restored into this editor.
 function discard(){clearTimeout(timer);try{if(ownKey())localStorage.removeItem(ownKey());storageError.value=''}catch{storageError.value='草稿清理失败，数据库保存不受影响。'}baseline.value=JSON.stringify(form);read()}
 function discardAvailable(){const item=available.value;if(!item?.storage_key)return;const expected=snapshots.get(item.storage_key);if(!expected)return;try{removeSnapshot(item.storage_key,expected);storageError.value=''}catch{storageError.value='无法丢弃此草稿，请检查浏览器存储设置。'}read();recovery.value=undefined}
 function canLeave(){persist();if(!dirty.value)return true;return window.confirm(storageError.value?'当前修改尚未保存到数据库，且浏览器草稿保存失败。请先复制内容；仍要离开？':'当前修改尚未保存到数据库。浏览器草稿已保留，确定离开？')}
 function beforeUnload(event:BeforeUnloadEvent){persist();if(dirty.value){event.preventDefault();event.returnValue=''}}
 function storageChanged(event:StorageEvent){if(active()&&key()&&event.key&&(event.key===key()||event.key.startsWith(`${key()}:writer:`)))read()}
 window.addEventListener('beforeunload',beforeUnload);window.addEventListener('storage',storageChanged)
 onUnmounted(()=>{persist();clearTimeout(timer);window.removeEventListener('beforeunload',beforeUnload);window.removeEventListener('storage',storageChanged)})
 return{available,values,select,discardAvailable,recovery,restoreForced:()=>restore(true),clearRecovery:()=>{recovery.value=undefined},dirty,storageError,restore,discard,reset,persist,canLeave,markDirty:()=>{baseline.value='';persist(true)}}
}
export interface Conflict {local:Record<string,unknown>;remote:Record<string,unknown>;path:string}
export function useConflict(){const current=ref<Conflict>();const error=ref('');async function capture(exception:unknown,path:string,local:object){if(exception instanceof ApiError&&exception.status===409){try{current.value={local:JSON.parse(JSON.stringify(local)),remote:await api<Record<string,unknown>>(path),path};return true}catch(e){error.value=messageOf(e)}}return false}return{current,error,capture,clear:()=>{current.value=undefined}}}
