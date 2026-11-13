export interface NextPlanCandidate {key:string;kind:string;id:string;version:number;title:string;project_id:string;assignee:string;content?:string;reason:string}
export function candidatePlanText(items:NextPlanCandidate[]){return items.map(item=>`- ${item.title}${item.content&&item.content.trim()!==item.title.trim()?`\n  ${item.content.trim().replace(/\n/g,'\n  ')}`:''}`).join('\n')}
function withoutPlanPlaceholder(text:string){return text.replace(/^[ \t]*-[ \t]+待补充[ \t]*\r?$(?:\n)?/gm,'')}
export function appendPlan(current:string,addition:string){if(!addition.trim())return current;const kept=withoutPlanPlaceholder(current).trimEnd();return `${kept}${kept.trim()?'\n':''}${addition}`}
export function appendTomorrowSection(current:string,addition:string){
  if(!addition.trim())return current
  const heading=/^(#{2,6})[ \t]+(?:明日计划|后续计划)[ \t]*\r?$/m.exec(current)
  if(!heading)return `${current}${current.trim()?'\n\n':''}## 明日计划\n\n${addition}\n`
  const start=heading.index+heading[0].length,tail=current.slice(start),next=new RegExp(`\\n#{1,${heading[1]!.length}}[ \\t]+`).exec(tail),end=next?start+next.index:current.length
  const kept=withoutPlanPlaceholder(current.slice(start,end)).trimEnd()
  return `${current.slice(0,start)}${kept}\n${addition}\n${current.slice(end)}`
}
