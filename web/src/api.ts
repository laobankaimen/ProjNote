import { reactive } from 'vue'
import type { Page, Project } from './types'
export const ui = reactive({ toast: '', error: false, projects: [] as Project[], projectsLoaded: false, revision: 0, datasetId: '', dataDir: '' })
let toastTimer: ReturnType<typeof setTimeout> | undefined
export function notify(message: string, error = false) { ui.toast = message; ui.error = error; clearTimeout(toastTimer); toastTimer = setTimeout(() => { ui.toast = '' }, error ? 9000 : 3500) }
export function messageOf(error: unknown) { return error instanceof Error ? error.message : '请求失败，请稍后重试' }
export class ApiError extends Error { constructor(message:string,public status:number,public code:string){super(message)} }
export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.method && options.method !== 'GET') { headers.set('X-ProjNote-Request', '1'); if (!(options.body instanceof FormData)) headers.set('Content-Type', 'application/json') }
  let response: Response
  try { response = await fetch(path, { ...options, headers }) } catch { throw new Error('无法连接本机服务，请检查 ProjNote 是否正在运行') }
  let body: {data?:T;error?:{code:string;message:string}}
  try { body = await response.json() } catch { throw new Error(`服务返回了无效响应（${response.status}）`) }
  if (!response.ok || body.error) throw new ApiError(body.error?.message || `请求失败（${response.status}）`,response.status,body.error?.code||'request_failed')
  return body.data as T
}
export function query(values: Record<string, string | number | undefined>): string { const params = new URLSearchParams(); Object.entries(values).forEach(([key, value]) => { if (value !== '' && value !== undefined) params.set(key, String(value)) }); return params.toString() }
export async function all<T>(path: string, values: Record<string,string|number|undefined> = {}): Promise<T[]> {
  const items: T[] = []; let total = 1;let cursor=''
  while (items.length < total) { const page = await api<Page<T>>(`${path}?${query({...values,limit:500,...(cursor?{cursor}:{offset:items.length})})}`);cursor=page.next_cursor||''; total = page.total; if (!page.items.length) break; items.push(...page.items) }
  return items
}
export async function loadProjects() { ui.projects = await all<Project>('/api/projects'); ui.projectsLoaded = true }
export function projectName(id: string) { return ui.projects.find(p => p.id === id)?.name || (id ? '项目已不可用' : '未归类') }
export function isArchived(id: string) { return ui.projects.some(p => p.id === id && p.status === 'archived') }
export const projectStatuses: Record<string,string> = { active:'进行中',paused:'已暂停',closed:'已结项',archived:'已归档' }
export const taskStatuses: Record<string,string> = {todo:'未开始',doing:'进行中',done:'已完成',canceled:'已取消'}
export const recordTypes: Record<string,string> = {note:'随手记',inspection:'现场巡检',abnormal:'异常记录',photo:'现场照片',decision:'决策记录',other:'其他'}
export const issueStatuses: Record<string,string> = {open:'待处理',analyzing:'分析中',fixing:'修复中',verifying:'待验证',closed:'已关闭',rejected:'已驳回'}
export const severities: Record<string,string> = {critical:'严重',major:'重要',minor:'一般',suggestion:'建议'}
export function dateLabel(value?: string) { if (!value) return '未设置'; const date = new Date(/^\d{4}-\d{2}-\d{2}$/.test(value)?value+'T00:00:00':value); return Number.isNaN(date.valueOf()) ? value : date.toLocaleDateString('zh-CN',{month:'2-digit',day:'2-digit'}) }
export function timeLabel(value?:string) { if (!value) return ''; const date = new Date(value); return Number.isNaN(date.valueOf()) ? '' : date.toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit'}) }
export function localDate() { const d=new Date(); return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}` }
export function downloadMarkdown(content:string,name:string) { const a=document.createElement('a'); a.href=URL.createObjectURL(new Blob([content],{type:'text/markdown;charset=utf-8'})); a.download=name; a.click(); setTimeout(()=>URL.revokeObjectURL(a.href),1000) }

export function pickFields<T extends object>(source: object, shape: T): T { return Object.fromEntries(Object.keys(shape).map(key=>[key,key in source?(source as Record<string,unknown>)[key]:shape[key as keyof T]])) as T }

export function dateTimeLabel(value?: string) { if (!value) return '未设置'; const date = new Date(value); return Number.isNaN(date.valueOf()) ? value : date.toLocaleString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }) }

export async function softDelete(kind:string,entity:{id:string;version:number},name:string){if(!window.confirm(`将「${name}」移入回收站？可以在回收站恢复。`))return false;await api(`/api/${kind}/${entity.id}?version=${entity.version}`,{method:'DELETE',body:JSON.stringify({version:entity.version})});notify('已移入回收站');return true}
export function dateTimeLocal(value?:string){if(!value)return '';const d=new Date(value);if(Number.isNaN(d.valueOf()))return '';return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}T${String(d.getHours()).padStart(2,'0')}:${String(d.getMinutes()).padStart(2,'0')}`}
export function durationLabel(minutes:number){return `${Math.floor(minutes/60)} 小时${minutes%60?` ${minutes%60} 分钟`:''}`}
export function isProjectReadOnly(id:string){return ui.projects.some(p=>p.id===id&&['closed','archived'].includes(p.status))}
