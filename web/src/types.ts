export interface Entity { id: string; created_at: string; updated_at: string; version: number }
export interface Project extends Entity {code:string;name:string;customer:string;phase:string;manager:string;start_date:string;end_date:string;status:'active'|'paused'|'closed'|'archived';description:string}
export interface Task extends Entity {project_id:string;parent_id:string;wbs_code:string;name:string;assignee:string;plan_start:string;plan_end:string;progress:number;status:'todo'|'doing'|'done'|'canceled';priority:number;estimate_hours:number;description:string;actual_start:string;actual_end:string;overdue:boolean}
export interface Attachment {id:string;filename:string;url:string;thumb_url?:string;mime:string;size:number;sha256:string}
export interface RecordEntry extends Entity {code:string;project_id:string;type:string;title:string;content:string;location:string;occurred_at:string;tags:string[];attachments:Attachment[]}
export interface Issue extends Entity {code:string;project_id:string;title:string;description:string;severity:string;source:string;status:string;owner:string;due_at:string;root_cause:string;solution:string;verify_result:string;rejection_reason:string;closed_at:string;history?:Array<{id?:string;from_status?:string;to_status?:string;status?:string;created_at?:string;operator?:string;action?:string}>}
export interface Worklog extends Entity {project_id:string;period_type:'day'|'week';log_date:string;content:string}
export interface Page<T> {items:T[];total:number}
export interface Today {date:string;tasks:Task[];records:RecordEntry[];stats:{projects:number;tasks_open:number;tasks_done:number;issues_open:number;records_today:number;unclassified:number}}
export interface SearchHit {entity_type:string;entity_id:string;project_id:string;title:string;body:string}
export interface Backup {name:string;size:number;created_at:string}
