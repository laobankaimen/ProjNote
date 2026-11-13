export const projectViews = ['tasks','progress','plan-versions','requirements'] as const
export type ProjectView = typeof projectViews[number]
export const projectViewLabels:Record<ProjectView,string> = {tasks:'任务计划',progress:'进度视图','plan-versions':'计划版本',requirements:'需求管理'}
export function isProjectView(page:string):page is ProjectView { return (projectViews as readonly string[]).includes(page) }
export function projectHref(projectId:string,view:ProjectView='tasks',entityId='') {
 return `#/projects/${encodeURIComponent(projectId)}/${view}${entityId?'?id='+encodeURIComponent(entityId):''}`
}
export function readRoute(hash:string) {
 const [path,search='']=hash.replace(/^#\/?/,'').split('?'),parts=(path||'').split('/'),params=new URLSearchParams(search)
 if(parts[0]==='projects'&&parts[1]&&isProjectView(parts[2]||''))return {page:parts[2]!,projectId:decodeURIComponent(parts[1]),entityId:params.get('id')||''}
 return {page:parts[0]||'today',projectId:params.get('project_id')||'',entityId:params.get('id')||''}
}
