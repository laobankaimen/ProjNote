import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
const source=await readFile(new URL('../src/nextPlan.ts',import.meta.url),'utf8')
const output=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText
const {appendPlan,appendTomorrowSection,candidatePlanText}=await import(`data:text/javascript;base64,${Buffer.from(output).toString('base64')}`)
const addition=candidatePlanText([{key:'tasks:1',kind:'tasks',id:'1',version:2,title:'设备联调',project_id:'p',assignee:'王工',content:'测试串口\n核对日志',reason:'明天开始'}])
assert.equal(addition,'- 设备联调\n  测试串口\n  核对日志')
assert.equal(appendPlan('手写：联系客户',addition),'手写：联系客户\n'+addition)
assert.equal(appendTomorrowSection('## 完成工作\n已完成验收',addition),'## 完成工作\n已完成验收\n\n## 明日计划\n\n'+addition+'\n')
const existing='## 完成工作\n保留内容\n\n## 明日计划\n- 手写计划\n\n## 需协调\n待客户确认'
const appended=appendTomorrowSection(existing,addition)
assert.ok(appended.includes('- 手写计划\n'+addition),'existing plan survives before appended candidates')
assert.ok(appended.endsWith('## 需协调\n待客户确认'),'later section survives')
assert.equal((appended.match(/^## 明日计划$/gm)||[]).length,1,'reuse an existing tomorrow section')
assert.equal(appendTomorrowSection(existing,''),existing,'empty selection never changes prose')
assert.equal(appendPlan('手写计划',''),'手写计划')
assert.equal(candidatePlanText([{title:'同一内容',content:'同一内容'}]),'- 同一内容','record content is not duplicated when title already contains it')
const legacy='## 后续计划\n- 旧版手写计划'
assert.equal(appendTomorrowSection(legacy,addition),legacy+'\n'+addition+'\n','old generated plan section is reused without duplicating headings')
console.log('PASS: candidate formatting, handwritten text preservation, existing tomorrow section, following sections, empty selection')
