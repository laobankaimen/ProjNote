import type { Entity, Task } from './types'
export interface PlanVersion {id:string;project_id:string;number:number;name:string;description:string;created_by:string;created_at:string;hash:string;task_count:number;version:number;snapshot?:{snapshot_format:number;project:Record<string,unknown>;tasks:Task[]}}
export interface PlanEnd {id:string;number:number;name:string;created_at:string;hash:string;task_count:number;current:boolean}
export interface FieldDifference {field:string;before:unknown;after:unknown}
export interface PlanComparison {project_id:string;from:PlanEnd;to:PlanEnd;summary:{added:number;removed:number;changed:number;moved:number;reordered:number;total:number};added:Task[];removed:Task[];changed:Array<{id:string;before:Task;after:Task;fields:FieldDifference[];moved:boolean;reordered:boolean}>}
export type RequirementStatus='pending'|'doing'|'accepted'|'void'
export interface Requirement extends Entity {project_id:string;code:string;title:string;description:string;acceptance_criteria:string;owner:string;source:string;status:RequirementStatus;revision:number;acceptance_note:string;change_note:string;task_ids:string[];linked_tasks?:Array<Partial<Task>&{id:string;deleted_at?:string}>;deleted_at?:string}
export interface RequirementRevision {requirement_id:string;revision:number;created_by:string;created_at:string;change_note:string;snapshot:Record<string,unknown>;hash:string}
export interface RequirementComparison {from:RequirementRevision;to:RequirementRevision;fields:FieldDifference[];task_links:{added:unknown[];removed:unknown[]};changed:boolean}
export const requirementStatuses:Record<RequirementStatus,string>={pending:'待处理',doing:'进行中',accepted:'已验收',void:'已作废'}
export const planningLabels:Record<string,string>={project_id:'项目',title:'需求标题',description:'说明',acceptance_criteria:'验收准则',acceptance_note:'验收说明',change_note:'修订原因',owner:'负责人',source:'来源',status:'状态',task_ids:'关联任务',name:'名称',parent_id:'上级任务',sort_order:'同级顺序',wbs_code:'WBS编码',plan_start:'计划开始',plan_end:'计划结束',assignee:'负责人',estimate_hours:'预计工时',priority:'优先级',version:'数据版本',revision:'需求修订'}
export function planningValue(value:unknown){if(value===null||value===undefined||value==='')return '（空）';if(typeof value==='object')return JSON.stringify(value,null,2);return String(value)}
export function revisionTasks(value:Record<string,unknown>):Array<Record<string,unknown>>{return Array.isArray(value.linked_tasks)?value.linked_tasks as Array<Record<string,unknown>>:[]}
