# v0.2 增量接口

日期2026-11-05。沿用v0.1同源JSON、专用写头、服务端UTC时间与version并发保护；用户确认GJB5000B二级作为项目目标，不代表工具自动取得符合性认证。

- POST `/api/tasks/{id}/move` `{version,parent_id,before_id?,expected_versions}`；before_id为空表示同父末尾；非空parent_id/before_id须在expected_versions包含当前版本。
- POST `/api/tasks/{id}/reopen` `{version,reason}`，已完成叶子带原因重开；父进度/状态跟随重算。
- POST `/api/tasks/batch` `{items:[{id,version}],changes:{assignee?,status?,plan_start?,plan_end?}}`，最多100，全部成功或全部回滚。
- POST `/api/records/batch-classify` `{items:[{id,version}],project_id}`，同事务归类。
- DELETE `/api/{kind}/{id}?version=...`，移动到回收站，不删除物理文件；父任务级联删除需显式cascade和子项版本确认。
- GET `/api/trash?limit=&offset=`；POST `/api/trash/{kind}/{id}/restore` `{version}`；恢复不覆盖活跃实体，复查归档/父任务/日志唯一性。
- 列表支持`limit/offset`，增量`cursor`、`next_cursor`由Store返回；记录支持`unclassified=1`、`date_from/date_to`（兼容`from/to`）、`tag`。搜索返回实体分组可用的复数entity_type，全部结果按后端排序分页，不以首500条假作总量。
- GET/PUT `/api/settings` 新增稳定dataset_id、固定gjb_level='2'、setup_completed、backup_keep_count(2..365默认30)、backup_max_mb(0不限，或100..1000000)、backup_auto(0/1)。dataset_id/gjb_level不可由客户端修改。
- GET `/api/system/info` `{data_dir,executable,platform,gjb_level,dataset_id,backup:{last_success,last_error,checked_at,in_progress,warning,available_bytes,archive_name},migration_note}`。
- POST `/api/system/maintenance-plan` `{action:'relocate',target_dir}` 或 `{action:'restore',backup_name}` → `{commands:[],notes:[],source_dir,target_dir?,backup_path?,platform}`，只生成可审阅的停服CLI步骤。
- CLI `--relocate /absolute/empty/directory` 停服后完整复制当前数据并校验；原目录保留，目标目录不能与源互相包含；迁移恢复点受保护。
- POST `/api/backups/prune` `{confirm:true}`按已保存策略清理旧普通备份；新包先验证完整性。最近有效包与升级/恢复/迁移回滚点不可自动删除。

v1数据库升级至v2前，程序创建受保护完整快照；新程序接受schema1/2备份并在私有暂存区验证/迁移。原v0.1无法读取v2数据库。网页不会直接替换运行中的数据库。

GET `/api/tags?q=` 返回 `{items:string[],total}`，有效记录标签去重，最多1000条；关键词最多100字，可输入后缩小候选。回收站不参与补全。
