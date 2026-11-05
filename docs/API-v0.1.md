# v0.1 接口契约

成功 `{ "data": ... }`；失败 `{ "error": {"code": "...", "message": "..."} }`。列表 data 为 `{items:[], total:0}`。所有请求同源，写请求 JSON，带 `X-ProjNote-Request: 1`。时间由服务端生成；日期 YYYY-MM-DD，时间 RFC3339。空 project_id 表示未归类或跨项目日志。

- GET /api/health → {version}
- GET /api/today → {date,tasks:[],records:[],stats:{projects,tasks_open,tasks_done,issues_open,records_today,unclassified}}
- GET/POST /api/{projects,tasks,records,issues,worklogs}
- GET/PATCH/DELETE /api/{kind}/{id}，PATCH 带当前 version（乐观并发控制）；创建不带 id、version、created_at。
- GET 列表支持 project_id、status、q、limit（默认 100，上限 500）、offset；records 增加 type。
- POST /api/issues/{id}/transition → 同 PATCH，带 status 和 version；也可直接 PATCH。
- POST /api/worklogs/generate {date,period_type:'day'|'week',project_id?} → {content,log_date,period_type,project_id}，仅生成草稿，不覆盖已保存日志。
- GET /api/search?q=... → {items:[{entity_type,entity_id,project_id,title,body}],total}
- GET/PUT /api/settings → {operator,...}，operator 默认“本人”。
- POST /api/demo：仅空库初始化示例项目、任务、记录、问题，显式用户触发。
- GET /api/audit/verify → {ok,total,broken_at?}
- GET /api/backups → {items:[{name,size,created_at}],total}
- POST /api/backups → {name,size,created_at}
- GET /api/backups/{name}/download → zip
- POST /api/attachments：multipart 字段 file → {id,filename,url,mime,size,sha256}；创建记录传 attachments 数组，仅接受本实例已上传 ID。
- GET /api/attachments/{id} → 原文件；禁止 SVG/HTML 内联显示。

实体字段：

- project: id,code,name,customer,phase,manager,start_date,end_date,status(active/paused/closed/archived),description,created_at,updated_at,version
- task: id,project_id,parent_id,wbs_code,name,assignee,plan_start,plan_end,progress(0..100),status(todo/doing/done/canceled),priority(1..3),estimate_hours,description,actual_start,actual_end,overdue(bool),created_at,updated_at,version
- record: id,code,project_id,type(note/inspection/abnormal/photo/decision/other),title,content,location,occurred_at,tags(string[]),attachments(object[]),created_at,updated_at,version
- issue: id,code,project_id,title,description,severity(critical/major/minor/suggestion),source,status(open/analyzing/fixing/verifying/closed/rejected),owner,due_at,root_cause,solution,verify_result,rejection_reason,closed_at,created_at,updated_at,version; 详情包含 history 数组。
- worklog: id,project_id,period_type(day/week),log_date,content,created_at,updated_at,version；同项目、周期、日期仅一篇。

草稿生成与保存分离。问题关闭从首版就必须验证。归档项目及其全部子实体不可写。done/closed 终态不得直接编辑。逾期为派生字段，不覆盖 status。

实现补充（2026-11-04）：

- ID 为32位随机十六进制；实例数据库 `app.db`，显式复数实体表；迁移使用 `PRAGMA user_version=1`。
- `entity_type` 返回复数资源名。搜索最多500条，支持limit/offset并返回capped；备份列表也支持limit/offset。
- 首版不开放DELETE（405），等待软删除/回收站完成。任务done和问题closed/rejected不可直接修改；重开后续实现。
- `POST /api/system/shutdown` 显式退出服务；关闭网页不结束进程。
- 附件PNG/JPEG/GIF，最多20MB/4000万像素；返回thumb_url，原图不可变，单条最多9张。系统会重建附件URL并校验已上传ID。
- 所有瞬时时间统一UTC落盘，按系统本地日期计算今日；`due_at` 在首版是纯日期。
- 创建编号目前为PRJ/REC/ISS+日期+序号；规则配置后续实现。速记保留原始正文，同时提取标签和唯一匹配项目；`!提醒`尚未实现。
- 父任务进度按有效叶子权重汇总，取消项排除。父状态/工时/进度是派生值，不接受手改。
- CLI恢复会校验完整包、schema/integrity/fk、审计与原图引用，替换前创建回滚包；中断恢复残留时拒绝正常启动。
