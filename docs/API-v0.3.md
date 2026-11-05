# v0.3首个增量接口契约

日期：2026-11-05。状态：已对照本轮Store实现并通过真实程序API/CLI集成；具体证据与未验收范围见验收记录。沿用v0.1/v0.2同源JSON、`X-ProjNote-Request: 1`写头、服务端UTC瞬时时间、稳定dataset_id、version并发保护及归档只读。GJB5000B二级为已确认目标配置，不是认证结论。

本轮先补P0操作缺口（完整项目任务树、行内修改、变更历史和速记一次提醒），再提供P301独立工时源。Word/Excel模板、正式评审/基线和重复提醒不因本版号自动完成。

## 通用约定

- 成功`{"data":...}`，失败`{"error":{"code":"...","message":"..."}}`。创建201，读取/修改/软删/恢复200；参数或业务输入无效400，未找到404，版本冲突/归档只读/恢复冲突409。
- URL使用`time-entries`，SQL和返回的`entity_type`使用`time_entries`；提醒资源使用`reminders`。其他资源沿用projects/tasks/records/issues/worklogs。
- 新实体返回id、created_at、updated_at、version、deleted_at。创建由服务端生成这些字段；PATCH及软DELETE必须携带当前version。列表/统计/日志聚合排除软删除。
- 纯日期`YYYY-MM-DD`按系统本地业务日解释；due_at等瞬时时间必须为带时区RFC3339，落库统一UTC。应用页面快捷键不是操作系统全局热键；提醒在应用今日页查看/处理，不承诺后台系统通知。

## 完整项目任务树、行内修改与历史

| 方法 | 路径 | 请求/响应 |
|---|---|---|
| GET | `/api/projects/{id}/tree` | `data={items:[task+{depth,has_children}],total}`；返回项目全部活跃任务，按WBS父先子后顺序，不受普通50/100条列表分页截断 |
| PATCH | `/api/tasks/{id}` | 沿用通用修改，`{version,name?,assignee?,plan_start?,plan_end?,progress?,status?,priority?,estimate_hours?,description?}`；服务端仍执行汇总/日期/终态/归档规则 |
| GET | `/api/{kind}/{id}/history` | kind为projects/tasks/records/issues/worklogs/time-entries/reminders，返回审计`{items,total,next_cursor}`；条目含id、created_at、operator、entity_type、entity_id、action、before_json、after_json、before、after、prev_hash、hash；audit.id降序，支持limit/offset或cursor |

树深度根节点为0，子节点逐级+1，has_children依据完整活跃树计算，不依据当前可见页。非法项目404；归档项目允许只读查看。不可把子任务因为分页没取到而当根节点或叶子。汇总进度/状态由服务端派生，不允许行内输入绕开。任务Get/List/ListPage/Tree/Today返回actual_minutes与actual_hours，按有效工时条目汇总当前子树（含自身历史条目）且每条只计一次；项目级无任务工时不虚分配给任务。

历史使用真实审计快照，不从当前实体反推旧值；不替换issue.history已有的流转专用时间线。before_json/after_json为原始JSON字符串（可为字符串"null"），before/after为解析后的同等JSON值；正常界面只能查看，不能编辑审计内容。已软删实体仍可读取历史，实体不存在则404；这是审计查看接口，不把实体恢复为活跃，也不允许写入历史。

## 工时记录

实体业务字段：`{project_id,task_id,work_date,minutes,note}`。project_id必须有效；task_id省略或空字符串表示项目级工时，非空时必须属于同项目活跃叶子任务（done允许）。minutes是整数分钟，必须>0；work_date必须是真实日历日期。

| 方法 | 路径 | 请求/响应 |
|---|---|---|
| GET/POST | `/api/time-entries` | 列表`{items,total,next_cursor:""}`；支持project_id/task_id/work_date/date_from/date_to/q及limit/offset；列表额外project_name/task_name；创建仅传业务字段，返回实体 |
| GET/PATCH | `/api/time-entries/{id}` | 详情；修改`{version,...业务字段}`，不得修改服务端字段 |
| DELETE | `/api/time-entries/{id}?version=...` | 软删除，释放本日活跃工时额度；沿用回收站响应 |
| GET | `/api/time-entries/summary?date_from=&date_to=&project_id=&task_id=` | 请求日期范围含首尾；返回total_minutes、by_day、by_project、by_task，字段如下文定义 |
| POST | `/api/trash/time_entries/{id}/restore` | `{version}`；返回恢复实体，须复查项目/任务/日额度，不覆盖活跃记录 |

summary响应：

```json
{"total_minutes":540,
 "by_day":[{"date":"2026-11-02","minutes":540,"over_eight_hours":true}],
 "by_project":[{"project_id":"...","project_name":"...","minutes":540}],
 "by_task":[{"task_id":"...","project_id":"...","task_name":"...","minutes":540}]}
```

无任务分组task_id为空、task_name为“未关联任务”。24小时总额冲突为409/daily_time_limit；单条不在1..1440、非整数、非法日期、跨项目或非叶子任务为400。查询范围date_from/date_to首尾包含，颠倒范围400。

工时唯一权威源是time_entries，日报/周报/任务展示按其聚合，不将已保存日志正文或旧hours缓存相加。按一个用户的同一work_date跨项目活跃总分钟校验24小时硬上限（1440），包含创建/改日期/改分钟/恢复；删除不计。超过8小时（480分钟）提示而不阻断，超过12小时可额外提示。8小时提示由summary.by_day[].over_eight_hours给出，不额外把警告文本塞进创建响应；12小时特别提示尚未作为本轮契约。

工时补记保留work_date与实际created_at；已完成叶子任务可登记实际工时；closed项目拒绝登记/修改/恢复，archived项目拒绝新增/修改/软删/恢复。任务仍有活跃工时时禁止移入回收站；历史聚合只按time_entries记录计算，任务后续成为汇总节点也不能重复累加已有工时。恢复遇额度/引用冲突必须整事务失败，旧回收记录保留。

生成`POST /api/worklogs/generate`日/周草稿时，同一工时只归集一次；按project_id和业务日期范围过滤，未保存草稿不得改变统计。返回沿用content/log_date/period_type/project_id四字段，兼容旧客户端将整个草稿提交保存；不新增total_minutes字段。正文单独列“合计N分钟（X小时）”和按日分钟/超过8小时提示；精确数值由summary接口查询，正文编辑不会反向改工时。

## 一次提醒与速记

提醒字段：`{title,project_id?,record_id?,due_at}`，另有done_at、version和通用元数据；done_at非空表示完成，没有独立done布尔字段。due_at是带时区RFC3339瞬时；project_id允许空则为跨项目/未归类。record_id若有必须有效且归属兼容，不接受跨项目隐式绑定。

| 方法 | 路径 | 请求/响应 |
|---|---|---|
| GET/POST | `/api/reminders` | 列表`{items,total}`；创建一次提醒；支持status=pending（默认）/done/all、project_id、record_id、due_from/due_to RFC3339及limit/offset，due_from含边界、due_to不含 |
| GET/PATCH | `/api/reminders/{id}` | 详情；修改`{version,title?,project_id?,record_id?,due_at?}`，done_at非空拒绝PATCH，返回409 |
| POST | `/api/reminders/{id}/done` | `{version}`；置done并写done_at/审计；过期version409，重复完成不能当新成功事件 |
| DELETE | `/api/reminders/{id}?version=...` | 软删除；沿用回收站version保护 |
| POST | `/api/trash/reminders/{id}/restore` | `{version}`；复查项目/记录引用，返回恢复实体 |
| GET | `/api/today` | 原字段之外新增reminders数组、顶层time_minutes_today及stats.time_minutes_today/stats.reminders_due；包含无项目或active/paused项目中due_at早于本地明日00:00的未完成项（含过期），完成后移除待办；closed/archived提醒仍可在普通列表查阅 |

记录POST/PATCH可带`reminder_due_at`（RFC3339或null）。记录写入、相关提醒的创建/修改与审计在同一事务；任何一部分失败不留下半条记录或提醒。记录编辑不得反复生成第二个活跃提醒；新建省略该字段时才解析正文；普通编辑省略字段保持原提醒，不因新正文含!语法重建。显式null或空字符串软删除原提醒；显式RFC3339更新原活跃提醒ID。已完成提醒拒绝显式改期409，若要新建须先明确清除（保留旧提醒于回收站）再设置。记录返回reminder:{id,due_at,version,done_at}|null、reminder_due_at及reminder_parse_hint。独立提醒接口对关联提醒的新建/修改/完成/软删/恢复会同步增加关联record.version并写reminder_change审计，使此前打开的记录编辑页收到409；记录事务内部同步提醒不重复增加自己的版本。

速记正文支持`!今天`、`!明天`等后端中文日期语法；默认本地09:00，`!今天`09:00已过时使用当前时间后1分钟。支持独立空白分隔token !今天/!明天/!周一～!周日（!周天同义）/!YYYY-MM-DD，后可跟HH:MM；不承诺工作日历/节假日推算。显式reminder_due_at优先于正文；选取首个可识别token；当周对应星期时间已过则推下周同一星期；日期严格校验，不把2月30日归一化。无法识别或时间不合法保留原文并返回解析提示，不创建提醒。保留原文，不把无法识别的文本静默变成错误时间。解析后存UTC；不使用UTC日期代替本地业务日期。

一次提醒不等于每日/每周重复提醒，也不含系统后台通知。done/软删除必须与今日列表和统计同步；同一记录多次编辑、失效version重试及事务回滚不新增重复提醒。

## 本轮集成验收范围

scripts/smoke_v03.py只对新编译二进制和临时合成数据运行，不选择用户默认数据目录。已执行并通过实际API/CLI覆盖：

- 一个项目超过50项任务的完整tree、depth/has_children、行内PATCH的version和真实history/audit。
- 工时创建/汇总、跨项目任务拒绝、整数/真实日期/24小时限制、8小时提示、改时/软删/恢复重新校验、归档写保护。
- 日/周日志从time_entries归集一次；多次生成和已保存日志不改变工时总量。
- 速记一次提醒语法、记录更新不重复、显式提醒改期/完成/version409、今日待办变化。
- 新实体历史与审计真实可核，完整备份/恢复后schema3数据、工时和提醒一致。

上述脚本已对本轮Linux真实构建执行通过；浏览器已验范围见验收记录，Windows实机、真实模板、容量/性能和双周试用仍独立验收。
