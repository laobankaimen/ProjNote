export interface Entity { id: string; created_at: string; updated_at: string; version: number }
export interface Project extends Entity {code:string;name:string;customer:string;phase:string;manager:string;start_date:string;end_date:string;status:'active'|'paused'|'closed'|'archived';description:string}
export interface Task extends Entity {sort_order?:number;depth?:number;has_children?:boolean;actual_hours?:number;project_id:string;parent_id:string;wbs_code:string;name:string;assignee:string;assignee_member_id?:string|null;plan_start:string;plan_end:string;progress:number;status:'todo'|'doing'|'done'|'canceled';priority:number;estimate_hours:number;description:string;actual_start:string;actual_end:string;overdue:boolean}
export interface Attachment {id:string;filename:string;url:string;thumb_url?:string;mime:string;size:number;sha256:string}
export interface RecordEntry extends Entity {reminder_due_at?:string|null;reminder_parse_hint?:string;reminder?:{id:string;due_at:string;version:number;done_at:string}|null;code:string;project_id:string;type:string;title:string;content:string;location:string;occurred_at:string;tags:string[];attachments:Attachment[]}
export interface Issue extends Entity {code:string;project_id:string;title:string;description:string;severity:string;source:string;status:string;owner:string;due_at:string;root_cause:string;solution:string;verify_result:string;rejection_reason:string;closed_at:string;history?:Array<{id?:string;from_status?:string;to_status?:string;status?:string;created_at?:string;operator?:string;action?:string}>}
export interface Worklog extends Entity {project_id:string;period_type:'day'|'week'|'month';log_date:string;content:string}
export interface Page<T> {items:T[];total:number;next_cursor?:string}
export interface Today {date:string;tasks:Task[];records:RecordEntry[];reminders?:Reminder[];time_minutes_today?:number;stats:{projects:number;tasks_open:number;tasks_done:number;issues_open:number;records_today:number;unclassified:number;time_minutes_today?:number;reminders_due?:number}}
export interface SearchHit {entity_type:string;entity_id:string;project_id:string;title:string;body:string}
export interface Backup {name:string;size:number;created_at:string;protected?:boolean}

export interface TrashEntry {id:string;entity_type:string;entity_id?:string;title:string;name?:string;content?:string;log_date?:string;deleted_at:string;version:number}

export interface Reminder extends Entity {project_id:string;record_id:string;title:string;due_at:string;status:string;done_at?:string}
export interface TimeEntry extends Entity {assignee:string;project_name?:string;task_name?:string;project_id:string;task_id:string;work_date:string;minutes:number;note:string;deleted_at:string}

export interface MeetingMinute extends Entity {code:string;project_id:string;title:string;meeting_date:string;location:string;attendees:string;content:string;deleted_at:string}
