import { api, notify } from './api'
export type ExportSourceKind='worklogs'|'project_daily_logs'|'issues'|'meeting_minutes'
export type ExportFormat='docx'|'md'|'json'|'xlsx'|'html'
export interface ExportTemplate {id:string;name:string;format?:string;description:string;sha256:string;fields:string[]}
export interface ExportJob {id:string;state:'queued'|'running'|'succeeded'|'failed';source_kind:ExportSourceKind;source_id:string;source_version:number;format:ExportFormat;template_id:string;template_hash:string;output_name:string;output_sha256:string;size:number;error:string;created_at:string;updated_at:string;version:number}
export function builtinTemplate(kind:ExportSourceKind){return kind==='issues'?'builtin-issue':'builtin-journal'}
export async function queueWordExport(kind:ExportSourceKind,source:{id:string;version:number}){const job=await api<ExportJob>('/api/export-jobs',{method:'POST',body:JSON.stringify({source_kind:kind,source_id:source.id,source_version:source.version,template_id:builtinTemplate(kind),format:'docx'})});notify('Word 导出任务已创建，可在导出中心查看进度');location.hash=`#/exports?job=${encodeURIComponent(job.id)}`;return job}
export function exportState(state:string){return ({queued:'排队中',running:'正在生成',succeeded:'已完成',failed:'生成失败'} as Record<string,string>)[state]||state}
export function exportKind(kind:string){return ({worklogs:'个人日志',project_daily_logs:'项目日志',issues:'问题台账',meeting_minutes:'会议纪要'} as Record<string,string>)[kind]||kind}

export function exportFormatLabel(format:string){return ({docx:'Word',md:'Markdown',json:'JSON',xlsx:'Excel',html:'打印稿'} as Record<string,string>)[format]||format}
export async function queuePrintExport(kind:ExportSourceKind,source:{id:string;version:number}){const job=await api<ExportJob>('/api/export-jobs',{method:'POST',body:JSON.stringify({source_kind:kind,source_id:source.id,source_version:source.version,format:'html'})});notify('打印稿正在生成，完成后可打开并打印或保存为 PDF');location.hash=`#/exports?job=${encodeURIComponent(job.id)}`;return job}
