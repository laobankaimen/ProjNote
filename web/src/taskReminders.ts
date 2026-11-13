import type { Entity, Today } from './types'
export interface TaskReminder extends Entity {task_id:string;task_name:string;project_id:string;project_name:string;due_at:string;notified_at:string;done_at:string;last_error:string;task_status:string}
export interface TaskReminderStatus {supported:boolean;description:string}
export type TodayWithTaskReminders=Today&{task_reminders?:TaskReminder[];stats:Today['stats']&{task_reminders_due?:number}}
export function pendingTaskReminder(value:TaskReminder){return !value.done_at&&!['done','canceled'].includes(value.task_status)}
