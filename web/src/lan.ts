export interface LanMember {id:string;name:string;enabled:boolean;project_ids:string[];task_ids:string[];version:number;auth_version?:number;created_at?:string;updated_at?:string}
export interface LanStatus {running:boolean;enabled:boolean;port:number;addresses:string[];local_owner_url:string;certificate_sha256:string;certificate_download:string;certificate_expires_at:string;error?:string}
export interface LanDailyEntry {id:string;version:number;project_id:string;member_id:string;member_name?:string;work_date:string;content:string;next_plan:string;blockers:string;minutes:number;created_at?:string;updated_at?:string}
export interface LanTask {id:string;project_id:string;parent_id:string;wbs_code:string;name:string;assignee:string;assignee_member_id?:string|null;plan_start:string;plan_end:string;status:string;progress:number;version:number;depth:number;has_children:boolean;can_update:boolean}
export interface LanProject {id:string;name:string;status:string;version:number}
export interface LanWorkspace {member:LanMember;projects:LanProject[];tasks:LanTask[];total:number;dataset_id?:string}
