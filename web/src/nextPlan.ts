export interface NextPlanCandidate {key:string;kind:string;id:string;version:number;title:string;project_id:string;assignee:string;content?:string;reason:string}
export function candidatePlanText(items:NextPlanCandidate[]){return items.map(item=>`- ${item.title}${item.content&&item.content.trim()!==item.title.trim()?`\n  ${item.content.trim().replace(/\n/g,'\n  ')}`:''}`).join('\n')}
export function appendPlan(current:string,addition:string){if(!addition.trim())return current;return `${current}${current.trim()?'\n':''}${addition}`}
export function appendTomorrowSection(current:string,addition:string){
  if(!addition.trim())return current
  const heading=/^##[ \t]+(?:明日计划|后续计划)[ \t]*$/m.exec(current)
  if(!heading)return `${current}${current.trim()?'\n\n':''}## 明日计划\n\n${addition}\n`
  const start=heading.index+heading[0].length,tail=current.slice(start),next=/\n#{1,2}[ \t]+/.exec(tail),end=next?start+next.index:current.length
  return `${current.slice(0,end).trimEnd()}\n${addition}\n${current.slice(end)}`
}
