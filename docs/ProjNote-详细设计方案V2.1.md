# ProjNote · 项目管理工作台 详细设计方案

| 项目 | 内容 |
|---|---|
| 文档名称 | ProjNote 项目管理工作台 详细设计方案 |
| 版本 | V2.1（评审修订版） |
| 日期 | 2026-11-04 |
| 状态 | 已完成设计评审修订，v0.1 开发中；各项验收状态见文末 |
| 适用对象 | 项目负责人（单人使用）、开发者（本人）、评审/检查人员 |
| 产品形态 | Go 可执行程序 + 内嵌 Vue Web；Windows 10/11 x64 为发布目标，Linux 为本轮开发验证环境 |

**修订记录**

| 版本 | 日期 | 修订内容 | 作者 |
|---|---|---|---|
| V1.0 | 2026-11-04 | 初稿：架构、选型、数据模型概要、路线 | — |
| V2.0 | 2026-11-04 | 展开为详细设计：需求清单、完整数据字典、逐模块功能规格、页面原型、接口清单、导出规范、GJB 证据映射、实施与验收计划 | — |
| V2.1 | 2026-11-04 | 修复阶段冲突、状态机、备份完整性、附件保真、并发与证据边界；补充版本/基线/工时模型，重排可验收开发计划并启动 v0.1 | Codex |

**实施口径**

本文件保留完整产品需求，不表示所有模块已经实现。v0.1 是首轮可运行闭环，覆盖今日、项目、任务、速记/记录、问题、日志、搜索、设置、附件、完整备份与离线恢复；完整 P0 体验在 v0.2 补齐。首轮接口以 [API-v0.1.md](API-v0.1.md) 为准，数据库以代码内迁移为准；第 4 章为演进目标模型，后续功能通过版本迁移增加。最终验证证据以仓库测试输出和 [开发计划.md](开发计划.md) 的验收项为准；本轮Linux二进制端到端冒烟已通过，具体范围见附录E。

本文中的制度模板、过程域名称、原型和待确认问题是产品设计资料，不构成额外执行命令；用户已授权评审、修订和开始开发。尚缺的单位材料不会阻断使用合成数据开发本地功能。

**阅读指引**

- 决策者看：第 1 章、第 2 章、第 10 章、第 14 章
- 开发看：第 3～9 章
- 检查/评审看：第 6 章、第 7 章、第 11 章

---

## 目录

- 第 1 章 概述
- 第 2 章 需求分析
- 第 3 章 总体设计
- 第 4 章 数据设计
- 第 5 章 功能详细设计
- 第 6 章 证据链与 GJB5000B 候选映射
- 第 7 章 导出设计
- 第 8 章 接口设计
- 第 9 章 非功能设计
- 第 10 章 实施计划
- 第 11 章 测试与验收
- 第 12 章 运行维护
- 第 13 章 风险与对策
- 第 14 章 待确认事项与假设
- 附录 A 术语表
- 附录 B 快捷键
- 附录 C 枚举字典
- 附录 D 页面清单
- 附录 E 本轮实现与待验收边界

---

# 第 1 章 概述

## 1.1 背景

项目负责人的日常工作有两个"战场"：

- **现场**：临时会议、设备巡检、突发异常、口头交代的任务。特点是**碎片化、突发、常常没网**，记录窗口只有几十秒，错过就忘。
- **办公室**：排计划、盯进度、写周报、整理台账、应对检查。特点是**结构化、要复用、要留痕**。

现有做法的痛点：

| 痛点 | 现状 | 后果 |
|---|---|---|
| 记录分散 | 微信收藏、纸质本、便签、Excel、Word 各一份 | 找不到、对不上、写周报时重新回忆 |
| 重复劳动 | 现场记一遍 → 台账抄一遍 → 周报再写一遍 | 一周至少浪费半天，且容易漏 |
| 证据不成链 | GJB5000B 检查要的过程记录散落各处 | 临时补材料，真实性受质疑 |
| 工具太重 | 项目管理软件要么联网、要么多人、要么复杂 | 一个人用不起来，最后回到 Excel |

## 1.2 目标

**一句话定位**：一个双击就能用、断网也能用、记录零负担、证据能直接导出的**个人项目作战台**。

| 编号 | 目标 | 可度量标准 |
|---|---|---|
| G1 | 单文件交付 | 一个 exe，无安装、无运行时依赖，冷启动 < 1 秒 |
| G2 | 记录零负担 | 任意界面 `Alt+N` 唤起，文字/截图回车即存，全程 < 5 秒 |
| G3 | 项目可控 | WBS、里程碑、进度、工时、问题、风险在一处闭环 |
| G4 | 证据可导出 | 周报/月报/问题台账/评审记录/配置项清单，套用现有 docx 模板一键生成 |
| G5 | 可恢复 | 已确认保存写入事务；完整备份可独立恢复、操作留痕、可整体迁移；恢复目标以演练结果为准 |
| G6 | 离线可用 | 无网络环境全部功能可用 |

## 1.3 范围

### 1.3.1 做（In Scope）

- 单用户、单机运行的项目管理工作台
- 项目 / WBS / 任务 / 里程碑 / 进度 / 工时
- 速记、现场记录、会议纪要、工作日志（日/周/月）
- 问题台账、风险登记、评审记录、配置项与变更（轻量）
- 全局搜索、标签体系、提醒
- 模板化导出（Word / Excel / PDF 打印）
- 自动备份、审计留痕、归档包

### 1.3.2 不做（Out of Scope）

| 不做 | 原因 |
|---|---|
| 多人协作、账号与权限 | 单人使用；操作人姓名是可配置标识，不能代替身份认证或数字签名 |
| 云端同步、移动 App | 单机即服务端，同步复杂度远超收益 |
| 替代 OA / PDM / 配置管理系统 | 只做个人侧证据采集与成型 |
| 完整需求管理与测试管理 | 与核心诉求弱相关，P3 再评估 |
| AI 能力（转写、摘要） | 当前不需要，预留插件位 |
| 组织级过程（OPF/OPD/OT）与供方管理（SAM） | 属单位级体系，不在个人工具范围 |

## 1.4 成功标准

上线 3 个月后自评：

1. 现场记录**当场完成率** > 80%（不再事后回忆）
2. 周报撰写时间从 2 小时降到 **30 分钟以内**
3. 检查前**临时补材料**的工作量下降 70%
4. 连续使用 30 天不中断（说明真的顺手）

## 1.5 术语

| 术语 | 含义 |
|---|---|
| WBS | 工作分解结构，本工具中即任务树 |
| 速记 | 一句话/一张图快速入库的入口，事后归类 |
| 记录 | 现场发生的事项（速记、巡检、异常、照片、决策） |
| 台账 | 结构化清单（问题台账、风险登记册、配置项清单） |
| 证据 | 能支撑 GJB5000B 过程域审核的记录与文档 |
| 模板 | 用户提供的 docx/xlsx 文件，含占位符，导出时填充 |
| 归档包 | 阶段结束时打包的全部记录、文档与哈希清单 |

---

# 第 2 章 需求分析

## 2.1 用户与使用场景

### 2.1.1 用户画像

- 角色：软件项目负责人（兼项目管理、技术把关、对外协调）
- 环境：Windows 10/11 笔记本；现场可能无网络；手机不一定随身/可用
- 习惯：习惯用 Excel/Word 出材料；不喜欢填表单；对"多点几下才能记一条"零容忍
- 约束：项目过程需符合 GJB5000B，检查时要拿得出记录

### 2.1.2 典型场景（用户故事）

| 编号 | 场景 | 用户故事 | 关键要求 |
|---|---|---|---|
| S1 | 现场速记 | 在机房看到接线松动，掏出电脑/手机，10 秒记下并拍照 | 快、能拍照、不用选项目也能先存 |
| S2 | 现场会议 | 甲方口头提了三条要求，会后要变成任务 | 纪要结构化、结论一键转任务 |
| S3 | 每日收工 | 想知道今天干了啥，顺手生成日志 | 自动汇总、可编辑、不用回忆 |
| S4 | 周五写周报 | 汇总本周进度、问题、下周计划 | 一键生成草稿 + 套模板导出 |
| S5 | 问题跟踪 | 上周提的问题现在到哪一步了 | 状态流转、超期提醒、关闭要验证 |
| S6 | 检查前 | 找出某项目的全部评审记录和问题闭环证据 | 全局搜索 + 一键归档包 |
| S7 | 换电脑 | 把工具和数据拷到新机器继续用 | 数据目录整体可迁移 |
| S8 | 断网 | 现场没网也要能记、能查 | 全本地，无外部依赖 |

## 2.2 功能需求清单

> 需求级别：P0=完整基础闭环，P1=汇报与进度，P2=证据与追溯，P3=按需增强。版本交付见2.5；首轮v0.1先交付P0核心，v0.2补齐全部P0。

### 2.2.1 M01 今日工作台

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-01-01 | 今日待办聚合 | 汇总今日到期、进行中、逾期任务，按项目分组 | P0 | 打开首页 1 秒内看到全部待办 |
| FR-01-02 | 逾期高亮与跳转 | 逾期项红色标识，点击进入任务详情 | P0 | 逾期判定与任务状态一致 |
| FR-01-03 | 今日记录流 | 按时间倒序展示当天记录，含缩略图 | P0 | 新增记录后实时出现在顶部 |
| FR-01-04 | 日志草稿生成 | 依据当天任务变更、记录、问题，生成 Markdown 草稿 | P0 | 生成的草稿覆盖当天主要动作 |
| FR-01-05 | 本周概览 | 完成数、新增问题、关闭问题、工时 | P1 | 统计口径与台账一致 |
| FR-01-06 | 未归类提醒 | 超过 7 天未归类的速记在首页提示 | P1 | 可批量归类 |

### 2.2.2 M02 项目管理

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-02-01 | 项目新建/编辑/归档 | 编号、名称、客户、阶段、起止、负责人、状态 | P0 | 编号唯一，归档后只读 |
| FR-02-02 | 项目列表与筛选 | 卡片/列表切换，按状态、阶段、关键字筛选 | P0 | 100 个项目下操作流畅 |
| FR-02-03 | 项目概览 | 进度环、里程碑时间轴、问题分布、工时燃尽 | P1 | 数据实时准确 |
| FR-02-04 | 阶段与里程碑 | 里程碑计划/实际日期、达成判定、关联评审 | P1 | 未通过评审不可标记达成（可强制并留痕） |
| FR-02-05 | 项目模板 | 从模板创建项目，预置 WBS 与检查单 | P2 | 模板可编辑、可导入导出 |

### 2.2.3 M03 WBS 与任务

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-03-01 | WBS 树编辑 | 增删改、拖拽调层级与顺序，自动重算编码 | P0 | 拖拽后编码正确、无环 |
| FR-03-02 | 任务字段编辑 | 名称、类型、负责人、计划/实际起止、进度、优先级、工时、交付物 | P0 | 行内编辑即时保存 |
| FR-03-03 | 子任务与进度汇总 | 父任务按有效叶子后代计划工时加权，缺省权重1 | P0 | 父汇总只读；重开/取消/移动后保持一致 |
| FR-03-04 | 批量操作 | 多选改状态、负责人、日期 | P1 | 批量操作写审计 |
| FR-03-05 | Excel 导入 | 粘贴/导入 WBS 表 | P1 | 支持层级列与缩进两种格式 |
| FR-03-06 | 任务依赖 | 前置任务、FS/SS 关系 | P2 | 冲突时提示不强锁 |

### 2.2.4 M04 进度视图

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-04-01 | 看板 | 按状态分列，拖拽改状态 | P1 | 拖拽即时生效并留痕 |
| FR-04-02 | 甘特图 | 时间轴、今日线、导出 PNG；依赖线随 FR-03-06 后续增加 | P1 | 200 任务下渲染目标 < 1 秒，须按性能方案实测 |
| FR-04-03 | 日历视图 | 里程碑、截止日、记录 | P1 | 月/周切换 |
| FR-04-04 | 延期识别 | plan_end < 本地今天 且 status∈{todo,doing} → 派生 overdue | P0 | 不覆盖任务 status，不为日期自然变化重复写业务审计 |

### 2.2.5 M05 速记

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-05-01 | 全局唤起 | `Alt+N` 任意页面唤起浮层 | P0 | 不丢失当前页面状态 |
| FR-05-02 | 快捷语法 | `#标签` `@项目` `!时间` 自动补全 | P0 | 解析准确，可回退为普通文本 |
| FR-05-03 | 截图粘贴 | `Ctrl+V` 粘贴剪贴板图片直接存附件 | P0 | 保留原始字节，另生成预览/缩略图 |
| FR-05-04 | 语音备忘 | 录音存为附件 | P1 | 可播放、可下载 |
| FR-05-05 | 未归类池 | 速记先入池，事后批量归类 | P1 | 支持按时间/关键字筛选 |
| FR-05-06 | 连续记录 | `Ctrl+Enter` 保存并继续下一条 | P2 | 保留上下文（项目/标签） |

### 2.2.6 M06 现场记录

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-06-01 | 时间线视图 | 按天分组、类型色标、筛选 | P0 | 万级记录滚动流畅 |
| FR-06-02 | 类型化记录 | 速记/巡检/异常/照片/决策/其他 | P0 | 类型可配置 |
| FR-06-03 | 照片九宫格 | 缩略图墙、点击大图、左右切换 | P0 | 支持键盘操作 |
| FR-06-04 | 地点与定位 | 常用地点下拉、手填；手机可选 GPS | P1 | 地点可记忆 |
| FR-06-05 | 会议纪要 | 时间/地点/主持/参会/议题/结论/待办 | P1 | 可导出纪要 docx |
| FR-06-06 | 结论转任务/问题 | 纪要中的待办一键生成任务或问题 | P1 | 双向关联可追溯 |
| FR-06-07 | 手机拍照 | 手机浏览器调用相机拍摄上传 | P3 | 原图保留；可选压缩副本明确标识，弱网可重试 |

### 2.2.7 M07 工作日志

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-07-01 | 日报 | 按日汇总动作生成草稿，可编辑 | P0 | 覆盖当天任务、记录、问题 |
| FR-07-02 | 周报 | 按周汇总，含进度、问题、下周计划 | P1 | 可套模板导出 docx |
| FR-07-03 | 月报 | 周报聚合 + 度量指标 | P2 | 可导出 |
| FR-07-04 | 工时登记 | 按任务登记工时，日/周汇总 | P1 | 超 8 小时提醒 |
| FR-07-05 | 补记 | 选择历史日期补写日志 | P1 | 统计口径正确 |
| FR-07-06 | 日志导出 | 单篇/批量导出 Markdown / docx | P1 | 格式与模板一致 |

### 2.2.8 M08 问题台账

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-08-01 | 问题登记 | 标题、来源、严重度、分类、责任人、期限 | P0 | 编号自动生成 |
| FR-08-02 | 状态流转 | open→analyzing→fixing→verifying→closed | P0 | 每次流转记录时间与操作人 |
| FR-08-03 | 关闭校验 | 关闭必填原因分析与验证结论；驳回必填原因 | P0 | 与状态流转同版实现，缺失时阻止终结 |
| FR-08-04 | 筛选统计 | 按项目/状态/严重度/责任人/时间 | P1 | 平均关闭周期可算 |
| FR-08-05 | 导入导出 | Excel 导入、台账导出 | P1 | 字段映射可配置 |
| FR-08-06 | 问题报告单 | 单条问题导出 docx 报告单 | P1 | 套用现有模板 |

### 2.2.9 M09 风险管理

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-09-01 | 风险登记 | 描述、类别、可能性、影响、应对策略、责任人 | P2 | 字段完整可导出 |
| FR-09-02 | 等级计算 | 3×3 矩阵自动算高/中/低 | P2 | 等级与矩阵一致 |
| FR-09-03 | 措施跟踪 | 缓解/应急措施、触发条件、状态 | P2 | 可关联任务 |
| FR-09-04 | 登记册导出 | 风险登记册 xlsx/docx | P2 | 与台账口径一致 |

### 2.2.10 M10 评审管理

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-10-01 | 评审记录 | 类型、对象、时间、评审人、结论 | P2 | 可附评审材料 |
| FR-10-02 | 发现转问题 | 评审发现一键生成问题（来源=评审） | P2 | 双向关联，重复转换不生成第二条问题 |
| FR-10-03 | 结论签署 | 编制/审核/批准、日期、版本 | P2 | 签署后锁定 |
| FR-10-04 | 评审记录导出 | 套模板生成评审记录 docx | P2 | 按单位实际模板验收，工具不自行宣称体系符合性 |

### 2.2.11 M11 配置管理（轻量）

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-11-01 | 配置项登记 | 标识、名称、类型、版本、状态、存放位置 | P2 | 标识唯一 |
| FR-11-02 | 基线管理 | 基线名称、包含配置项、建立时间 | P2 | 基线内配置项可追溯 |
| FR-11-03 | 变更申请 | 变更单、影响分析、审批状态 | P2 | 与配置项关联 |
| FR-11-04 | 状态报告 | 配置项状态汇总表 | P2 | 可导出 |

### 2.2.12 M12 全局搜索

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-12-01 | 全局搜索 | `Ctrl+K` 输入即搜，跨项目/任务/问题/记录/日志 | P0 | 1 万条数据下 < 100ms |
| FR-12-02 | 分组与高亮 | 按实体分组、关键词高亮、回车打开 | P0 | 键盘可全程操作 |
| FR-12-03 | 过滤语法 | `#标签` `@项目` `type:issue` `status:open` | P1 | 语法错误有提示 |
| FR-12-04 | 最近访问 | 记录最近打开条目 | P2 | 首页可见 |

### 2.2.13 M13 导出中心

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-13-01 | 模板管理 | 导入/替换/删除 docx、xlsx 模板 | P1 | 模板存数据目录，可迁移 |
| FR-13-02 | 单值替换 | 段落内 `{{project.name}}` 等 | P1 | 保留原样式 |
| FR-13-03 | 表格循环 | 表格行按集合重复填充 | P1 | 行数动态、样式继承 |
| FR-13-04 | Excel 导出 | 台账/统计多 sheet | P1 | 冻结首行、自动列宽 |
| FR-13-05 | 打印/PDF | 专用打印样式页 | P1 | 分页、页眉页脚正确 |
| FR-13-06 | 批量合并 | 多项目合并成一本台账 | P2 | 排序与编号连续 |
| FR-13-07 | 导出留痕 | 记录模板、范围、文件哈希 | P1 | 审计可查 |

### 2.2.14 M14 提醒与待办

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-14-01 | 截止提醒 | 任务/问题到期前提醒 | P1 | 启动时汇总 |
| FR-14-02 | 自定义提醒 | 自由文本 + 时间 + 重复 | P1 | 支持每天/每周 |
| FR-14-03 | 桌面通知 | 系统通知（可选） | P3 | 可关闭 |

### 2.2.15 M15 系统与数据

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-15-01 | 首次启动向导 | 选择数据目录、填写姓名、创建示例项目 | P0 | 3 步内完成 |
| FR-15-02 | 数据目录迁移 | 改路径、校验完整性、迁移附件 | P0 | 迁移后数据一致 |
| FR-15-03 | 自动备份与恢复 | 每日完整快照、保留策略；v0.1 离线 CLI 恢复，v0.2 恢复向导 | P0 | 单个备份独立恢复，校验数据库和全部引用文件；恢复时服务必须停止 |
| FR-15-04 | 单实例与端口 | 二次启动直接开浏览器 | P0 | 端口冲突自动顺延 |
| FR-15-05 | 局域网访问 | 安全会话 + 二维码 + 显式开关 | P3 | 默认关闭，单独设计HTTPS/撤销/访问控制后验收 |
| FR-15-06 | 诊断包 | 导出日志、版本、环境信息 | P1 | 不含业务数据 |
| FR-15-07 | 托盘 | 打开/备份/退出 | P3 | 可选开启 |

### 2.2.16 M16 归档

| 编号 | 功能 | 说明 | 优先级 | 验收要点 |
|---|---|---|---|---|
| FR-16-01 | 状态机与锁定 | 草稿→待评审→已批准→归档，批准后只读 | P2 | 修改生成新版本 |
| FR-16-02 | 归档包 | 一键打包全部记录、文档、附件 | P2 | 目录结构规范 |
| FR-16-03 | 哈希清单 | `manifest.sha256` + 校验工具 | P2 | 可验证内容与清单一致；无外部签名时不能证明清单本身未被重算 |

## 2.3 非功能需求

| 编号 | 类别 | 需求 | 指标 |
|---|---|---|---|
| NFR-01 | 性能 | 冷启动 | < 1 秒（SSD） |
| NFR-02 | 性能 | 万级列表打开 | < 200ms |
| NFR-03 | 性能 | 全局搜索响应 | < 100ms（1 万条） |
| NFR-04 | 容量 | 单库记录数 | ≥ 10 万条 |
| NFR-05 | 容量 | 附件总量 | ≥ 50GB |
| NFR-06 | 可靠性 | 保存确认与事务一致 | WAL + synchronous=FULL；确认提交后再显示已保存；断电能力受操作系统与硬件影响，必须故障演练 |
| NFR-07 | 可靠性 | 自动备份 | 每日完整备份 1 次；保留数量可配置，容量不足明确报告失败，不能先删除唯一有效备份 |
| NFR-08 | 安全 | 默认仅本机监听 | 绑定 127.0.0.1 |
| NFR-09 | 安全 | 上传校验 | 魔数校验 + 路径白名单 |
| NFR-10 | 兼容 | 操作系统 | Windows 10/11 x64 |
| NFR-11 | 兼容 | 办公软件 | Office 2016+ / WPS 2019+ |
| NFR-12 | 兼容 | 显示 | 1080P 起，支持 125%/150% 缩放 |
| NFR-13 | 易用 | 核心操作步数 | 记一条 ≤ 3 步 |
| NFR-14 | 可移植 | 数据迁移 | 停止服务后拷贝数据目录，或使用一致性完整备份；禁止运行时只拷贝 SQLite 主文件 |
| NFR-15 | 体积 | 单文件大小 | < 40MB |

## 2.4 约束与假设

### 2.4.1 约束（C）

| 编号 | 约束 |
|---|---|
| C1 | 单人开发与维护，无专职测试 |
| C2 | Windows 10/11 x64 为发布验收目标；Linux 为本轮开发环境；交叉编译成功不等于 Windows 运行验证通过 |
| C3 | 不接入单位 OA/PDM/配置管理系统 |
| C4 | 开发环境无法访问外网 Go 代理时，使用国内镜像 |
| C5 | 交付物为源码 + 可执行文件 + 使用说明，不含运维服务 |

### 2.4.2 假设（A）

| 编号 | 假设 | 若不成立的影响 |
|---|---|---|
| A1 | 开发与演示仅使用合成或已获准的非敏感数据；真实数据部署属性待单位确认 | 若不能在个人电脑处理：按单位制度另行评估部署介质、存储和分发，不以简单加密或百分比工期替代评估 |
| A2 | 单位使用的标准版本、适用条款和检查单尚待核对 | 第 6 章仅为证据候选映射；未核对权威原文和单位制度，不作成熟度等级或完整符合宣称 |
| A3 | 已有可用的 docx/xlsx 模板 | 若无：需先设计模板，工期 +1 周 |
| A4 | 基准场景为项目 ≤ 50、任务 ≤ 5000/项目、记录 ≤ 2 万/年 | 总任务可能达 25 万，必须分场景测试；先以索引、分页和查询计划优化，不按固定条数直接决定分库 |
| A5 | 用户接受"先记后归类"的工作方式 | 若不接受：速记入口需改为表单式，体验下降 |
| A6 | 单机单用户，但多标签、上传、备份和自动任务仍会并发 | 从首版使用事务、busy_timeout、受控写入和 version 乐观并发；多人是独立架构范围，需要身份、权限、并发、共享附件和备份重设计 |
| A7 | 中文为主，少量英文术语 | 若多语言：需 i18n 框架 |
| A8 | 办公软件为 Office/WPS 主流版本 | 若为老旧版本：docx 兼容需降级 |

## 2.5 需求优先级与 MVP 边界

P0/P1/P2 是需求的重要性和完整版本边界；v0.1/v0.2 等是可交付增量，不将“v0.1 可运行”称为“全部 P0 已完成”。

**完整 P0 必须包含**：FR-01-01～04，FR-02-01/02，FR-03-01～03，FR-04-04，FR-05-01～03，FR-06-01～03，FR-07-01，FR-08-01～03，FR-12-01/02，FR-15-01～04。M01 的本周概览和未归类提醒仍为 P1。

**首轮 v0.1**：本地可运行程序、今日、项目/任务、文字速记/图片附件、记录列表、问题登记与强制关闭校验、日/周日志草稿和显式保存、基础搜索、操作人设置、审计校验、完整备份下载、停服 CLI 恢复。以 API-v0.1.md 的字段和路由为准。

**v0.2 补齐完整 P0 体验**：WBS 拖拽/层级排序、完整图片灯箱、速记补全和连续操作体验、首次向导、迁移/恢复向导、草稿恢复和结构化搜索体验。需求中未在首轮完成的项目必须保留为待办。

**P1/P2 后续**：v0.3 模板导出、工时与进度视图；v0.4 风险、评审、证据版本、配置基线与归档；v1.0 完成发布兼容性和真实双周试用。局域网/手机、AI 等独立延后。

**P0 完整验收**：能在真实项目上连续使用两周，确认保存的记录可找回，附件和备份可独立恢复，问题流转有校验，草稿生成不会覆盖人工编辑内容。性能、Windows 运行和真实模板尚未验证时，不标记通过。

---

# 第 3 章 总体设计

## 3.1 设计原则

| 原则 | 含义 | 体现 |
|---|---|---|
| P1 快于全 | 记录速度优先于字段完整性 | 速记默认只填正文，其余字段可有默认值 |
| P2 一次录入，多处复用 | 同一条数据不在两个地方各写一遍 | 问题→周报→台账→归档自动串联 |
| P3 本地优先 | 无网络可用，数据在用户手里 | 单机 SQLite，无云依赖 |
| P4 数据可迁移 | 数据目录可在停服后整体拷贝、可一致性备份 | SQLite（含 WAL 运行语义）+ 附件 + 模板 + 清单；运行时使用备份接口 |
| P5 模板即接口 | 导出复用用户现有 docx，不重排版式 | 占位符替换而非重新生成文档 |
| P6 可追溯 | 关键动作、来源和历史版本可查 | 同事务审计 + 完整性哈希链 + 版本锁定；不把本机哈希链等同可信签署 |
| P7 渐进增强 | P0记录、P1汇报、P2追溯 | 每阶段独立验收，工具能力不等于组织标准符合性 |
| P8 不过度设计 | 单人工具不引入分布式复杂度 | 无消息队列、无缓存中间件、无微服务 |

## 3.2 架构视图

### 3.2.1 部署视图

```
┌──────────────────────── 用户 Windows 电脑 ────────────────────────┐
│                                                                   │
│   ProjNote.exe（单进程）                                           │
│   ├─ 内嵌前端资源（embed.FS）                                       │
│   ├─ HTTP 服务 127.0.0.1:18600                                     │
│   └─ 数据目录                                                     │
│       ├─ app.db          SQLite（WAL）                             │
│       ├─ attachments/    原图、视频、录音、文档                      │
│       ├─ thumbs/         缩略图                                     │
│       ├─ backups/        每日快照 zip                               │
│       ├─ templates/      用户导入的 docx/xlsx                        │
│       ├─ exports/        导出产物（可清理）                          │
│       └─ logs/           运行日志                                    │
│                                                                   │
│   浏览器（Edge/Chrome） ── HTTP ──▶ 127.0.0.1:18600               │
└───────────────────────────────────────────────────────────────────┘
        ▲
        │ 可选：同一局域网 http://<内网IP>:18600（需开启，默认关闭）
        └ 手机/平板浏览器
```

单机无外部依赖：不连数据库服务器、不连 CDN、不连对象存储、不连第三方 API。

### 3.2.2 请求处理链路

```
浏览器请求
   │
   ▼
[中间件链]  Recovery → 访问日志 → 安全头(CSP/X-CTO) → 请求体限流
   │
   ├─ /api/*  ──▶ 路由(net/http) ──▶ handler ──▶ service ──▶ store(SQL) ──▶ SQLite
   │                                  │
   │                                  ├─ files（附件读写）
   │                                  ├─ audit（写审计）
   │                                  └─ search（更新索引）
   │
   ├─ /files/* ─▶ 静态文件（Range 支持、路径白名单、ETag）
   │
   └─ 其他     ─▶ embed.FS 静态资源；未命中且 Accept: text/html → 返回 index.html（history 回退）
```

分层职责：

| 层 | 职责 | 禁止 |
|---|---|---|
| handler | 参数解析、校验、组装 DTO | 不写 SQL、不写业务规则 |
| service | 业务规则、事务边界、状态机 | 不碰 HTTP 细节 |
| store | SQL、索引、迁移 | 不做业务判断 |
| files | 附件存取、缩略图、哈希 | 不感知业务实体 |

### 3.2.3 后端模块分解

```
internal/
├─ app/          装配：配置加载、DB 初始化、迁移、路由注册、优雅退出
├─ config/       配置结构与环境变量（端口、数据目录、局域网开关）
├─ server/       net/http 路由、中间件、SPA 回退、静态资源
├─ api/          按领域拆分的 handler：project/task/issue/record/worklog/
│                risk/review/config/search/export/backup/system
├─ service/      领域服务：任务树维护、进度汇总、状态机、周报生成、编号分配
├─ store/        仓储：每个聚合一个文件 + 迁移 SQL（embed）
├─ search/       FTS5 索引维护与查询解析（语法、过滤、排序）
├─ files/        存储布局、缩略图、sha256、类型识别、清理
├─ docx/         模板引擎：zip 读写、token 流、占位符替换、表格循环
├─ xlsx/         excelize 封装：样式、列宽、冻结、条件格式
├─ export/       导出编排：取数 → 映射 → 渲染 → 落盘 → 留痕
├─ backup/       快照、zip、保留策略、恢复校验
├─ audit/        审计写入、哈希链、校验
├─ platform/     开浏览器、托盘、单实例锁、路径（win/darwin/linux 构建标签）
└─ webui/        //go:embed all:dist
```

### 3.2.4 前端架构

```
web/src/
├─ main.ts            应用入口、全局错误捕获
├─ router/            路由表（懒加载）
├─ stores/            Pinia：project / task / issue / record / ui / settings
├─ api/               请求封装：统一错误提示、超时、重试
├─ views/             页面：Today / Projects / ProjectDetail / Tasks / Board /
│                     Gantt / Records / Logs / Issues / Risks / Reviews /
│                     Config / Search / Export / Settings
├─ components/        通用：DataTable / TreeTable / Uploader / ImageGrid /
│                     MarkdownEditor / QuickCapture / CommandPalette / Gantt
└─ styles/            主题变量、打印样式（print.css）
```

关键技术点：

- **路由懒加载**，首屏只加载今日工作台
- **虚拟滚动**（长列表、时间线），避免万级 DOM
- **保存确认**：新记录仅在服务端提交成功后显示“已保存”；提交中保留编辑内容和状态。局部乐观更新必须有失败回滚，状态变更携带当前 version，409 时提示重载比较。
- **草稿保护**：草稿按数据集/实体/版本分开存储，显示“草稿保存在本浏览器”与“已写入数据库”的区别。自动保存和崩溃恢复在 v0.2 验收；浏览器关闭不能视为服务退出或服务器已保存。
- **统一错误提示**：后端返回 `{code, message}`，前端 toast + 可展开详情

## 3.3 技术选型

### 3.3.1 选型总表

以下为本环境可复现开发基线，不代表最新版本或受支持发布承诺。实际依赖以 go.mod、go.sum 和前端锁文件为准；正式发布前必须在受支持 Go 基线重新构建并验证。

| 层 | 首轮基线 | 说明 |
|---|---|---|
| 语言 | Go 1.24.0 | 本环境工具链；发布前升级到届时受支持基线并复验 |
| HTTP | net/http + 标准库 | 减少首轮依赖，业务规则独立在 service/store |
| 数据库 | modernc.org/sqlite v1.39.1 | 纯 Go；具体 SQLite 能力在锁定驱动上测试 |
| SQL | database/sql + 手写迁移 | 每连接启用外键；事务内维护业务、搜索和审计 |
| 检索 | SQLite FTS5 trigram + 短词 LIKE | 首轮按当前接口提供基础搜索，语法过滤和性能分阶段验收 |
| 单实例 | Linux 文件锁 / Windows LockFileEx | 系统锁是权威，PID/runtime.json 仅作定位信息；不得只靠 PID 接管 |
| 日志 | log/slog | 标准库 |
| 前端 | Vue 3.5.44 + TypeScript 5.9.3 | 本地打包，不使用运行时 CDN |
| 构建 | Vite 7.3.7 | 按锁文件和匹配的 Node 环境构建 |
| UI/状态/编辑 | 首轮 Vue 组件、CSS、文本编辑 | 暂不引入独立 UI/状态/Markdown 编辑器框架 |
| Word/Excel/图表 | v0.3 按样板实测后锁定 | 候选包括 OOXML 模板处理、Excel 库与图表库，不在首轮承诺复杂模板兼容 |
| 打包 | go:embed + ldflags | 包体、启动、内存属于待实测目标 |

### 3.3.2 备选方案对比

| 决策点 | 选择 | 备选 | 否决理由 |
|---|---|---|---|
| 桌面壳 | 浏览器 + 本地 HTTP | Electron | 包体 100MB+，内存 300MB+ |
| | | Tauri | 需 Rust 工具链、系统 WebView 版本差异 |
| | | Wails | 依赖 WebView2 运行时，老机器缺失 |
| 数据库 | SQLite | BoltDB/Badger | 无 SQL、无关联查询、无 FTS |
| | | 内嵌 PostgreSQL | 体积与启动成本高 |
| ORM | 手写 SQL | GORM | 隐式查询、迁移不可控，表少时是负担 |
| 前端 | Vue 3 | React | 团队无偏好；Vue 模板更贴近"表单密集"场景 |
| 富文本 | Markdown | 富文本 JSON | JSON 结构脆弱、导出需转换、迁移困难 |
| PDF | 浏览器打印 | wkhtmltopdf | 外部二进制，违背单文件 |
| | | chromedp | 依赖本机 Chrome，作为可选增强 |

### 3.3.3 依赖清单（第三方库）

首轮直接依赖为数据库驱动、平台锁适配所需库、Vue/Vite/TypeScript 构建链及 lucide-vue-next 图标（版本见package-lock.json）；完整传递依赖以锁文件为准。后续导出、图表、二维码、托盘库须在对应阶段核实许可、维护状态与平台支持后加入。

发布时生成实际依赖与许可证清单，不用“直接依赖少于 10 个”推断供应链风险。前端必须打包到可执行文件中，运行时不加载外部字体、脚本或样式。

## 3.4 关键技术方案

### 3.4.1 单文件打包

```
构建流程：
  web/  ──锁文件对应的 build 命令──▶  web/dist/
                              │ 拷贝
                              ▼
                    internal/webui/dist/   ← //go:embed all:dist
                              │
                    go build -trimpath -ldflags "-s -w"
                              │
                              ▼
                       dist/ProjNote.exe
```

要点：

- `//go:embed all:dist` 放在 `internal/webui` 包，导出 `FS` 变量；`all:` 前缀确保包含 `_` 开头文件。
- SPA 回退：只对可识别前端路由返回 index.html；API、附件以及不存在的静态资产必须保留 404，不得吞掉接口错误。首轮附件通过 /api/attachments/{id} 读取。
- 静态资源加 `Cache-Control: max-age=31536000, immutable`（文件名带 hash）；`index.html` 用 `no-cache`。
- Windows 发布分别提供图形启动与可用 CLI 恢复入口；隐藏控制台前确认 CLI 错误与退出码仍可见。图标/版本信息在打包验收阶段补齐。
- 体积目标 < 40MB；以真实构建产物记录各平台大小，不能由依赖大小相加直接认定达标。
- 不使用 UPX：压缩后杀软误报率显著上升。

### 3.4.2 SQLite 与中文全文检索

**检索取舍**：LIKE 作为短词与首轮兜底；长词用 FTS5。扫描耗时、索引体积与召回需要用本机真实语料测量，不预设未经验证的毫秒结论。

**分词方案对比**：

| 方案 | 中文效果 | 说明 |
|---|---|---|
| FTS5 `unicode61` | 需语料验证 | 不能直接替代中文子串需求 |
| FTS5 `trigram` | ✅ 3 字以上 | 按 3 字符滑窗建索引，支持任意子串匹配 |
| 自建 bigram 倒排 | ✅ 2 字 | 需自己维护索引表，工作量大 |
| 外部分词 | 需语料验证 | 可能增加词典/依赖与更新成本，按召回结果评估 |

**采用方案**：`trigram` 为主 + `LIKE` 兜底。

```sql
-- 统一检索表（应用层维护，与业务表同事务写入）
CREATE TABLE search_doc (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_type TEXT NOT NULL,      -- project/task/issue/record/worklog/meeting
  entity_id   TEXT NOT NULL,
  project_id  TEXT,
  title       TEXT,
  body        TEXT,               -- 标题+正文+标签拼接
  updated_at  TEXT NOT NULL,
  UNIQUE(entity_type, entity_id)
);

CREATE VIRTUAL TABLE search_fts USING fts5(
  title, body,
  content='search_doc', content_rowid='id',
  tokenize='trigram'
);
```

查询策略：

| 输入长度 | 策略 | 预期 |
|---|---|---|
| ≥ 3 字符 | `search_fts MATCH` + `bm25` 排序 | < 50ms |
| 2 字符 | `LIKE '%kw%'` on search_doc（限制 entity_type 与时间范围） | < 150ms |
| 1 字符 | 仅前缀匹配 title，提示"请输入更多字符" | 即时 |
| 含语法 | 解析 `#标签`/`@项目`/`type:`/`status:` 转为过滤条件 | 同上 |

**验收方法**：用 1 万条和 10 万条分层数据测试技术术语、人名、编号、两字词、三字词和标点。分别记录后端查询、端到端 P50/P95、索引大小、硬件与冷热缓存条件；1000 条仅用于召回冒烟测试。原始用户输入必须按字面词安全转义/参数绑定，不直接拼成 FTS 语法或 SQL。

**索引一致性**：业务写操作与 search_doc 更新在同一事务，软删除同步移除搜索文档。完整校验和重建可手动/后台执行，不把全库重建放在每次冷启动阻塞路径；完整性检查与重建是两个不同动作。

### 3.4.3 docx 模板引擎

**难点**：Word 会把一段文字拆成多个 `<w:r>`（run），占位符 `{{project.name}}` 可能被拆成 `{{pro` + `ject.na` + `me}}`，直接字符串替换会失败。

**算法**：

```
1. 解压模板 docx（zip），读取 word/document.xml（以及 header*.xml/footer*.xml）
2. 用 encoding/xml 的 Decoder.Token() 流式遍历：
   a. 进入 <w:p> 时，收集该段内所有 <w:t> 节点及其文本偏移
   b. 将段内文本拼接为 paragraphText（保留 run 边界映射）
   c. 在 paragraphText 上做正则匹配 {{...}}
   d. 命中时：把替换值写入第一个 run 的 <w:t>，其余涉及的 run 文本置空
      （保留 run 属性，字体/字号/颜色不丢）
   e. 表格循环：遇到 <w:tr> 且该行含 {{#list}} 标记：
      - 复制该行 XML 片段 N 次（N = 集合长度）
      - 第 i 份中 {{#list}} 行替换为 {{item.xxx}} 的实际值
      - 删除原始模板行（若集合为空则整行删除）
3. 重新打包 zip：仅替换 document.xml，其余部件（styles.xml、
   numbering.xml、media/、theme/）原样保留
4. 校验：用 zip 完整性检查 + 可选调用 Office 打开测试
```

**占位符语法**：

| 语法 | 含义 | 示例 |
|---|---|---|
| `{{project.name}}` | 单值 | 填入项目名称 |
| `{{report.week}}` | 单值 | 2026年第45周 |
| `{{#issues}} ... {{/issues}}` | 表格行循环 | 表格中该行按问题列表重复 |
| `{{item.code}}` | 循环内字段 | Q-2026-0001 |
| `{{#if cond}} ... {{/if}}` | 条件段（P2） | 仅当有风险时输出风险章节 |
| `{{date:2026-11-04}}` | 格式化日期 | 2026年11月4日 |

**兼容性策略**：

- 仅操作 `w:p` / `w:t` / `w:tr` / `w:tc` 标准元素
- 不支持：目录域、交叉引用、文本框内占位符、SmartArt（遇到时保持原样）
- 测试矩阵：Office 2016 / Office 2021 / WPS 2019 / WPS 365

**降级路径**：模板复杂到无法处理时，导出 Markdown + 附件图片，用户自行粘贴排版（明确告知，不静默失败）。

### 3.4.4 附件与缩略图

- 原文件按上传字节保存并计算 SHA-256，不以有损压缩覆盖原图。缩略图/预览图是可重建派生物，记录派生参数；用户主动压缩导出产生独立派生文件。
- 目标模型将物理对象 file_blob 与 attachment 逻辑关联分开。相同哈希可以复用物理对象；删除逻辑关联不移动仍被其他记录引用的文件。
- 首轮采用按 ID 读取、应用层关联的受控文件目录；已生成独立缩略图，不做全局物理去重。上传先落临时区，完成校验和原子改名后写元数据；业务关联失败保留暂存态，后续安全回收。
- 不接受用户提供的路径，只接受本实例上传 ID；读取时解析相对路径并保证在数据目录内，拒绝符号链接/路径逃逸。SVG、HTML 等可执行内容不内联渲染。
- 完整产品目标单文件上限 200MB、单次最多 20 文件；首轮仅 PNG/JPEG/GIF，单张20MB、4000万像素上限，长边不超过512的独立PNG缩略图。限制整个请求、解码尺寸和并发，避免图片解压或批量上传耗尽资源。
- 格式预览支持按阶段实测；浏览器不支持的音视频/HEIC 可下载。大文件 Range 与缩略图灯箱不是首轮必然已有的功能。
- 删除使用逻辑回收站，回收期目标 30 天；物理清理要求无活跃引用、无待提交上传引用且保留策略允许。备份中的对象保留不依赖主数据目录清理结果。

### 3.4.5 备份与恢复

**首轮每份备份必须可独立恢复，不使用仅按 mtime 筛选的附件增量。** 当前实现遍历完整 attachments/templates 目录并保存各文件的路径、大小、SHA-256；不按引用做增量，也不自动清理历史包。下列为完整产品演进要求，未实现的清单元数据和容量策略不计为首轮通过。 备份对象包括一致性数据库快照、快照中引用的原始附件、已启用的模板及配置；排除 backups/ 自身、临时区与可重建缓存，避免递归打包。

备份流程：

1. 进入受控备份窗口，阻止数据库/文件引用变更与物理回收；使用 SQLite 一致性快照能力生成 snapshot.db。不得仅在 WAL 运行时复制主数据库文件。
2. 从快照枚举全部引用文件，核对存在性，按稳定文件对象生成完整清单；复制期间保持相关对象不可变。包含模板的版本和哈希。
3. manifest.json 至少包含备份格式版本、应用版本、数据库 schema 版本、UTC 创建时间、数据集 ID、实体数量、每个文件的相对路径/大小/SHA-256、数据库哈希及审计链头。清单与文件必须一一对应。
4. 写入临时 zip，完成校验后原子改名，只有成功文件进入列表。失败不覆盖既有备份。每日首次启动和手动备份共用流程。
5. 目标保留策略为可配置日备 30 份/周备 12 份；同一包可承担两类保留标记，不重复复制。50GB 全量数据与大量全备需要单独容量预算；首轮若只提供日备，周保留标记列为后续。
6. 每次预估空间与容量，容量不足明确失败；只有新备份已校验成功后才可滚动清理，不自动删除最后一个有效恢复点。备份存同一磁盘不能覆盖磁盘损坏风险，支持复制到用户允许的另一介质。

**首轮恢复使用停服 CLI，网页负责生成/下载备份，不在运行服务内替换数据库。**

```
停止服务并取得同一数据目录独占锁
→ 检查备份格式/schema兼容性与解压上限，拒绝绝对路径、..、符号链接、重复路径和异常压缩比
→ 解压到目标旁临时目录，校验清单全部文件与哈希
→ PRAGMA integrity_check + foreign_key_check + 引用文件核对 + 审计链校验
→ 为当前数据生成完整回滚点（失败则不替换）
→ 原子交换数据目录，失败时回滚，不覆盖程序或既有备份目录
→ 记录恢复来源、原/新数据集与链头，再启动新数据并验证
```

目标备份恢复点为最近一次成功备份；“每日备份”不等于零数据损失，恢复耗时须按数据量测量。数据库损坏时保留原文件和诊断信息；不得自动以 .recover 结果覆盖原库或把部分抢救结果称为完整恢复。

### 3.4.6 单实例、端口与开机自启

| 项 | 方案 |
|---|---|
| 单实例 | Linux 文件锁、Windows LockFileEx；锁持有期间唯一实例；二次启动通过受控运行信息定位端口 |
| 端口 | 默认 18600，冲突则递增至 18699；实际端口写 `runtime.json` |
| 僵尸锁 | 系统文件锁是唯一权威；PID 可能复用，不得据 PID 删除活跃锁；runtime.json 校验数据目录与服务身份 |
| 开浏览器 | Windows `rundll32 url.dll,FileProtocolHandler`；macOS `open`；Linux `xdg-open` |
| 开机自启 | 可选写入 `HKCU\...\Run`，默认关闭；设置页开关 |
| 退出 | 浏览器标签关闭不代表服务退出；首轮使用终端 Ctrl+C 正常停服，后续退出按钮/托盘需受保护的显式操作 |

### 3.4.7 离线与手机访问

- 桌面端：本机 HTTP，天然离线。
- 手机/局域网属于后续增强，首轮强制 loopback。启用远程访问前另行设计 HTTPS、身份/会话、撤销、最小接口集合与暴力尝试控制，不把短码置于 URL；访问码不能代替传输加密。
- 本地接口同样需要 Host/Origin 校验及写请求专用头，防止其他网页向本机接口写入；不以“不启用 CORS”推断已具备 CSRF 防护。
- 弱网：前端对上传做队列与重试（最多 3 次，指数退避）。

## 3.5 关键流程时序

### 3.5.1 速记

```
用户           前端                后端                  DB
 │ Alt+N        │                   │                    │
 │─────────────▶│ 打开浮层          │                    │
 │ 输入+粘贴图  │                   │                    │
 │ 保存         │ 显示提交中     │                    │
 │─────────────▶│──────────────────▶│ 校验/解析语法       │
 │              │                   │ 事务开始 ──────────▶│
 │              │                   │ 写 record          │
 │              │                   │ 写 attachment      │
 │              │                   │ 写 audit_log       │
 │              │                   │ 写 search_doc      │
 │              │                   │ 事务提交 ──────────▶│
 │              │◀──────────────────│ 201 {id, code}     │
 │              │ 确认已保存并更新列表        │                    │
 │              │ 失败保留草稿 + 可重试错误 │                    │
```

### 3.5.2 周报生成

```
选择周次 → 后端按周聚合：
  ├─ 本周完成任务（status 变更记录）
  ├─ 进行中任务及进度
  ├─ 新增问题 / 关闭问题
  ├─ 本周记录摘要（按类型分组，取标题）
  ├─ 工时合计（time_entry；未提供时明确不生成工时结论）
  └─ 下周计划（未完成任务按 plan_end 排序）
→ 生成 Markdown 草稿 → 用户编辑 → 保存 worklog(period_type=week)
→ 导出：取模板 → 字段映射 → docx 渲染 → 下载
```

### 3.5.3 导出

```
POST /api/exports {template_id, range, project_ids}
  → 创建持久化导出任务（单并发，重启后将未完成任务标记中断，可重试）
  → 固定源 revision/一致性数据快照后分页取数，避免导出期间业务变动造成混合版本
  → 映射（模板占位符 → 数据路径）
  → 渲染（docx/xlsx）
  → 写 exports/<uuid>.docx
  → 写导出元数据（模板版本/哈希、源范围/版本、产物哈希）+ audit_log(export)
  → 返回 {task_id}
GET /api/exports/{id}         → 进度
GET /api/exports/{id}/download → 文件流
```

## 3.6 错误处理与日志

**统一响应**：

```json
{
  "code": "ISSUE_CLOSE_REQUIRES_ROOT_CAUSE",
  "message": "关闭问题前必须填写原因分析",
  "detail": {"field": "root_cause"},
  "request_id": "01HX..."
}
```

| 类别 | HTTP | 处理 |
|---|---|---|
| 参数错误 | 400 | 前端定位到字段 |
| 未找到 | 404 | 提示并返回列表 |
| 业务规则冲突 | 409 | 说明原因与补救操作 |
| 服务内部错误 | 500 | 记录堆栈到日志，前端给 request_id |
| 文件过大 | 413 | 提示压缩后重试 |

**日志**：`log/slog` 输出 JSON 到 `logs/app-YYYYMMDD.log`，按日轮转保留 14 天；记录启动参数、迁移版本、慢查询（>500ms）、导出耗时、异常堆栈。诊断包只含日志与环境信息，不含业务数据。

---

# 第 4 章 数据设计

## 4.1 实体关系

```
                          ┌──────────┐
                          │ project  │
                          └────┬─────┘
        ┌──────────┬───────────┼───────────┬──────────┬──────────┐
        ▼          ▼           ▼           ▼          ▼          ▼
   ┌────────┐ ┌────────┐ ┌─────────┐ ┌────────┐ ┌────────┐ ┌──────────┐
   │ task   │ │milestone│ │ issue   │ │ record │ │worklog │ │ meeting  │
   └───┬────┘ └────────┘ └────┬────┘ └───┬────┘ └───┬────┘ └────┬─────┘
       │ 自关联(WBS)          │           │          │            │
       │                     │           │          ▼            │
       │                     │           │     ┌──────────┐      │
       │                     │           │     │worklog_  │      │
       │                     │           │     │task      │      │
       │                     │           │     └──────────┘      │
       ▼                     ▼           ▼                       ▼
   ┌──────────────────────────────────────────────────────────────────┐
   │ attachment（多态：owner_type + owner_id）                          │
   └──────────────────────────────────────────────────────────────────┘

   project ──▶ risk / review / config_item ──▶ change_request
   任意实体 ──▶ entity_tag ──▶ tag
   任意实体 ──▶ audit_log（哈希链）
```

## 4.2 基础目标 SQL 与演进模型

> 本节为完整需求的目标 SQL，首轮实际表与字段以代码迁移和 API-v0.1.md 为准，不可直接用该目标 SQL 覆盖运行库。瞬时时间统一 UTC RFC3339（Z、固定精度）；业务纯日期使用 YYYY-MM-DD。按配置 IANA 时区计算本地日/周边界，转换成 UTC 半开区间查询，不用 date(timestamp) 猜本地日期。业务主键 ULID 为目标约定，首轮以实际 API 返回的不透明 ID 为准；调用方不得依赖 ID 推断业务时间。布尔与计数使用 INTEGER。
>
> 所有 SQLite 连接启用 foreign_keys、busy_timeout、WAL 与 synchronous=FULL。mutable 表带整数 version（配置项已有业务version时用row_version），写操作附当前并发版本 并用条件更新；更新行数为零返回 VERSION_CONFLICT。下方仅展示部分 CHECK，所有入口还必须统一校验状态、日期、归档和跨项目引用。

```sql
-- ============ 0. 迁移版本 ============
CREATE TABLE schema_version (
  version     INTEGER PRIMARY KEY,
  applied_at  TEXT NOT NULL,
  description TEXT
);

-- ============ 1. 项目 ============
CREATE TABLE project (
  id          TEXT PRIMARY KEY,
  code        TEXT NOT NULL UNIQUE,          -- P-2026-001
  name        TEXT NOT NULL,
  short_name  TEXT,                          -- 简称，用于导出文件名
  customer    TEXT,                          -- 甲方/总体单位
  phase       TEXT,                          -- dict: phase
  manager     TEXT,                          -- 项目负责人
  start_date  TEXT,                          -- YYYY-MM-DD
  end_date    TEXT,
  status      TEXT NOT NULL DEFAULT 'active',-- active/paused/closed/archived
  description TEXT,                          -- Markdown
  doc_no      TEXT,                          -- 体系文件编号（可选）
  sort_no     INTEGER DEFAULT 0,
  version     INTEGER NOT NULL DEFAULT 1,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  deleted_at  TEXT
);
CREATE INDEX idx_project_status ON project(status);

-- ============ 2. 里程碑 ============
CREATE TABLE milestone (
  id           TEXT PRIMARY KEY,
  project_id   TEXT NOT NULL REFERENCES project(id),
  name         TEXT NOT NULL,
  plan_date    TEXT,
  actual_date  TEXT,
  criteria     TEXT,                          -- 达成准则
  status       TEXT DEFAULT 'planned',        -- planned/reached/missed
  review_id    TEXT REFERENCES review(id),
  remark       TEXT,
  sort_no      INTEGER DEFAULT 0,
  version     INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE INDEX idx_milestone_project ON milestone(project_id, plan_date);

-- ============ 3. 任务（WBS） ============
CREATE TABLE task (
  id             TEXT PRIMARY KEY,
  project_id     TEXT NOT NULL REFERENCES project(id),
  parent_id      TEXT REFERENCES task(id),
  wbs_code       TEXT,                        -- 1.2.3
  name           TEXT NOT NULL,
  type           TEXT DEFAULT 'task',         -- dict: task_type
  assignee       TEXT,
  plan_start     TEXT,
  plan_end       TEXT,
  actual_start   TEXT,
  actual_end     TEXT,
  progress       INTEGER NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100),
  status         TEXT NOT NULL DEFAULT 'todo' CHECK(status IN ('todo','doing','done','canceled')),
  priority       INTEGER DEFAULT 2 CHECK(priority BETWEEN 1 AND 3), -- 1高 2中 3低
  estimate_hours REAL CHECK(estimate_hours IS NULL OR estimate_hours >= 0),
  actual_hours   REAL,                        -- 兼容缓存；权威值来自 time_entry 聚合
  deliverable    TEXT,
  description    TEXT,
  sort_no        INTEGER DEFAULT 0,
  version        INTEGER NOT NULL DEFAULT 1,
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  deleted_at     TEXT,
  UNIQUE(project_id, id),
  FOREIGN KEY(project_id, parent_id) REFERENCES task(project_id, id),
  CHECK(parent_id IS NULL OR parent_id <> id)
);
CREATE INDEX idx_task_project  ON task(project_id, status);
CREATE INDEX idx_task_parent   ON task(parent_id, sort_no);
CREATE INDEX idx_task_plan_end ON task(plan_end);

-- ============ 4. 问题 ============
CREATE TABLE issue (
  id              TEXT PRIMARY KEY,
  code            TEXT NOT NULL,              -- Q-2026-0001
  project_id      TEXT NOT NULL REFERENCES project(id),
  title           TEXT NOT NULL,
  description     TEXT,
  source          TEXT,                       -- dict: issue_source
  severity        TEXT,                       -- dict: issue_severity
  category        TEXT,                       -- dict: issue_category
  status          TEXT NOT NULL DEFAULT 'open',-- open/analyzing/fixing/verifying/closed/rejected
  owner           TEXT,
  found_at        TEXT,
  due_at          TEXT,
  closed_at       TEXT,
  root_cause      TEXT,                       -- 原因分析（关闭必填）
  solution        TEXT,                       -- 纠正措施
  verify_result   TEXT,                       -- 验证结论（关闭必填）
  rejection_reason TEXT,                      -- rejected 必填
  version         INTEGER NOT NULL DEFAULT 1,
  related_task_id TEXT REFERENCES task(id),
  related_review_id TEXT REFERENCES review(id),
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL,
  deleted_at      TEXT
);
CREATE UNIQUE INDEX idx_issue_code ON issue(code);
CREATE INDEX idx_issue_project ON issue(project_id, status, severity);
CREATE INDEX idx_issue_due     ON issue(due_at);

-- 问题状态流转历史（用于统计平均关闭周期、生成时间线）
CREATE TABLE issue_history (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  issue_id   TEXT NOT NULL REFERENCES issue(id),
  from_status TEXT,
  to_status   TEXT NOT NULL,
  note        TEXT,
  operator    TEXT,
  at          TEXT NOT NULL
);
CREATE INDEX idx_issue_history ON issue_history(issue_id, at);

-- ============ 5. 记录（速记/巡检/异常/照片/决策） ============
CREATE TABLE record (
  id           TEXT PRIMARY KEY,
  code         TEXT,                          -- R-2026-11-04-001
  project_id   TEXT REFERENCES project(id),   -- 可为空（未归类）
  type         TEXT NOT NULL DEFAULT 'note',  -- dict: record_type
  title        TEXT,
  content      TEXT,                          -- Markdown
  location     TEXT,
  gps          TEXT,                          -- "lat,lng" 可选
  occurred_at  TEXT NOT NULL,                 -- 发生时间
  participants TEXT,                          -- JSON 数组字符串
  source       TEXT DEFAULT 'manual',         -- manual/voice/import/mobile
  classified   INTEGER NOT NULL DEFAULT 0,    -- 与 project_id 推导一致，不接受客户端独立修改
  version      INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  deleted_at   TEXT
);
CREATE INDEX idx_record_time    ON record(occurred_at DESC);
CREATE INDEX idx_record_project ON record(project_id, occurred_at DESC);
CREATE INDEX idx_record_type    ON record(type);

-- ============ 6. 会议纪要 ============
CREATE TABLE meeting (
  id           TEXT PRIMARY KEY,
  project_id   TEXT REFERENCES project(id),
  title        TEXT NOT NULL,
  held_at      TEXT NOT NULL,
  place        TEXT,
  host         TEXT,
  participants TEXT,                          -- JSON 数组
  agenda       TEXT,                          -- Markdown
  conclusion   TEXT,                          -- Markdown
  actions      TEXT,                          -- JSON: [{text, owner, due, task_id}]
  status       TEXT DEFAULT 'draft',          -- draft/pending/approved/archived
  version      INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  deleted_at   TEXT
);
CREATE INDEX idx_meeting_project ON meeting(project_id, held_at DESC);

-- ============ 7. 工作日志 ============
CREATE TABLE worklog (
  id             TEXT PRIMARY KEY,
  project_id     TEXT REFERENCES project(id),
  period_type    TEXT NOT NULL DEFAULT 'day', -- day/week/month
  log_date       TEXT NOT NULL,               -- 2026-11-04（周报填周一日期）
  period_label   TEXT,                        -- 2026-W45 / 2026-11
  content        TEXT NOT NULL,               -- Markdown
  hours          REAL,                       -- 展示缓存，不作为工时权威数据源
  scope_key      TEXT NOT NULL,              -- global 或 project:<id>
  version        INTEGER NOT NULL DEFAULT 1,
  status         TEXT DEFAULT 'draft',        -- draft/submitted/archived
  auto_generated INTEGER DEFAULT 0,
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  UNIQUE(scope_key, period_type, log_date)
);
CREATE INDEX idx_worklog_date ON worklog(log_date DESC);

-- ============ 8. 日志-任务关联 ============
CREATE TABLE worklog_task (
  worklog_id TEXT NOT NULL REFERENCES worklog(id),
  task_id    TEXT NOT NULL REFERENCES task(id),
  hours      REAL,                           -- 从 time_entry 汇总的展示快照，禁止二次累计
  PRIMARY KEY (worklog_id, task_id)
);

-- ============ 9. 风险 ============
CREATE TABLE risk (
  id          TEXT PRIMARY KEY,
  code        TEXT,                           -- RK-2026-001
  project_id  TEXT NOT NULL REFERENCES project(id),
  title       TEXT NOT NULL,
  description TEXT,
  category    TEXT,                           -- dict: risk_category
  probability INTEGER DEFAULT 2,              -- 1低 2中 3高
  impact      INTEGER DEFAULT 2,              -- 1低 2中 3高
  level       TEXT,                           -- 高/中/低（由矩阵计算）
  strategy    TEXT,                           -- 规避/减轻/转移/接受
  measure     TEXT,                           -- 缓解措施
  trigger     TEXT,                           -- 触发条件
  contingency TEXT,                           -- 应急措施
  owner       TEXT,
  status      TEXT DEFAULT 'open',            -- open/monitoring/closed/occurred
  due_at      TEXT,
  version     INTEGER NOT NULL DEFAULT 1,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  deleted_at  TEXT
);
CREATE INDEX idx_risk_project ON risk(project_id, level, status);

-- ============ 10. 评审 ============
CREATE TABLE review (
  id          TEXT PRIMARY KEY,
  code        TEXT,                           -- PR-2026-001
  project_id  TEXT NOT NULL REFERENCES project(id),
  type        TEXT,                           -- dict: review_type
  object      TEXT NOT NULL,                  -- 被评审对象
  held_at     TEXT,
  place       TEXT,
  reviewers   TEXT,                           -- JSON 数组
  conclusion  TEXT,                           -- 通过/有条件通过/不通过
  summary     TEXT,                           -- 评审意见
  status      TEXT DEFAULT 'draft',           -- draft/pending/approved/archived
  revision_label TEXT DEFAULT 'V1.0', -- 业务显示版本
  doc_no      TEXT,
  version     INTEGER NOT NULL DEFAULT 1,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  deleted_at  TEXT
);
CREATE INDEX idx_review_project ON review(project_id, held_at DESC);

-- ============ 11. 配置项 ============
CREATE TABLE config_item (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL REFERENCES project(id),
  identifier  TEXT NOT NULL,                  -- CI 标识，项目内唯一
  name        TEXT NOT NULL,
  type        TEXT,                           -- 文档/代码/数据/工具
  version     TEXT,
  baseline    TEXT,                           -- 兼容展示字段；权威关系在 baseline_item
  row_version INTEGER NOT NULL DEFAULT 1, -- 配置项version为业务版本，row_version用于并发
  status      TEXT DEFAULT 'draft',           -- draft/approved/released/changed
  location    TEXT,                           -- 存放位置
  owner       TEXT,
  remark      TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  deleted_at  TEXT,
  UNIQUE(project_id, identifier)
);

-- ============ 12. 变更申请 ============
CREATE TABLE change_request (
  id             TEXT PRIMARY KEY,
  code           TEXT,                        -- CR-2026-001
  project_id     TEXT NOT NULL REFERENCES project(id),
  config_item_id TEXT REFERENCES config_item(id),
  title          TEXT NOT NULL,
  reason         TEXT,
  impact         TEXT,                        -- 影响分析
  status         TEXT DEFAULT 'draft',        -- draft/submitted/approved/rejected/closed
  applicant      TEXT,
  approver       TEXT,
  applied_at     TEXT,
  decided_at     TEXT,
  version     INTEGER NOT NULL DEFAULT 1,
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL
);
CREATE INDEX idx_cr_project ON change_request(project_id, status);

-- ============ 12a. 物理文件对象（演进模型） ============
CREATE TABLE file_blob (
  id          TEXT PRIMARY KEY,
  sha256      TEXT NOT NULL UNIQUE,
  stored_path TEXT NOT NULL UNIQUE,
  size        INTEGER NOT NULL CHECK(size >= 0),
  mime        TEXT NOT NULL,
  state       TEXT NOT NULL DEFAULT 'ready', -- staged/ready/quarantined
  created_at  TEXT NOT NULL
);

-- ============ 13. 附件（多态） ============
CREATE TABLE attachment (
  id          TEXT PRIMARY KEY,
  blob_id     TEXT REFERENCES file_blob(id), -- v0.4 拆分物理文件对象；首轮路径按实际迁移
  owner_type  TEXT NOT NULL,                  -- record/issue/task/meeting/review/project...
  owner_id    TEXT NOT NULL,
  filename    TEXT NOT NULL,                  -- 原始文件名
  stored_path TEXT NOT NULL,                  -- attachments/2026/11/<ulid>.jpg
  thumb_path  TEXT,
  mime        TEXT,
  size        INTEGER,
  width       INTEGER,
  height      INTEGER,
  sha256      TEXT NOT NULL,
  kind        TEXT DEFAULT 'file',            -- image/video/audio/file
  deleted_at  TEXT,                           -- 逻辑关联的回收状态
  created_at  TEXT NOT NULL
);
CREATE INDEX idx_att_owner ON attachment(owner_type, owner_id);
CREATE INDEX idx_att_sha   ON attachment(sha256);

-- ============ 14. 标签 ============
CREATE TABLE tag (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  color      TEXT,
  usage_count INTEGER DEFAULT 0,
  version     INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL
);

CREATE TABLE entity_tag (
  entity_type TEXT NOT NULL,
  entity_id   TEXT NOT NULL,
  tag_id      TEXT NOT NULL REFERENCES tag(id),
  PRIMARY KEY (entity_type, entity_id, tag_id)
);
CREATE INDEX idx_etag_tag ON entity_tag(tag_id);

-- ============ 15. 提醒 ============
CREATE TABLE reminder (
  id           TEXT PRIMARY KEY,
  title        TEXT NOT NULL,
  note         TEXT,
  due_at       TEXT NOT NULL,
  repeat_rule  TEXT,                          -- none/daily/weekly/monthly
  related_type TEXT,                          -- task/issue/project/record
  related_id   TEXT,
  done         INTEGER DEFAULT 0,
  done_at      TEXT,
  version     INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL
);
CREATE INDEX idx_reminder_due ON reminder(done, due_at);

-- ============ 16. 审计（哈希链） ============
CREATE TABLE audit_log (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_type TEXT NOT NULL,
  entity_id   TEXT NOT NULL,
  action      TEXT NOT NULL,                  -- create/update/delete/export/archive/restore/login
  changes     TEXT,                           -- JSON: {field: [old, new]}
  operator    TEXT NOT NULL,
  at          TEXT NOT NULL,
  chain_id    TEXT NOT NULL,
  sequence    INTEGER NOT NULL,
  hash_format INTEGER NOT NULL DEFAULT 1,
  hash_prev   TEXT NOT NULL,
  hash_self   TEXT NOT NULL,
  UNIQUE(chain_id, sequence)
);
CREATE INDEX idx_audit_entity ON audit_log(entity_type, entity_id, at DESC);
CREATE INDEX idx_audit_time   ON audit_log(at DESC);

-- ============ 17. 编号生成 ============
CREATE TABLE doc_seq (
  scope  TEXT NOT NULL,                       -- issue/record/risk/review/change/project
  period TEXT NOT NULL,                       -- 2026 或 2026-11
  last_no INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (scope, period)
);

-- ============ 18. 设置 ============
CREATE TABLE setting (
  key        TEXT PRIMARY KEY,
  value      TEXT,
  version    INTEGER NOT NULL DEFAULT 1,
  updated_at TEXT
);

-- ============ 19. 字典 ============
CREATE TABLE dict_item (
  id         TEXT PRIMARY KEY,
  category   TEXT NOT NULL,                   -- phase/task_type/issue_source/...
  code       TEXT NOT NULL,
  label      TEXT NOT NULL,
  sort_no    INTEGER DEFAULT 0,
  enabled    INTEGER DEFAULT 1,
  is_system  INTEGER DEFAULT 0,               -- 系统内置不可删
  version     INTEGER NOT NULL DEFAULT 1,
  UNIQUE(category, code)
);

-- ============ 20. 导出模板 ============
CREATE TABLE export_template (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  kind        TEXT NOT NULL,                  -- weekly/monthly/issues/review/plan/archive
  file_path   TEXT NOT NULL,                  -- templates/xxx.docx
  mapping     TEXT,                           -- JSON 字段映射覆盖
  is_builtin  INTEGER DEFAULT 0,
  version     INTEGER NOT NULL DEFAULT 1,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

-- ============ 21. 检索 ============
CREATE TABLE search_doc (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_type TEXT NOT NULL,
  entity_id   TEXT NOT NULL,
  project_id  TEXT,
  title       TEXT,
  body        TEXT,
  updated_at  TEXT NOT NULL,
  UNIQUE(entity_type, entity_id)
);
CREATE INDEX idx_sdoc_project ON search_doc(project_id, entity_type);

CREATE VIRTUAL TABLE search_fts USING fts5(
  title, body,
  content='search_doc', content_rowid='id',
  tokenize='trigram'
);

-- 触发器保持 FTS 与主表同步（external content 模式）
CREATE TRIGGER search_doc_ai AFTER INSERT ON search_doc BEGIN
  INSERT INTO search_fts(rowid, title, body) VALUES (new.id, new.title, new.body);
END;
CREATE TRIGGER search_doc_ad AFTER DELETE ON search_doc BEGIN
  INSERT INTO search_fts(search_fts, rowid, title, body) VALUES('delete', old.id, old.title, old.body);
END;
CREATE TRIGGER search_doc_au AFTER UPDATE ON search_doc BEGIN
  INSERT INTO search_fts(search_fts, rowid, title, body) VALUES('delete', old.id, old.title, old.body);
  INSERT INTO search_fts(rowid, title, body) VALUES (new.id, new.title, new.body);
END;
```

## 4.3 数据字典（核心表字段说明）

### 4.2.1 必须补齐的演进实体与一致性规则

以下对象在 v0.3/v0.4 通过独立迁移落地，不在首轮宣称已支持：

| 对象 | 关键字段/约束 | 解决的问题 |
|---|---|---|
| time_entry | id、project_id、task_id、work_date、minutes≥0、note、version；任务归属必须一致 | 工时只在此录入；日报/周报/月报和 task.actual_hours 都是聚合或快照，避免重复累计 |
| document / document_revision | 稳定 document_id、entity_type/entity_id、revision_no、state、内容快照、内容哈希、创建人/时间；同文档版本号唯一 | 项目计划有可持久化载体；修改已批准内容必须派生新 revision，保留历史 |
| document_signature | revision_id、角色、姓名标识、签署日期、声明来源；签名类型/校验结果可选 | 签署绑定具体版本，设置中的姓名只作默认值，后改设置不回写历史 |
| baseline / baseline_item | baseline.id、project_id、name、created_at；baseline_item 引用 config_item_id + revision_id + hash；复合唯一 | 一个基线含多个配置项，一个配置项有多个历史版本；锁定快照而不是锁住全部后续修改 |
| review_finding | id、review_id、描述、严重度、owner、issue_id、resolution；稳定 ID | 可记录发现数，并可幂等转换问题；不以数组下标作为长期引用 |
| meeting_action | id、meeting_id、text、owner、due_at、task_id/issue_id、version | 待办排序不改变引用；转任务/问题在事务中写回稳定关联 |
| entity_relation | 来源/目标类型与 ID、关系类型、created_at；同关系唯一 | 速记→问题→任务→评审双向追溯，不复制同一原始证据 |
| export_job | status、template_revision/hash、source_revisions、result_path/hash、created_at、error | 重启后导出任务可解释；留痕能定位生成时的数据和模板 |

- mutable业务表一律有整数并发version（配置项用row_version以区别业务版本）；调用方不得用 last-write-wins 覆盖他人标签页/自动任务的更新。
- 任务 parent_id 只能指向同项目活跃节点，禁止自身和祖先循环，移动/删除/重算在一个事务中完成。SQL 自身约束只能阻止自引用，任意深度环由领域服务检查。
- 关联 task/review/issue 必须与所属项目一致；归档项目及所有子实体、附件关联、标签和签署都不可修改。
- 软删除由所有列表、统计、搜索统一过滤。级联删除明确包含哪些子节点；已批准证据不得由普通软删除绕开锁定。
- project_id 为空的日志 scope_key=global，非空为 project:<id>；同 scope_key+周期+日期唯一。禁止以 NULL 唯一性表达全局日志。
- classified 从 project_id 是否为空推导；状态字段不能同时表示业务状态和超期视图。

### 4.3.1 project

| 字段 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|
| id | TEXT | 是 | ULID | 主键 |
| code | TEXT | 是 | 自动生成 | `P-{年}-{3位}`，唯一，可手改 |
| name | TEXT | 是 | — | 项目全称 |
| short_name | TEXT | 否 | — | 导出文件名用，如"XX显控" |
| customer | TEXT | 否 | — | 甲方/总体单位 |
| phase | TEXT | 否 | 方案 | 论证/方案/工程研制/设计定型/生产/维护 |
| manager | TEXT | 否 | 当前用户 | 负责人 |
| start_date / end_date | TEXT | 否 | — | `YYYY-MM-DD` |
| status | TEXT | 是 | active | active/paused/closed/archived |
| doc_no | TEXT | 否 | — | 体系文件编号，导出时填入模板 |

### 4.3.2 task

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| wbs_code | TEXT | 否 | 由树结构自动生成，如 `1.2.3`；手工导入时可指定 |
| progress | INTEGER | 是 | 0–100；汇总节点按有效叶子后代加权（权重=叶子计划工时，缺省=1），不以子树层级重复加权 |
| status | TEXT | 是 | todo/doing/done/canceled；overdue 为派生布尔值，不持久化到 status |
| priority | INTEGER | 否 | 1 高 / 2 中 / 3 低 |
| estimate_hours / actual_hours | REAL | 否 | 用于工时对比与燃尽 |
| deliverable | TEXT | 否 | 交付物名称，用于里程碑判定 |

### 4.3.3 issue

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| code | TEXT | 是 | `Q-{年}-{4位}`，全局唯一 |
| severity | TEXT | 否 | 严重/一般/轻微/建议 |
| status | TEXT | 是 | 见 4.5.2 状态机 |
| root_cause | TEXT | 关闭时必填 | 原因分析（CAR 证据） |
| verify_result | TEXT | 关闭时必填 | 验证结论 |
| closed_at | TEXT | 关闭时自动 | 用于平均关闭周期统计 |

### 4.3.4 record

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| type | TEXT | 是 | note 速记 / inspection 巡检 / abnormal 异常 / photo 照片 / decision 决策 / other |
| occurred_at | TEXT | 是 | 发生时间，默认当前时间，可改 |
| classified | INTEGER | 是 | 0 = 未归类（未指定项目），1 = 已归类 |
| participants | TEXT | 否 | JSON 数组，如 `["张工","李工"]` |

### 4.3.5 worklog

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| period_type | TEXT | 是 | day/week/month |
| log_date | TEXT | 是 | 日=当天；周=周一；月=1 日 |
| content | TEXT | 是 | Markdown，由草稿编辑而来 |
| auto_generated | INTEGER | 否 | 1 表示由系统生成过草稿 |

### 4.3.6 attachment

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| owner_type + owner_id | TEXT | 是 | 多态关联，无外键约束（由应用保证） |
| sha256 | TEXT | 是 | 用于去重与完整性校验 |
| stored_path | TEXT | 是 | 相对数据目录的路径，禁止 `..` |

## 4.4 索引与查询设计

| 场景 | 查询 | 索引 |
|---|---|---|
| 今日待办 | `WHERE project_id=? AND (status='doing' OR (status='todo' AND plan_end<=?))`，过滤软删除 | idx_task_project + idx_task_plan_end |
| 逾期扫描 | `WHERE status IN ('todo','doing') AND plan_end < ?`，过滤软删除 | idx_task_plan_end |
| 记录时间线 | `ORDER BY occurred_at DESC LIMIT 50` | idx_record_time |
| 问题台账 | `WHERE project_id=? AND status=? ORDER BY severity,due_at` | idx_issue_project |
| 全文检索 | `search_fts MATCH ?` | FTS5 内部索引 |
| 审计追溯 | `WHERE entity_type=? AND entity_id=? ORDER BY at` | idx_audit_entity |

**分页策略**：v0.1 使用明确有上限的 limit/offset（默认 100、上限 500），客户端需展示总数与翻页/加载状态。后续对深翻页改用与排序一致的复合游标，例如 occurred_at DESC,id DESC 对应二元边界；不能仅用 after_id 支持任意排序。游标包含过滤条件和排序版本，ID 只作稳定去重键。

**统计口径**（避免各处不一致，集中在 service 层）：

| 指标 | 定义 |
|---|---|
| 任务完成率 | 已完成叶子任务数 / 有效叶子任务数，排除 canceled/软删除；分母为 0 显示暂无数据 |
| 进度偏差 | 相对差=(实际−计划)/计划；计划为 0 显示不适用，并另列百分点差；计划进度由已冻结基线与日历规则计算 |
| 问题平均关闭周期 | avg(closed_at − found_at)，仅统计已关闭 |
| 按期完成率 | 已完成且有计划结束日期的叶子任务中，实际完成日 ≤ 基线计划完成日的占比；缺日期单列，不随事后改期美化历史 |
| 工时偏差比 | time_entry 实际工时 / 有效计划工时；计划为 0/缺失显示不适用，不称生产效率或利用率 |

## 4.5 状态机

### 4.5.1 任务

```
        ┌────────┐  开始   ┌────────┐  完成  ┌──────┐
        │  todo  │────────▶│ doing  │───────▶│ done │
        └───┬────┘         └───┬────┘        └──┬───┘
            │                  │                │
            │ 取消              │ 取消            │ 重开
            ▼                  ▼                ▼
        ┌──────────┐      ┌──────────┐     ┌────────┐
        │ canceled │      │ canceled │     │ doing  │
        └──────────┘      └──────────┘     └────────┘

  系统判定：plan_end < 配置时区的今天 且 status ∈ {todo, doing} → overdue=true（派生展示，不写 status）
```

### 4.5.2 问题

```
  open ──▶ analyzing ──▶ fixing ──▶ verifying ──▶ closed
    │           │            │            │
    │           │            │            └── 验证不通过 ──▶ fixing
    │           │            └── 无法修复 ──▶ rejected（需填原因）
    │           └── 判定非问题 ──▶ rejected
    └── 直接关闭（重复/无效）──▶ rejected
```

规则：

- 从首版进入 closed 必须从 verifying 转入，root_cause 与 verify_result 非空，closed_at 由服务端写入。进入 rejected 必填 rejection_reason；closed/rejected 终态只读，后续纠正用受审计重开/新版本流程，不允许普通 PATCH 绕开。
- 每次流转写 `issue_history` 与 `audit_log`。
- `rejected` 不计入关闭周期统计。

### 4.5.3 文档类记录（评审/纪要/项目计划）

```
draft ──提交──▶ pending ──通过──▶ approved ──归档──▶ archived（只读）
  ▲                │
  └──── 退回 ──────┘
```

- approved 后内容锁定；修改创建新的 document_revision，revision_no 单调递增。数据库行并发 version 与文档业务 revision_no 是不同概念，不使用小数运算生成版本号。
- archived 正文、附件与签署均只读；若允许批注，批注作为独立追加事件，不改归档快照。

### 4.5.4 项目

```
active ⇄ paused ──▶ closed ──▶ archived
```

- closed 后不允许新增任务/问题，可追加收尾记录；active/paused 均可经显式操作关闭，未闭环问题需提示并记录关闭理由。
- `archived` 全只读，仅可导出与查看。

## 4.6 编号生成

```sql
-- 事务内执行，保证唯一
BEGIN;
  INSERT INTO doc_seq(scope, period, last_no) VALUES(?, ?, 1)
    ON CONFLICT(scope, period) DO UPDATE SET last_no = last_no + 1
    RETURNING last_no;
COMMIT;
```

| 对象 | scope | period | 格式 | 示例 |
|---|---|---|---|---|
| 项目 | project | 2026 | `P-{period}-{no:03d}` | P-2026-001 |
| 问题 | issue | 2026 | `Q-{period}-{no:04d}` | Q-2026-0007 |
| 记录 | record | 2026-11 | `R-{period}-{no:03d}` | R-2026-11-001 |
| 风险 | risk | 2026 | `RK-{period}-{no:03d}` | RK-2026-002 |
| 评审 | review | 2026 | `PR-{period}-{no:03d}` | PR-2026-003 |
| 变更 | change | 2026 | `CR-{period}-{no:03d}` | CR-2026-005 |

规则可配置（前缀、位数、是否含年月），配置存 `setting.key = 'numbering.rule'`。

## 4.7 审计与哈希链

业务写入、状态历史、搜索同步、审计追加在同一事务，失败整体回滚。所有 API 写入口和后台任务共用领域规则，不能从通用 PATCH 绕开归档、关闭校验或签署锁。

```
event = {chain_id, sequence, entity_type, entity_id, action,
         canonical_changes, operator_label, at_utc, hash_prev}
hash_self = SHA256(canonical_json(event))
```

哈希算法和 canonical_json 有显式格式版本，稳定处理 key 排序、UTF-8、null 和数字；对完整结构编码，避免无分隔字段拼接歧义。创世前链哈希为 64 个零，sequence 单调递增，时间统一 UTC。

- GET /api/audit/verify 返回 {ok,total,broken_at}；从头重算校验，并核对最后链头。删除中间记录、普通字段篡改应能定位。
- 本机管理员可改系统时钟、数据库和程序，也可重算整条链；配置中的姓名只是操作人标识。无可信身份、外部时间戳或独立签名锚点时，不能宣称“不可伪造”或“证明事情一定发生”。
- approved/archived revision 锁定通过正常应用入口强制执行；它保护正常流程，不代替文件系统访问控制。
- 备份保存链头；恢复属于数据集回到旧快照，需要记录恢复来源和新旧链头。先写入将被替换的数据库的 restore 事件会丢失，因此离线恢复日志保留在恢复边界外，成功后再追加新的恢复事件。
- 日志不记录正文和原始请求；业务审计 changes 可能含业务数据，属于受管理数据，不能混入无业务数据的诊断包。

## 4.8 字典与初始化数据

| 字典 category | 取值 |
|---|---|
| phase | 论证、方案、工程研制、设计定型、生产、维护 |
| task_type | 任务、活动、评审、交付物、会议、其他 |
| issue_source | 现场、评审、测试、客户、内部、巡检、其他 |
| issue_severity | 严重、一般、轻微、建议 |
| issue_category | 设计、实现、文档、过程、接口、其他 |
| record_type | note、inspection、abnormal、photo、decision、other |
| review_type | 方案评审、设计评审、代码评审、测试评审、归档评审、其他 |
| risk_category | 技术、进度、资源、质量、外部、其他 |
| doc_status | draft、pending、approved、archived |

以上为**系统内置**（`is_system=1`），用户可增删自定义项；内置项不可删除但可禁用。

首次启动时写入：字典、默认设置（端口、备份策略、快捷键）、示例项目（可选，可跳过）。

---

# 第 5 章 功能详细设计

## 5.1 信息架构与导航

```
┌──────────────────────────────────────────────────────────────────────────┐
│  ProjNote        [🔍 搜索 Ctrl+K            ]   [＋速记 Alt+N]  [🔔] [张] │
├────────────┬─────────────────────────────────────────────────────────────┤
│ ▸ 今日     │                                                             │
│ ▸ 项目     │                                                             │
│ ▸ 任务     │                       内容区                                │
│ ▸ 记录     │                                                             │
│ ▸ 日志     │                                                             │
│ ▸ 问题     │                                                             │
│ ▸ 风险     │                                                             │
│ ▸ 评审     │                                                             │
│ ▸ 配置     │                                                             │
│ ─────────  │                                                             │
│ ▸ 导出     │                                                             │
│ ▸ 设置     │                                                             │
└────────────┴─────────────────────────────────────────────────────────────┘
```

- 左侧固定导航（可折叠），选中项高亮。
- 顶部常驻：全局搜索框、速记按钮、提醒铃（未读角标）、当前用户。
- 内容区最大宽度 1440px，宽屏留白；小于 1024px 折叠侧栏；小于768px底部Tab（P3手机增强）。

## 5.2 页面原型

### 5.2.1 今日工作台（默认首页）

```
┌────────────────────────────────────────────────────────────────────────────┐
│ 2026年11月4日 星期二 · 第45周                     [生成今日日志]  [＋速记]   │
├──────────────────────────────────┬─────────────────────────────────────────┤
│ 今日待办（3）              逾期（2）│ 今日记录（8）                            │
│ ┌──────────────────────────────┐ │ ┌─────────────────────────────────────┐ │
│ │ ○ 完成接口联调      XX项目    │ │ │ 09:12 会议  3号机柜接线评审          │ │
│ │   今天 18:00       高         │ │ │        XX显控 · 张工 李工            │ │
│ ├──────────────────────────────┤ │ ├─────────────────────────────────────┤ │
│ │ ○ 提交周报          今天      │ │ │ 10:40 异常  电源模块温升超标  [图][图]│ │
│ │ ● 评审材料准备      逾期2天   │ │ │        #现场 #质量                    │ │
│ └──────────────────────────────┘ │ ├─────────────────────────────────────┤ │
│                                  │ │ 14:20 速记  与甲方确认交付节点        │ │
│ 本周概览                          │ │        （未归类）                     │ │
│ ┌────┬────┬────┬────┐            │ └─────────────────────────────────────┘ │
│ │完成│新增│关闭│工时│            │                                         │
│ │ 12 │  5 │  3 │38h │            │  [查看全部记录 →]                        │
│ └────┴────┴────┴────┘            │                                         │
├──────────────────────────────────┴─────────────────────────────────────────┤
│ 💡 有 2 条速记超过 7 天未归类，[去归类]                                       │
└────────────────────────────────────────────────────────────────────────────┘
```

交互：

- 待办勾选即改状态（乐观更新），写审计。
- 记录卡片点击展开详情抽屉；图片点击大图。
- "生成今日日志"：已有当日日志则询问"覆盖/追加/取消"。

### 5.2.2 项目详情

```
┌────────────────────────────────────────────────────────────────────────────┐
│ ← 项目   XX显控软件研制      P-2026-001  [工程研制]  ● active    [编辑]      │
├────────────────────────────────────────────────────────────────────────────┤
│ 概览 │ WBS │ 甘特 │ 问题 │ 记录 │ 日志 │ 风险 │ 评审 │ 配置项 │ 文档         │
├────────────────────────────────────────────────────────────────────────────┤
│ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐                        │
│ │ 进度 62% │ │ 任务 48  │ │ 问题 12  │ │ 工时 320h│                        │
│ │ ▓▓▓▓▓▓░░ │ │ 完成 30  │ │ 未闭 4   │ │ 计划 400h│                        │
│ └──────────┘ └──────────┘ └──────────┘ └──────────┘                        │
│                                                                            │
│ 里程碑                                                                      │
│ ●───────●───────○──────────○                                               │
│ 方案评审  设计评审  联调完成    验收        （○ 未达成，● 已达成）             │
│ 3/15     6/20     10/30      12/20                                         │
│                                                                            │
│ 近期动态                                                                    │
│ · 张工 完成「接口联调」 10分钟前                                             │
│ · 新增问题 Q-2026-0012 电源模块温升超标 2小时前                              │
└────────────────────────────────────────────────────────────────────────────┘
```

### 5.2.3 任务 / WBS

```
┌────────────────────────────────────────────────────────────────────────────┐
│ 任务    [列表|看板|甘特|日历]    筛选: 负责人▾ 状态▾ 关键字        [＋任务]  │
├──────┬──────────────────────────┬──────┬────────┬────────┬──────┬─────────┤
│ 编码 │ 名称                     │负责人│ 计划   │ 进度   │状态  │ 工时    │
├──────┼──────────────────────────┼──────┼────────┼────────┼──────┼─────────┤
│ 1    │ 需求与方案               │ 张工 │        │  70%   │进行中│ 80/120  │
│ 1.1  │ ├ 需求确认               │ 张工 │10-01~10│ 100%   │已完成│ 40/40   │
│ 1.2  │ ├ 方案设计               │ 李工 │10-15~11│  60%   │进行中│ 40/80   │
│ 2    │ 软件实现                 │ 王工 │        │  45%   │进行中│ 200/260 │
│ 2.1  │ ├ 接口开发               │ 王工 │11-01~11│  30%   │进行中│ 60/100  │
└──────┴──────────────────────────┴──────┴────────┴────────┴──────┴─────────┘
  拖拽行可调整层级与顺序；双击行内编辑；右侧详情抽屉编辑全字段。
```

### 5.2.4 速记浮层

```
状态1 初始：
┌────────────────────────────────────────────────┐
│ 速记                                      [×]  │
│ ┌────────────────────────────────────────────┐ │
│ │ 记点什么…  #标签  @项目  !明天              │ │
│ └────────────────────────────────────────────┘ │
│ [📷 截图] [🎤 录音] [📎 文件]        Enter 保存 │
└────────────────────────────────────────────────┘

状态2 输入中（语法高亮 + 补全）：
┌────────────────────────────────────────────────┐
│ 速记                                      [×]  │
│ ┌────────────────────────────────────────────┐ │
│ │ 3号机柜接线松动 #现场 @XX显控 !明天         │ │
│ │                          ┌──────────────┐  │ │
│ │                          │ #现场        │  │ │
│ │                          │ #现场问题    │  │ │
│ │                          │ #现场安全    │  │ │
│ │                          └──────────────┘  │ │
│ └────────────────────────────────────────────┘ │
│ [📷] [🎤] [📎]                    Enter 保存   │
└────────────────────────────────────────────────┘

状态3 已粘贴图片：
┌────────────────────────────────────────────────┐
│ 速记                                      [×]  │
│ ┌────────────────────────────────────────────┐ │
│ │ 3号机柜接线松动                             │ │
│ │ ┌──────┐                                   │ │
│ │ │ 缩略 │ 截图1.webp 312KB        [×]        │ │
│ │ └──────┘                                   │ │
│ └────────────────────────────────────────────┘ │
│ 项目：XX显控 ▾   类型：速记 ▾    时间：现在 ▾    │
└────────────────────────────────────────────────┘
```

### 5.2.5 记录时间线

```
┌────────────────────────────────────────────────────────────────────────────┐
│ 记录    类型▾  项目▾  标签▾  时间范围▾            [时间线|相册]     [＋]    │
├────────────────────────────────────────────────────────────────────────────┤
│ 2026-11-04 星期二                                                          │
│  ├ 09:12  ┃会议   3号机柜接线评审                                          │
│  │        ┃XX显控 · 张工、李工 · 3号机房            [图] [图]              │
│  ├ 10:40  ┃异常   电源模块温升超标                                          │
│  │        ┃#现场 #质量 #待处理                                             │
│  └ 14:20  ┃速记   与甲方确认交付节点                                        │
│           ┃（未归类）                                                      │
│ 2026-11-03 星期一                                                          │
│  ├ 16:00  ┃巡检   机柜巡检正常                                             │
└────────────────────────────────────────────────────────────────────────────┘
```

### 5.2.6 问题台账

```
┌────────────────────────────────────────────────────────────────────────────┐
│ 问题   项目▾ 状态▾ 严重度▾ 责任人▾ 时间▾      [台账|看板]      [＋问题]     │
├──────────┬────────────────────────┬──────┬──────┬──────┬────────┬─────────┤
│ 编号     │ 标题                   │ 来源 │严重度│ 状态 │ 责任人 │ 期限    │
├──────────┼────────────────────────┼──────┼──────┼──────┼────────┼─────────┤
│Q-2026-0012│电源模块温升超标        │现场  │ 严重 │处理中│ 王工   │11-08 ⚠ │
│Q-2026-0011│接口文档缺少字段说明    │评审  │ 一般 │待验证│ 李工   │11-05    │
│Q-2026-0009│日志时间戳不一致        │测试  │ 轻微 │已关闭│ 张工   │11-01    │
└──────────┴────────────────────────┴──────┴──────┴──────┴────────┴─────────┘
  点击行 → 右侧抽屉：详情 / 流转时间线 / 关联记录 / 附件 / 原因分析
```

### 5.2.7 周报编辑

```
┌────────────────────────────────────────────────────────────────────────────┐
│ 周报  2026年第45周（11-02 ~ 11-08）  XX显控        [重新汇总] [导出▾] [保存]│
├────────────────────────────────────────────────────────────────────────────┤
│ 一、本周完成                                                                │
│ · 完成接口联调，联调通过率 95%                                              │
│ · 完成方案评审，评审意见 8 条已闭环 6 条                                     │
│                                                                            │
│ 二、进行中                                                                  │
│ · 软件实现 45%（计划 60%）— 偏差原因：硬件到货延迟                           │
│                                                                            │
│ 三、问题                                                                   │
│ · 新增 5 条，关闭 3 条，未闭环 4 条（严重 1 条）                             │
│                                                                            │
│ 四、下周计划                                                                │
│ · 完成剩余接口开发与自测                                                    │
│                                                                            │
│ 五、风险                                                                    │
│ · 硬件到货延迟可能影响联调节点（中）                                         │
└────────────────────────────────────────────────────────────────────────────┘
```

### 5.2.8 导出中心

```
┌────────────────────────────────────────────────────────────────────────────┐
│ 导出                                                                       │
├──────────────────┬─────────────────────────────────────────────────────────┤
│ 模板             │  周报模板（XX显控_周报_V2.docx）                          │
│ ▸ 周报           │  ┌───────────────────────────────────────────────────┐  │
│ ▸ 月报           │  │ 字段映射                                          │  │
│ ▸ 问题台账       │  │ {{project.name}}   ← 项目名称                     │  │
│ ▸ 问题报告单     │  │ {{report.week}}    ← 周次                         │  │
│ ▸ 评审记录       │  │ {{#issues}}        ← 本周问题列表                  │  │
│ ▸ 项目计划       │  │ {{item.code}}      ← 问题编号                     │  │
│ ▸ 配置项清单     │  └───────────────────────────────────────────────────┘  │
│ ▸ 风险登记册     │  范围：[XX显控 ▾]  时间：[2026-W45 ▾]                     │
│                  │  [预览]  [生成 Word]  [生成 Excel]  [打印/PDF]           │
├──────────────────┴─────────────────────────────────────────────────────────┤
│ 最近导出：XX显控_周报_2026W45_V1.0_20261104.docx  10:32  [下载] [打开目录]  │
└────────────────────────────────────────────────────────────────────────────┘
```

### 5.2.9 全局搜索面板

```
┌──────────────────────────────────────────────────────────────┐
│ 🔍 电源温升                                        [Esc 关闭] │
├──────────────────────────────────────────────────────────────┤
│ 问题（2）                                                     │
│  Q-2026-0012  电源模块温升超标          XX显控 · 处理中       │
│  Q-2026-0008  电源纹波偏大              XX显控 · 已关闭       │
│ 记录（1）                                                     │
│  2026-11-04  电源模块温升超标 #现场      XX显控               │
│ 任务（0）                                                     │
├──────────────────────────────────────────────────────────────┤
│ ↑↓ 选择   Enter 打开   Ctrl+Enter 在新标签打开                │
└──────────────────────────────────────────────────────────────┘
```

## 5.3 模块详细规格

### 5.3.1 M01 今日工作台

**目的**：一屏回答"今天做什么、今天记了什么、这周怎么样"。

**组成**：

| 区域 | 内容 | 数据来源 |
|---|---|---|
| 待办 | 今日到期 + 进行中 + 逾期任务 | `task` where status=doing OR (status=todo AND plan_end<=本地今天)，统一过滤软删除/归档显示策略 |
| 记录流 | 当日记录，倒序 | `record` where occurred_at ∈ 本地今天对应的 UTC 半开区间 |
| 本周概览 | 完成数、新增问题、关闭问题、工时 | 聚合查询 |
| 未归类提醒 | 超过 7 天未归类的速记 | `record.classified = 0` |

**日志草稿生成规则**：

```
输入：日期 D
1. 完成任务 = task where actual_end ∈ D 且 status = done
2. 推进中任务 = task where D 有 audit_log 的 progress 变更
3. 当日记录 = record where occurred_at ∈ D 的 UTC 半开区间，按 type 分组取标题
4. 问题变更 = issue where D 有状态流转（issue_history）
5. 工时 = time_entry where work_date = D 的 minutes 合计；无工时模块时明确缺项

输出 Markdown：
  ## 今日工作
  - 完成：<完成任务列表>
  - 推进：<推进中任务及进度>
  ## 现场与沟通
  - <记录标题（按类型）>
  ## 问题
  - 新增：…；关闭：…；推进：…
  ## 明日计划
  - <明日到期且未完成的任务>
```

**规则与异常**：

- 草稿生成与保存分离；已有日志时不自动覆盖或追加。先预览差异，再由用户显式选择替换/追加并携带 version 保存，避免重复生成无限追加相同内容。
- 无数据时草稿仍生成骨架，提示"今日暂无记录"。
- 记录流按 `occurred_at` 排序，未归类项带灰色标签。

### 5.3.2 M02 项目管理

**列表**：卡片模式（阶段色带、进度环、问题数）+ 列表模式（表格）。筛选：状态、阶段、关键字；排序：最近更新、编号、截止日。

**新建/编辑**：

| 字段 | 控件 | 校验 |
|---|---|---|
| 编号 | 文本（预填自动编号） | 唯一、非空、格式 `P-\d{4}-\d{3}` |
| 名称 | 文本 | 非空，≤ 100 字 |
| 简称 | 文本 | ≤ 20 字，用于文件名 |
| 客户 | 文本 | ≤ 50 字 |
| 阶段 | 下拉（字典） | 必选 |
| 起止日期 | 日期区间 | 结束 ≥ 开始 |
| 负责人 | 文本 | — |
| 状态 | 下拉 | — |

**归档**：需二次确认，提示“归档后项目和全部子实体只读”；写审计。允许追加收尾记录的是 closed，不是 archived。归档包可单独生成，不能以生成失败破坏原数据。

**规则**：

- 项目编号年度重置（每年从 001 开始）。
- 删除仅限"无任务、无问题、无记录"的空项目，且为软删除。
- 项目切换：全局顶部可切换"当前项目"上下文，影响新建任务的默认归属。

### 5.3.3 M03 WBS 与任务

**树编码算法**：

```
recalcCodes(projectID):
  roots = tasks where parent_id is null order by sort_no
  dfs(roots, prefix="")
    for i, node in enumerate(siblings, start=1):
        node.wbs_code = prefix == "" ? str(i) : prefix + "." + str(i)
        dfs(children(node), node.wbs_code)
  单事务批量 UPDATE
```

**进度汇总**：

```
computeProgress(task):
  if no children: return task.progress（手填）
  leaves = 有效非 canceled/非软删除叶子后代
  weights = leaves.map(c => c.estimate_hours > 0 ? c.estimate_hours : 1)
  return round(Σ(leaf.progress * weight) / Σ(weight))；空集合按“无有效子任务”处理
```

- 父任务进度只读（显示为汇总值）；改动子任务后递归向上重算。
- 汇总节点不允许独立编辑进度/状态。有效叶子全部 done 时汇总为 done；子任务重开或新增后重新汇总，不能留下父 done/子 doing 的冲突。叶子 done 固定 100%，重开清空 actual_end 并保留历史。原 estimate_hours 缺失值的默认权重规则需在 UI 明示。

**拖拽规则**：

| 操作 | 结果 |
|---|---|
| 拖到目标行上方 | 成为同级，插入该位置之前 |
| 拖到目标行内部（右侧 1/3） | 成为其子任务 |
| 拖到自己子孙下 | 禁止，提示"不能移动到自己的子任务下" |
| 拖出根节点 | 禁止（本项目内） |

**Excel 导入格式**（两种模式）：

| 模式 | 格式 | 说明 |
|---|---|---|
| 缩进模式 | A 列名称，前面空格/制表符表示层级 | 从 Word/Excel 大纲直接粘贴 |
| 编码模式 | A 列编码（1.2.3）、B 列名称、C 列负责人… | 已有 WBS 表 |

导入前预览：显示将创建的行数、层级、冲突项；确认后事务导入。

**任务详情抽屉**：基本信息 / 计划与实际 / 工时 / 附件 / 关联问题 / 变更历史。

### 5.3.4 M04 进度视图

**看板**：列 = 状态（待办 / 进行中 / 已完成 / 已取消），可自定义列顺序；卡片显示名称、负责人、截止日、优先级色条、问题数。拖拽换列即改状态，写 `issue_history` 式的流转记录（任务用 audit_log）。

**甘特**：

- 行 = 任务树（可折叠），列 = 日期轴（日/周/月缩放）。
- 计划条（浅色）+ 实际条（深色）叠加；今日竖线；里程碑菱形。
- 拖拽条形改期：更新 `plan_start/plan_end`，写审计。
- 导出 PNG（前端 canvas 渲染，非截图）。
- 超过 300 任务时提示"建议先筛选"。

**日历**：月视图显示里程碑（◆）、任务截止（●）、记录（·）；点击日期快速新建。

### 5.3.5 M05 速记

**唤起与关闭**：

| 操作 | 行为 |
|---|---|
| `Alt+N` | 打开浮层，焦点在输入框 |
| `Esc` | 有内容则提示"保存并关闭 / 放弃"；无内容直接关闭 |
| `Enter` | 保存并关闭 |
| `Shift+Enter` | 换行 |
| `Ctrl+Enter` | 保存并继续（保留项目与标签） |

**语法解析**（保存时解析，输入时高亮）：

| 语法 | 解析结果 | 失败处理 |
|---|---|---|
| `#标签名` | 匹配或新建标签 | 保留原文，提示"已按文本保存" |
| `@项目名/简称` | 归属项目 | 未匹配则不归属，提示从候选选择 |
| `!今天`/`!明天`/`!周五`/`!2026-11-10` | 生成提醒 | 无法解析则忽略 |
| 纯文本 | 正文 | — |

解析出的语法标记从正文中移除，正文保持干净。

**图片**：

- 支持 `Ctrl+V` 粘贴、拖拽文件、点击选择。
- 单条目标最多9张；原字节上传保留，不做有损覆盖。首轮单张20MB/4000万像素，预览使用独立缩略图；超限提示并保留输入。
- 上传失败：保留本地 blob 预览，标红重试。

**保存流程**：

```
1. 前端校验（正文或附件至少一项）
2. POST /api/records  {content, type, project_id, occurred_at, tags[], files[]}
3. 后端事务：编号 → record → attachments → audit → search_doc
4. 返回 {id, code}
5. 前端：替换乐观项，toast "已记录 R-2026-11-04-001"
```

**未归类**：

- 未指定项目的记录 `classified=0`，进入"未归类池"。
- 归类方式：单条选择项目 / 批量多选归类 / 按关键字规则自动归类（P2）。
- 超过 7 天未归类：首页提示 + 记录流灰色标记。

### 5.3.6 M06 现场记录

**时间线**：按天分组，左侧时间轴，右侧卡片。筛选：类型、项目、标签、时间范围、关键字。视图切换：时间线 / 相册（图片墙）/ 地图（P2）。

**记录类型与必填**：

| 类型 | 建议字段 | 典型用途 |
|---|---|---|
| note 速记 | 正文 | 随手记 |
| inspection 巡检 | 地点、结论、问题项 | 机房/设备巡检 |
| abnormal 异常 | 地点、现象、影响、临时措施 | 现场故障 |
| photo 照片 | 图片、说明 | 以图为主 |
| decision 决策 | 背景、选项、结论、依据 | DAR 证据 |
| other | 正文 | 其他 |

**会议纪要**：

- 独立页面，字段：标题、时间、地点、主持、参会人（多选标签）、议题（Markdown）、结论（Markdown）、待办列表。
- 待办项使用稳定 action_id（内容、负责人、截止、关联 task_id/issue_id）；可一键生成任务/问题并双向关联。转换与回写同事务，重复点击返回已关联对象，不按数组 idx 寻址。
- 纪要状态统一 draft/pending/approved/archived；确认生成具体 revision 的签署信息，批准后只读，导出绑定该版本。

**照片**：

- 九宫格缩略图，点击进入灯箱：左右切换、缩放、下载、删除。
- 灯箱支持键盘 ← → Esc。
- 批量下载（打包 zip）。

### 5.3.7 M07 工作日志

**日报**：见 M01 的草稿生成规则；编辑后保存为 `worklog(period_type=day)`。

**周报**：

```
汇总范围：周一 00:00 ~ 周日 23:59
1. 本周完成：actual_end 落在本周且 done 的任务
2. 进行中：本周有进度变更且未完成的任务，取最新进度与计划进度对比
3. 问题：本周新增 / 关闭 / 未闭环（按严重度排序）
4. 记录摘要：本周记录按类型计数 + 各类型前 3 条标题
5. 工时：仅从 time_entry 汇总，按任务分组；日报/周报引用这些数据，不重复相加
6. 下周计划：plan_start 在下周的任务 + 未完成且 plan_end 已过的任务
7. 风险：本周新增/状态变更的风险
```

- 生成后进入编辑器，可增删改；保存后 `auto_generated=1` 仅表示"曾自动生成"。
- 支持"插入本周记录摘要"按钮，把记录标题批量插入。

**工时**：在 time_entry 按日期/任务登记分钟，显示日合计；日报/周报/月报聚合。超过 8 小时黄色提示，超过 12 小时红色提示（仅提示，不阻断）。补记保留录入时间与业务日期，更新写审计。

**补记**：日期选择器允许选过去 30 天；补记不影响"今日"逻辑。

### 5.3.8 M08 问题台账

**登记**：标题、项目（默认当前）、来源、严重度、分类、责任人、发现日期、期限、描述、附件。编号自动生成。

**列表**：按项目/状态/严重度/责任人/时间筛选；默认排序：未关闭优先 → 严重度高优先 → 期限近优先。

**详情抽屉**：

```
┌────────────────────────────────────────────┐
│ Q-2026-0012  电源模块温升超标      [编辑]   │
│ 状态：处理中   严重度：严重   来源：现场     │
├────────────────────────────────────────────┤
│ 描述 …                                      │
│ 原因分析 …（未填写时高亮提示）               │
│ 纠正措施 …                                  │
│ 验证结论 …（关闭前必填）                     │
├────────────────────────────────────────────┤
│ 流转时间线                                   │
│ 11-04 10:40  新建（张三）                    │
│ 11-04 14:00  分析中（王工）                  │
│ 11-05 09:30  处理中（王工）                  │
├────────────────────────────────────────────┤
│ 关联：任务「电源模块整改」 记录「10:40 异常」 │
│ 附件：[图] [图]                              │
└────────────────────────────────────────────┘
```

**关闭校验**：

```
if requested_status == closed:
    require current_status == verifying
    require trim(root_cause) != "" and trim(verify_result) != ""
    set closed_at = server_now_utc
    version条件更新 + issue_history + audit_log 同事务
if requested_status == rejected:
    require trim(rejection_reason) != ""
closed/rejected 终态拒绝普通编辑
```

**统计**：新增/关闭趋势（周/月）、按来源分布、按严重度分布、平均关闭周期、超期未闭数。

**导出**：台账 xlsx（多 sheet：汇总、明细、统计）；单条问题报告单 docx。

### 5.3.9 M09 风险管理

- 登记字段：标题、描述、类别、可能性（1-3）、影响（1-3）、应对策略、缓解措施、触发条件、应急措施、责任人、期限。
- 等级矩阵：

| 可能性 \ 影响 | 低(1) | 中(2) | 高(3) |
|---|---|---|---|
| 高(3) | 中 | 高 | 高 |
| 中(2) | 低 | 中 | 高 |
| 低(1) | 低 | 低 | 中 |

- 列表按等级排序，高等级红色；看板视图按等级分列。
- 状态：open / monitoring / closed / occurred（已发生则提示转问题）。
- 导出风险登记册 xlsx / docx。

### 5.3.10 M10 评审管理

- 字段：编号、项目、类型、对象、时间、地点、评审人（多选）、结论（通过/有条件通过/不通过）、意见汇总、附件。
- 评审发现：逐条录入（描述、严重度、责任方），可一键转问题（`source=评审`，`related_review_id` 回填）。
- 结论为"不通过"时，要求记录整改计划。
- 导出：评审记录 docx（套模板）、评审问题汇总 xlsx。

### 5.3.11 M11 配置管理（轻量）

- 配置项：标识（唯一）、名称、类型、版本、基线、状态、存放位置、责任人。
- 基线：baseline + baseline_item 固定配置项 revision/hash 集合；建立后基线成员快照只读，后续修订经变更单形成新 revision/新基线，不覆盖旧基线。
- 变更单：编号、关联配置项、变更原因、影响分析、申请人、审批人、状态。
- 导出：配置项清单 xlsx、配置状态报告 docx。

### 5.3.12 M12 全局搜索

- 唤起：`Ctrl+K`；输入 300ms 防抖。
- 结果分组：项目 / 任务 / 问题 / 记录 / 日志 / 会议；每组最多 5 条，回车展开全部。
- 高亮命中词；显示所属项目、状态、时间。
- 过滤语法：`#标签`、`@项目`、`type:issue`、`status:open`、`after:2026-10-01`。
- 空结果：提示"未找到，试试更短的关键词"。

### 5.3.13 M13 导出中心

- 左侧模板列表（按 kind 分组）；右侧配置区：范围、时间、字段映射（高级）。
- 预览：Word 模板显示"占位符 → 实际值"的映射表；Excel 显示前 20 行预览。
- 生成：异步任务，进度条；完成后自动下载并记录历史。
- 历史：最近 20 次导出，可重新下载或打开所在目录。
- 失败：显示原因（模板缺字段 / 数据为空 / 文件被占用），不产生半成品文件。

### 5.3.14 M14 提醒

- 来源：任务截止、问题期限、自定义提醒。
- 首页铃铛显示未来 3 天与逾期项数量。
- 启动时弹出"今日提醒"汇总（可关闭）。
- 桌面通知（P3）：目标Windows通知，可配置开关与免打扰时段，平台能力单独验收。

### 5.3.15 M15 系统与数据

**首次启动向导**（3 步）：

1. 选择数据目录（默认 exe 同级 `./data`，不可写则 `%LOCALAPPDATA%\ProjNote`）
2. 填写姓名（写入审计操作人）
3. 创建示例项目 / 跳过

**设置项**：

| 分组 | 项 |
|---|---|
| 通用 | 姓名、默认项目、语言（暂仅中文）、主题（浅/深） |
| 数据 | 数据目录、打开目录、立即备份、恢复、清理回收站 |
| 导出 | 默认模板、导出目录、文件名规则 |
| 提醒 | 桌面通知开关、免打扰时段 |
| 高级 | 端口、局域网访问、日志级别、诊断包 |

**备份界面**：首轮列表/创建/下载，显示备份完成与失败；恢复通过停止服务后的 CLI。v0.2 提供引导停服的恢复向导及保留策略，不能在 SQLite 连接仍打开时直接替换文件。

### 5.3.16 M16 归档

- 触发：项目状态改为 `archived` 时提示"生成归档包？"
- 内容：按第 6.6 节目录结构生成；附件按实体分目录；生成 `manifest.sha256`。
- 校验：`ProjNote --verify <zip>` 命令行参数，或界面"校验归档包"。
- 归档后项目及子实体只读，可导出、可查看。独立追加批注若启用只记录新事件，不改归档内容。

## 5.4 通用交互规范

### 5.4.1 表单校验

| 规则 | 提示方式 |
|---|---|
| 必填 | 失焦即提示，红色边框 + 下方文字 |
| 长度/格式 | 实时校验，输入时提示 |
| 唯一性 | 失焦时异步校验 |
| 跨字段（结束≥开始） | 提交时校验并定位 |

### 5.4.2 状态反馈

| 场景 | 表现 |
|---|---|
| 加载中 | 骨架屏（列表）/ 按钮 loading（操作） |
| 空状态 | 插画 + 一句说明 + 主操作按钮 |
| 成功 | toast 2 秒自动消失 |
| 失败 | toast + 可展开详情 + request_id |
| 危险操作 | 二次确认，需输入名称或勾选确认 |

### 5.4.3 键盘

全站可用：`Ctrl+K` 搜索、`Alt+N` 速记、`Esc` 关闭、`↑↓` 列表导航、`Enter` 打开、`Ctrl+S` 保存。完整列表见附录 B。

### 5.4.4 可访问性

- 所有交互元素可 Tab 到达，焦点可见。
- 颜色不作为唯一信息载体（如严重度同时用文字/图标）。
- 支持 125%/150% 缩放不破版。

---

# 第 6 章 证据链与 GJB5000B 候选映射

## 6.1 定位与边界

工具是**项目负责人侧的“证据采集与成型”工具**，不是过程管理体系本身。本轮未获得权威标准原文和单位适用检查单，下面的过程域名称仅沿用原需求作候选分类，必须由单位体系负责人核对标准版本、条款、术语、责任和保留期。不能根据本表宣布某成熟度等级覆盖、审核通过或完整符合。

| 层次 | 谁来管 | 工具的角色 |
|---|---|---|
| 组织级过程资产（方针、规程、模板） | 单位体系文件 | 工具**引用**其模板，不生成 |
| 项目级过程执行（计划、监控、评审、配置） | 项目负责人 | 工具**承载**记录，导出证据 |
| 个人工作记录（日志、速记） | 本人 | 工具**采集**，汇聚为证据 |

工具承载用户录入与关联来源，提供查找、版本和导出能力；是否真实发生、记录是否充分及审核是否通过需要组织流程和其他独立证据判断。

## 6.2 过程域覆盖矩阵

> 候选支撑：● 拟提供结构化记录与导出；◐ 部分字段/导出需组织材料补齐；○ 当前未规划。符号不是标准符合性或成熟度覆盖度。

### 6.2.1 原需求中的项目策划/监控类候选分类

| 过程域 | 典型证据 | 工具载体 | 导出物 | 候选支撑 |
|---|---|---|---|---|
| **PP 项目策划** | 项目计划、WBS、进度计划、估算、资源计划 | `project`、`task`、`milestone` | 项目计划 docx（套模板）、WBS 表 xlsx、进度计划 | ● |
| **PMC 项目监控** | 周报/月报、进度跟踪、偏差分析、里程碑评审 | `worklog`、`task` 实际值、`milestone`、`review` | 周报 docx、月报 docx、进度跟踪表 xlsx | ● |
| **REQM 需求管理** | 需求变更记录、需求跟踪矩阵 | `change_request`、`record(type=decision)` | 变更记录表 xlsx；跟踪矩阵 P3 | ◐ |
| **CM 配置管理** | 配置项清单、基线、变更单、配置状态报告 | `config_item`、`change_request` | 配置项清单 xlsx、配置状态报告 docx | ● |
| **PPQA 过程质量保证** | 检查单、不符合项、跟踪闭环 | `issue(source=内部/评审)`、`review` | 不符合项跟踪表 xlsx、检查记录 | ◐ |
| **MA 测量与分析** | 度量数据、趋势分析 | 自动统计 | 度量报表 xlsx（工时/缺陷/进度/按期率） | ● |
| **SAM 供方协议管理** | 供方协议、供方评价 | — | — | ○（单位级管理） |

### 6.2.2 原需求中的风险/评审等候选分类

| 过程域 | 典型证据 | 工具载体 | 导出物 | 候选支撑 |
|---|---|---|---|---|
| **RSKM 风险管理** | 风险登记册、风险应对记录 | `risk` | 风险登记册 xlsx/docx | ● |
| **DAR 决策分析** | 决策记录、备选方案对比 | `record(type=decision)`、`meeting` | 决策记录 docx | ◐ |
| **VER 验证** | 评审记录、验证记录 | `review`、`issue(来源=评审)` | 评审记录 docx | ● |
| **VAL 确认** | 确认记录、用户反馈 | `review`、`record` | 确认记录（评审模板变体） | ◐ |
| **CAR 原因分析** | 原因分析、纠正措施、验证 | `issue.root_cause/solution/verify_result` | 原因分析表 docx | ● |
| IPM / TS / PI | 集成计划、技术方案、集成记录 | 文档类附件 + 记录 | 附件归档 | ○（走文档管理） |
| OPF / OPD / OT | 组织过程资产 | — | — | ○（单位级） |

## 6.3 证据链的三条硬规则

### 规则一：一次录入，多处复用

```
          ┌──────────── 录入（唯一入口） ────────────┐
          │  速记 / 问题 / 任务 / 评审 / 风险 / 会议  │
          └───────────────────┬─────────────────────┘
                              │
        ┌─────────────┬───────┴────────┬─────────────┐
        ▼             ▼                ▼             ▼
   今日工作台      项目台账          周报/月报       归档包
   （当天可见）   （结构化统计）    （自动汇总）    （证据成型）
```

原始事实只录入一次，其他页面以关联视图复用；日志、导出和归档可保存版本化派生快照，并保留来源ID/revision、生成时间与哈希，不回写或冒充原始事实。

### 规则二：服务端记录时间与操作人标识，并明示可信边界

- created_at/updated_at 由服务端生成并使用 UTC；occurred_at/found_at 等业务发生时间允许补记，保留录入时间和修改历史。服务端时间取自本机，不是可信外部时间戳。
- 每条写操作带 operator_label（设置里的姓名快照）并写审计；姓名可配置，不代表该人经过身份认证。
- 审计链哈希，归档时附校验报告。

### 规则三：已批准即锁定

- 评审记录、项目计划、问题关闭后进入只读。
- 修改必须"新建版本"，保留历史版本对比。
- 后补记录必须明确业务发生时间和实际录入时间；锁定与历史只提供追溯，不能单独消除真实性争议。

## 6.4 记录状态与签署

```
draft ──▶ pending ──▶ approved ──▶ archived
  ▲           │
  └── 退回 ───┘
```

| 状态 | 可编辑 | 可导出 | 说明 |
|---|---|---|---|
| draft | ✅ | ✅ | 编制中，导出带"草稿"水印 |
| pending | ❌ | ✅ | 待评审，只可加批注 |
| approved | ❌ | ✅ | 已批准，正式版本 |
| archived | ❌ | ✅ | 已归档，项目结束 |

签署信息绑定 document_revision：编制/审核/批准角色、姓名标识、签署日期、来源与内容哈希。setting 只能提供新签署默认值，不能修改历史签署。首轮姓名填栏是声明记录，不是数字签名，也不代替单位批准流程。

## 6.5 检查前自检清单

工具提供"证据自检"页面，逐项检查并给出跳转：

| # | 检查项 | 判定规则 | 对应过程域 |
|---|---|---|---|
| 1 | 项目计划是否已批准 | 存在 approved 的项目计划记录 | PP |
| 2 | WBS 是否完整 | 任务树非空且无孤立节点 | PP |
| 3 | 里程碑是否都有评审 | 每个 reached 里程碑关联 review | PMC |
| 4 | 周报是否连续 | 项目周期内每周都有 worklog(week) | PMC |
| 5 | 问题闭环与超期情况 | 分别统计全部非终态数及 due_at 超期数，包含 verifying；30 天作为可配置提示，不冒充全部闭环 | PMC/PPQA |
| 6 | 关闭问题是否有原因分析 | `root_cause` 非空 | CAR |
| 7 | 风险是否定期更新 | 高风险每月至少更新一次 | RSKM |
| 8 | 评审记录是否完整 | 评审有结论、有评审人 | VER |
| 9 | 评审发现是否有处置 | 每条 review_finding 有关联 issue 或明确 disposition/reason；不可仅比较计数 | VER/PPQA |
| 10 | 配置项是否有基线 | 每个 released 配置项属于某基线 | CM |
| 11 | 变更单是否闭环 | 无 submitted 超过 15 天 | CM |
| 12 | 审计链是否完整 | 哈希校验通过 | 全部 |

自检结果导出为 证据自检报告.xlsx，分通过/不通过/不适用/待核对，并附规则版本、统计时间和范围；不提供“全绿即符合标准”的结论。

## 6.6 归档包结构

```
{项目简称}_归档_{YYYYMMDD}.zip
├─ 00_归档说明.docx             项目概况、归档范围、生成时间、校验说明
├─ 00_归档说明.md
├─ 01_项目计划/
│   ├─ 项目计划_V1.0.docx
│   └─ WBS清单.xlsx
├─ 02_周报月报/
│   ├─ 2026W45_周报.docx
│   └─ 2026-11_月报.docx
├─ 03_问题台账/
│   ├─ 问题台账.xlsx
│   └─ 问题报告单/Q-2026-0012.docx
├─ 04_评审记录/
│   ├─ PR-2026-003_设计评审.docx
│   └─ 评审问题汇总.xlsx
├─ 05_配置管理/
│   ├─ 配置项清单.xlsx
│   └─ 变更记录.xlsx
├─ 06_风险管理/
│   └─ 风险登记册.xlsx
├─ 07_现场记录/
│   ├─ 2026-11-04.md
│   └─ attachments/2026-11/*.jpg
├─ 08_工作日志/
│   └─ 2026-11.md
├─ 09_审计/
│   ├─ 审计报告.pdf
│   └─ audit.csv
└─ manifest.sha256               逐文件哈希
```

命名规则：`{项目简称}_{文档类型}_{期间}_{版本}_{日期}.docx`。

## 6.7 与现有体系文件的对应

| 工具导出物 | 对应体系文件（示例） | 说明 |
|---|---|---|
| 项目计划 | 项目计划/软件开发计划 | 套用现有模板 |
| 周报 | 项目周报/月报 | 模板可自定义 |
| 问题台账 | 问题报告/不符合项跟踪表 | 字段对齐现有表头 |
| 评审记录 | 评审报告/评审记录表 | 含签署栏 |
| 配置项清单 | 配置项清单/配置状态报告 | 标识规则可配 |
| 风险登记册 | 风险登记册/风险管理表 | 3×3 矩阵 |
| 会议纪要 | 会议记录表 | — |
| 归档说明 | 项目归档清单 | — |

> 具体模板以单位现行体系文件为准；工具通过"导入模板"适配，不内置特定单位的格式。

---

# 第 7 章 导出设计

## 7.1 导出能力矩阵

| 对象 | Word | Excel | PDF/打印 | Markdown | 说明 |
|---|---|---|---|---|---|
| 项目计划 | ✅ | ✅ WBS | ✅ | ✅ | 套模板 |
| 周报/月报 | ✅ | ◐ | ✅ | ✅ | Excel 仅附表 |
| 问题台账 | ✅ 单条 | ✅ 台账 | ✅ | ◐ | — |
| 评审记录 | ✅ | ✅ 汇总 | ✅ | ✅ | 含签署栏 |
| 风险登记册 | ✅ | ✅ | ✅ | ◐ | — |
| 配置项清单 | ◐ | ✅ | ✅ | ◐ | Word 仅汇总 |
| 会议纪要 | ✅ | — | ✅ | ✅ | — |
| 工作日志 | ✅ | ◐ | ✅ | ✅ | — |
| 度量报表 | ◐ | ✅ | ✅ | — | 图表以图片嵌入 |
| 归档包 | — | — | — | — | zip |

## 7.2 Word 模板规范

### 7.2.1 占位符字典

**全局上下文**

| 占位符 | 含义 | 示例值 |
|---|---|---|
| `{{project.code}}` | 项目编号 | P-2026-001 |
| `{{project.name}}` | 项目名称 | XX显控软件研制 |
| `{{project.short_name}}` | 简称 | XX显控 |
| `{{project.customer}}` | 客户 | XX研究所 |
| `{{project.phase}}` | 阶段 | 工程研制 |
| `{{project.manager}}` | 负责人 | 张三 |
| `{{project.start_date}}` | 开始日期 | 2026-01-05 |
| `{{project.end_date}}` | 结束日期 | 2026-12-20 |
| `{{report.title}}` | 报告标题 | 项目周报 |
| `{{report.period}}` | 期间 | 2026年第45周 |
| `{{report.period_start}}` | 起始日 | 2026-11-02 |
| `{{report.period_end}}` | 结束日 | 2026-11-08 |
| `{{report.date}}` | 生成日期 | 2026-11-04 |
| `{{report.author}}` | 编制人 | 张三 |
| `{{report.version}}` | 版本 | V1.0 |
| `{{report.doc_no}}` | 文档编号 | XX-PP-012 |
| `{{report.total_hours}}` | 总工时 | 38.5 |

**集合（表格行循环）**

| 集合 | 行内占位符 | 说明 |
|---|---|---|
| `{{#tasks}}` | `{{item.wbs}}` `{{item.name}}` `{{item.assignee}}` `{{item.plan_start}}` `{{item.plan_end}}` `{{item.progress}}` `{{item.status}}` | 任务清单 |
| `{{#issues}}` | `{{item.code}}` `{{item.title}}` `{{item.source}}` `{{item.severity}}` `{{item.status}}` `{{item.owner}}` `{{item.due_at}}` `{{item.root_cause}}` `{{item.verify_result}}` | 问题清单 |
| `{{#risks}}` | `{{item.code}}` `{{item.title}}` `{{item.level}}` `{{item.strategy}}` `{{item.owner}}` `{{item.status}}` | 风险清单 |
| `{{#records}}` | `{{item.time}}` `{{item.type}}` `{{item.title}}` `{{item.content}}` | 记录摘要 |
| `{{#findings}}` | `{{item.desc}}` `{{item.severity}}` `{{item.owner}}` | 评审发现 |
| `{{#milestones}}` | `{{item.name}}` `{{item.plan_date}}` `{{item.actual_date}}` `{{item.status}}` | 里程碑 |
| `{{#config_items}}` | `{{item.identifier}}` `{{item.name}}` `{{item.version}}` `{{item.baseline}}` `{{item.status}}` | 配置项 |

**条件段（P2）**

```
{{#if issues.length}}
  ……有问题时输出的章节……
{{/if}}
```

### 7.2.2 模板制作约定

1. 占位符必须写在**同一段落**内（跨段不支持）。
2. 表格循环：在表格中单独一行写 `{{#issues}}` 与 `{{item.xxx}}`，该行即模板行。
3. 占位符建议用等宽字体标注，便于辨认（导出后样式随模板）。
4. 签署栏用普通文本 + 占位符，如 `编制：{{report.author}}    日期：{{report.date}}`。
5. 页眉页脚中的占位符同样支持。
6. 图片插入（P2）：`{{image:chart_progress}}` 处替换为生成的图表 PNG。

### 7.2.3 示例：周报模板骨架

```
                    XX显控软件研制项目
                        项目周报

文档编号：{{report.doc_no}}          版本：{{report.version}}
报告期间：{{report.period}}          编制日期：{{report.date}}

一、本周工作完成情况
{{#tasks}}
{{item.wbs}}  {{item.name}}  {{item.assignee}}  {{item.progress}}%
{{/tasks}}

二、问题跟踪
┌──────────┬────────────┬──────┬──────┬────────┐
│ 编号     │ 标题       │严重度│ 状态 │ 责任人 │
├──────────┼────────────┼──────┼──────┼────────┤
│{{#issues}}{{item.code}}│{{item.title}}│{{item.severity}}│{{item.status}}│{{item.owner}}│{{/issues}}
└──────────┴────────────┴──────┴──────┴────────┘

三、下周计划
…

编制：{{report.author}}      审核：________      批准：________
```

## 7.3 Excel 导出规范

### 7.3.1 通用样式

- 表头：加粗、浅灰底、居中、冻结首行。
- 列宽：按内容自适应，上限 60 字符。
- 数据行：隔行浅色；逾期/高严重度用条件格式标红。
- 页脚：`第 &P 页 / 共 &N 页`；打印区域预设 A4 横向。
- 不使用宏、不使用外链、不使用需要联网的函数。

### 7.3.2 各台账 Sheet 定义

**问题台账.xlsx**

| Sheet | 列 |
|---|---|
| 汇总 | 项目、问题总数、未关闭、严重、一般、轻微、平均关闭周期（天） |
| 明细 | 编号、标题、来源、严重度、分类、状态、责任人、发现日期、期限、关闭日期、原因分析、纠正措施、验证结论 |
| 统计 | 按周新增/关闭趋势、按来源分布、按严重度分布 |

**WBS 清单.xlsx**

| Sheet | 列 |
|---|---|
| WBS | 编码、名称、类型、负责人、计划开始、计划结束、实际开始、实际结束、进度、状态、计划工时、实际工时、交付物 |
| 里程碑 | 名称、计划日期、实际日期、状态、关联评审 |

**风险登记册.xlsx**

| Sheet | 列 |
|---|---|
| 登记册 | 编号、标题、类别、可能性、影响、等级、策略、缓解措施、触发条件、应急措施、责任人、状态、期限 |
| 矩阵 | 3×3 热力图（条件格式着色） |

**配置项清单.xlsx**

| Sheet | 列 |
|---|---|
| 配置项 | 标识、名称、类型、版本、基线、状态、存放位置、责任人 |
| 基线 | 基线名称、建立日期、包含配置项数、状态 |
| 变更 | 编号、配置项、原因、影响分析、申请人、审批人、状态、申请日期、决定日期 |

**度量报表.xlsx**

| Sheet | 列 |
|---|---|
| 进度 | 周次、计划完成数、实际完成数、完成率、偏差 |
| 问题 | 周次、新增、关闭、未闭环、平均关闭周期 |
| 工时 | 周次、计划工时、实际工时、工时偏差比、缺计划项数 |
| 质量 | 问题密度、按期完成率、评审发现问题数 |

## 7.4 PDF / 打印规范

- 专用打印路由：`/print/{type}/{id}`，只渲染内容，无导航与按钮。
- `print.css`：

```css
@page { size: A4; margin: 20mm 18mm; }
@page { @bottom-center { content: "第 " counter(page) " 页 / 共 " counter(pages) " 页"; } }
body { font-family: "SimSun", "宋体", serif; font-size: 10.5pt; line-height: 1.6; }
h1 { font-size: 18pt; text-align: center; }
table { border-collapse: collapse; width: 100%; }
th, td { border: 1px solid #333; padding: 4px 6px; }
thead { display: table-header-group; }   /* 跨页重复表头 */
tr { page-break-inside: avoid; }
```

- 导出方式：`window.print()`；提示用户选择"另存为 PDF"。
- 可选：设置里开启"使用 Chrome 无头自动导出 PDF"（检测本机 Chrome，未安装则回退手动）。

## 7.5 文件命名与版本

```
{项目简称}_{文档类型}_{期间}_{版本}_{日期}.{ext}

示例：
XX显控_项目周报_2026W45_V1.0_20261104.docx
XX显控_问题台账_2026-11_V1.0_20261104.xlsx
XX显控_设计评审_V1.0_20261104.docx
```

- 版本：同类型同期间重复导出时自动 +0.1（V1.0 → V1.1），可在设置改为"覆盖不升版"。
- 冲突：同名文件存在时追加 `(2)`，不覆盖。

## 7.6 导出质量校验

| 检查 | 方法 | 通过标准 |
|---|---|---|
| 占位符残留 | 渲染后扫描 `{{` | 无残留（除明确保留的） |
| 表格行数 | 对比集合长度与生成行数 | 一致 |
| 文件可打开 | zip 完整性 + 可选 Office COM 打开 | 无修复提示 |
| 中文编码 | 检查 XML 声明与字体 | 无乱码 |
| 样式保持 | 对比模板与产物的样式 ID | 字体/字号一致 |
| 大文档 | 500 行问题表格 | 生成 < 5 秒，打开正常 |

---

# 第 8 章 接口设计

## 8.1 通用约定

| 项 | 约定 |
|---|---|
| Base URL | `http://127.0.0.1:{port}/api` |
| 编码 | UTF-8，`Content-Type: application/json; charset=utf-8` |
| 时间 | 请求/响应统一 RFC3339 带时区：`2026-11-04T15:46:11+08:00`；纯日期 `2026-11-04` |
| ID | 首轮为32位随机十六进制不透明ID；目标ULID如需切换必须兼容旧ID，调用方不得依赖ID格式或排序 |
| 本地请求保护 | 首轮仅 loopback，校验 Host/Origin；写请求带 X-ProjNote-Request: 1。局域网另行评审，不沿用无认证本地模式 |
| 分页 | v0.1 limit/offset，响应 {items,total}，默认100/上限500；后续复合排序游标见4.4 |
| 排序 | `?sort=field,-field2` |
| 幂等/并发 | v0.1 更新带 version，提交中禁重复按钮；通用 Idempotency-Key 和持久化幂等表后续验收，不宣称首轮已支持 |
| 请求 ID | 响应头 `X-Request-Id`，用于排查 |

**统一响应包**：

```json
// 成功
{ "data": { ... } }
// 失败
{ "error": { "code": "VALIDATION_FAILED", "message": "期限不能早于发现日期", "detail": {"field": "due_at"}, "request_id": "01HX..." } }
```

## 8.2 接口清单

以下为完整产品目标路由；本轮可调用范围仅以 API-v0.1.md 为准。尤其恢复、导出、会议、风险、评审等目标接口不能因列在此表即视为已实现。

### 8.2.1 今日与统计

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/today` | 今日待办、记录流、本周概览、未归类数 |
| GET | `/api/stats/overview?project_id=&period=` | 汇总统计 |
| GET | `/api/stats/trend?project_id=&from=&to=` | 趋势数据（周/月） |

### 8.2.2 项目

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/projects?status=&q=&page=` | 列表 |
| POST | `/api/projects` | 新建 |
| GET | `/api/projects/{id}` | 详情 |
| PATCH | `/api/projects/{id}` | 更新 |
| DELETE | `/api/projects/{id}` | 软删除（空项目） |
| POST | `/api/projects/{id}/archive` | 归档 |
| GET | `/api/projects/{id}/stats` | 项目统计 |

### 8.2.3 任务与 WBS

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/projects/{id}/tree` | WBS 树 |
| GET | `/api/tasks?project_id=&status=&assignee=` | 平铺列表 |
| POST | `/api/tasks` | 新建 |
| GET | `/api/tasks/{id}` | 详情 |
| PATCH | `/api/tasks/{id}` | 更新（含拖拽改状态/改期） |
| DELETE | `/api/tasks/{id}` | 软删除（级联子任务确认） |
| POST | `/api/tasks/{id}/move` | 调整父级与顺序 |
| POST | `/api/tasks/batch` | 批量更新 |
| POST | `/api/tasks/import` | Excel/粘贴导入（先预览） |

### 8.2.4 问题

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/issues?project_id=&status=&severity=` | 列表 |
| POST | `/api/issues` | 新建 |
| GET | `/api/issues/{id}` | 详情（含历史） |
| PATCH | `/api/issues/{id}` | 更新 |
| POST | `/api/issues/{id}/transition` | 状态流转 |
| DELETE | `/api/issues/{id}` | 软删除 |
| POST | `/api/issues/import` | Excel 导入 |
| GET | `/api/issues/export` | 导出台账 |

### 8.2.5 记录与会议

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/records?type=&project_id=&from=&to=&tag=` | 时间线 |
| POST | `/api/records` | 新建（速记） |
| GET | `/api/records/{id}` | 详情 |
| PATCH | `/api/records/{id}` | 更新 |
| DELETE | `/api/records/{id}` | 软删除 |
| POST | `/api/records/{id}/classify` | 归类到项目 |
| POST | `/api/records/batch-classify` | 批量归类 |
| GET | `/api/records/unclassified` | 未归类池 |
| GET | `/api/meetings?...` `/api/meetings/{id}` | 会议 |
| POST | `/api/meetings` | 新建 |
| POST | `/api/meetings/{id}/actions/{action_id}/to-task` | 稳定待办ID幂等转任务 |

### 8.2.6 日志、风险、评审、配置

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/worklogs?period_type=&date=` | 查询 |
| POST | `/api/worklogs/generate` | 生成草稿 `{period_type, date, project_id}` |
| POST | `/api/worklogs` | 保存 |
| PATCH | `/api/worklogs/{id}` | 更新 |
| GET/POST | `/api/risks`、`/api/risks/{id}` | 风险 CRUD |
| GET/POST | `/api/reviews`、`/api/reviews/{id}` | 评审 CRUD |
| POST | `/api/reviews/{id}/findings/{fid}/to-issue` | 发现转问题 |
| GET/POST | `/api/config-items`、`/api/config-items/{id}` | 配置项 CRUD |
| GET/POST | `/api/change-requests`、`/api/change-requests/{id}` | 变更 CRUD |

### 8.2.7 搜索、标签、附件

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/search?q=&type=&limit=` | 全局搜索 |
| GET | `/api/tags?q=` | 标签补全 |
| POST | `/api/tags` | 新建标签 |
| POST | `/api/attachments` | multipart 上传 |
| DELETE | `/api/attachments/{id}` | 删除（进回收站） |
| GET | `/api/attachments/{id}` | 附件按ID读取；Range能力后续单独验收 |
| GET | `/files/thumb/{path}` | 缩略图 |

### 8.2.8 提醒与导出

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/reminders?from=&to=` | 提醒列表 |
| POST | `/api/reminders` | 新建 |
| POST | `/api/reminders/{id}/done` | 完成 |
| GET | `/api/templates?kind=` | 模板列表 |
| POST | `/api/templates` | 上传模板 |
| DELETE | `/api/templates/{id}` | 删除模板 |
| POST | `/api/exports` | 创建导出任务 |
| GET | `/api/exports/{id}` | 查询进度 |
| GET | `/api/exports/{id}/download` | 下载 |
| GET | `/api/exports?limit=20` | 导出历史 |
| GET | `/print/{type}/{id}` | 打印页（HTML） |

### 8.2.9 系统

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/health` | 健康检查（版本、端口、DB 状态） |
| GET | `/api/system/info` | 环境信息 |
| GET | `/api/settings`、`PUT /api/settings` | 设置 |
| GET | `/api/backups` | 备份列表 |
| POST | `/api/backups` | 立即备份 |
| — | 停服 CLI restore | 首轮离线恢复；恢复向导后续补齐，不开放运行时覆盖数据库 |
| DELETE | `/api/backups/{id}` | 删除备份 |
| GET | `/api/audit?entity_type=&entity_id=` | 审计查询 |
| GET | `/api/audit/verify` | 哈希链校验 |
| POST | `/api/system/diagnostics` | 导出诊断包 |
| POST | `/api/system/relocate` | 迁移数据目录 |

## 8.3 关键接口示例

### 8.3.1 速记创建

```http
POST /api/records
Content-Type: application/json
Idempotency-Key: 01HX3ABC...

{
  "type": "note",
  "content": "3号机柜接线松动，已通知王工处理",
  "project_id": "01HX3PROJECT...",
  "occurred_at": "2026-11-04T10:40:00+08:00",
  "tags": ["现场", "质量"],
  "location": "3号机房",
  "source": "manual",
  "attachment_ids": []
}
```

```json
201 Created
{
  "data": {
    "id": "01HX3RECORD...",
    "code": "R-2026-11-001",
    "type": "note",
    "content": "3号机柜接线松动，已通知王工处理",
    "project_id": "01HX3PROJECT...",
    "occurred_at": "2026-11-04T10:40:00+08:00",
    "tags": ["现场", "质量"],
    "classified": 1,
    "created_at": "2026-11-04T10:40:03+08:00"
  }
}
```

### 8.3.2 全局搜索

```http
GET /api/search?q=电源温升&limit=20
```

```json
{
  "data": {
    "query": "电源温升",
    "took_ms": 12,
    "groups": [
      {
        "type": "issue",
        "total": 2,
        "items": [
          {"id": "...", "code": "Q-2026-0012", "title": "电源模块温升超标",
           "project": "XX显控", "status": "fixing", "highlight": "电源模块<em>温升</em>超标"}
        ]
      },
      {"type": "record", "total": 1, "items": ["..."]}
    ]
  }
}
```

### 8.3.3 生成周报草稿

```http
POST /api/worklogs/generate
{
  "period_type": "week",
  "date": "2026-11-02",
  "project_id": "01HX3PROJECT..."
}
```

```json
200 OK
{
  "data": {
    "content": "## 一、本周完成\n- 完成接口联调…\n\n## 二、进行中\n…",
    "stats": {"done": 6, "doing": 4, "issues_new": 5, "issues_closed": 3, "hours": 38.5},
    "warnings": ["本周有 2 条记录未归类"]
  }
}
```

### 8.3.4 创建导出

```http
POST /api/exports
{
  "template_id": "01HXTPL...",
  "kind": "weekly",
  "project_ids": ["01HX3PROJECT..."],
  "range": {"from": "2026-11-02", "to": "2026-11-08"}
}
```

```json
202 Accepted
{ "data": { "task_id": "01HXEXP...", "status": "queued" } }
```

## 8.4 错误码

| 错误码 | HTTP | 含义 |
|---|---|---|
| VALIDATION_FAILED | 400 | 参数校验失败 |
| NOT_FOUND | 404 | 资源不存在 |
| CONFLICT_CODE_EXISTS | 409 | 编号重复 |
| ISSUE_CLOSE_REQUIRES_ROOT_CAUSE | 409 | 关闭问题缺少原因分析 |
| ISSUE_CLOSE_REQUIRES_VERIFY | 409 | 关闭问题缺少验证结论 |
| PROJECT_ARCHIVED | 409 | 项目已归档，禁止写入 |
| TASK_CYCLE_DETECTED | 409 | 任务层级成环 |
| TEMPLATE_PARSE_FAILED | 422 | 模板无法解析 |
| TEMPLATE_PLACEHOLDER_MISSING | 422 | 模板缺少必要占位符 |
| EXPORT_IN_PROGRESS | 429 | 已有导出任务在执行 |
| FILE_TOO_LARGE | 413 | 文件超过限制 |
| UNSUPPORTED_MEDIA_TYPE | 415 | 不支持的文件类型 |
| ACCESS_KEY_REQUIRED | 401 | 局域网模式需要访问码 |
| ACCESS_KEY_INVALID | 403 | 访问码错误（含锁定） |
| DB_LOCKED | 503 | 数据库繁忙（重试） |
| INTERNAL_ERROR | 500 | 服务内部错误 |

---

# 第 9 章 非功能设计

## 9.1 性能

| 场景 | 目标 | 手段 |
|---|---|---|
| 冷启动 | 目标 < 1s | 单独测量进程就绪与页面可操作；迁移/首次备份耗时单列，不延迟必要迁移来换数字 |
| 首页 | < 500ms 首屏 | 聚合接口一次返回、骨架屏 |
| 列表 1 万条 | < 200ms 首屏 | 游标分页 + 虚拟滚动 |
| 搜索 | < 100ms | FTS5 + 结果截断 |
| 保存记录 | < 200ms | 单事务、无远程调用 |
| 导出 500 行 docx | < 5s | 流式写 zip、避免全量内存 |
| 内存 | < 150MB | 缩略图按需、导出分页取数 |

**慢查询治理**：> 500ms 的 SQL 记入日志并附执行计划，定期审查。

## 9.2 安全与保密

| 项 | 措施 |
|---|---|
| 网络暴露 | 首轮强制 loopback；Host/Origin校验和写请求专用头；局域网属于后续独立设计 |
| 路径穿越 | 附件按ID解析受控目录；导入zip拒绝..、绝对路径、重复路径和符号链接并限制解压体积 |
| 上传 | 魔数校验、大小限制、文件名净化、独立目录不可执行 |
| XSS | 前端默认转义；Markdown 渲染启用安全模式（禁原始 HTML 或白名单） |
| CSP | `default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:` |
| 点击劫持 | `X-Frame-Options: DENY` |
| 敏感信息 | 日志不记录正文；诊断包不含业务数据 |
| 数据部署属性 | 本轮使用合成/已获准数据；真实环境遵循单位介质、网络和处理制度，本工具不作涉密适用或保密认证声明 |

## 9.3 可靠性

| 风险 | 措施 |
|---|---|
| 断电 | WAL + synchronous=FULL；确认保存必须晚于提交，目标硬件上进行中断演练，不作绝对零丢失保证 |
| 程序崩溃 | panic 恢复中间件；启动自检（DB 完整性） |
| 数据库损坏 | 启动 `PRAGMA integrity_check`；失败引导恢复备份 |
| 误删 | 目标为软删除+30天回收站；首轮不开放DELETE，软删除里程碑完成前不能暴露物理删除接口 |
| 备份损坏 | 备份带 sha256；恢复前校验 |
| 磁盘满 | 启动检查剩余空间，< 1GB 警告；导出/备份前检查 |

## 9.4 兼容性

| 项 | 要求 | 验证方式 |
|---|---|---|
| Windows | 10 1809+ / 11，x64 | 虚拟机 + 实机 |
| 浏览器 | Edge 100+ / Chrome 100+ | 双浏览器功能走查 |
| 缩放 | 100% / 125% / 150% | 视觉检查 |
| Office | 2016 / 2019 / 2021 / 365 | 打开导出 docx/xlsx |
| WPS | 2019 / 365 | 同上 |
| 杀软 | 360 / 火绒 / Defender | 扫描不报毒（不 UPX） |
| 高 DPI | 2K/4K 屏 | 界面不模糊、不重叠 |

## 9.5 可维护性

- 结构化日志（slog JSON），按日轮转保留 14 天。
- 诊断包：版本、配置（脱敏）、迁移版本、最近 500 行日志、DB 统计。
- 数据库迁移：`schema_version` 表 + 顺序 SQL，启动时自动执行，失败回滚。
- 代码规范：`gofmt` + `go vet` + `golangci-lint`（CI 或本地钩子）。

## 9.6 可扩展性

| 扩展点 | 设计 |
|---|---|
| 新实体 | store + api + 前端页面三处新增，`search_doc` 注册新 entity_type |
| 新导出模板 | 放 `templates/` + 在 `export_template` 注册，无需改代码 |
| 新字典项 | 字典表动态配置，前端下拉自动加载 |
| AI 能力 | 预留 `POST /api/ai/*` 与设置项；本地模型（whisper.cpp）或在线 API 均可接 |
| 多用户 | 独立架构升级：身份/角色、共享数据与附件、并发冲突、服务部署、备份和审计主体均需设计，非简单增加用户表/鉴权 |

## 9.7 部署与升级

- **部署**：拷贝 exe → 双击 → 完成。无安装程序、无注册表写入（自启除外，默认关闭）。
- **升级**：停服 → 保留旧程序 → 验证新程序支持当前schema → 生成并校验升级前完整备份 → 事务迁移 → 启动验证。必要迁移失败时保留可诊断旧数据；首轮之外的自动升级流程不得提前宣称完成。
- **降级**：不支持自动降级；提示"请使用备份恢复"。
- **卸载**：删除 exe 与数据目录即可；提供"导出全部数据"以防遗漏。


---

# 第 10 章 实施计划

## 10.1 阶段划分

| 版本 | 需求范围 | 独立验收与交付 |
|---|---|---|
| v0.1 首轮闭环 | P0核心 + 少量低成本周草稿能力 | 可运行Go内嵌Vue、业务数据持久化、问题强制校验、附件原字节保存、完整zip与停服CLI恢复；不称完整P0 |
| v0.2 完整记录体验 | 补齐全部P0，适量P1批量能力 | WBS拖拽/排序、速记补全、图片灯箱、首次/迁移/恢复向导、草稿恢复；按P0清单逐条验收 |
| v0.3 汇报与进度 | P1主要能力 | time_entry、看板/甘特/日历、周报模板、Excel台账、导出留痕；真实Office/WPS模板验证 |
| v0.4 证据与追溯 | P2主要能力 | revision/签署、风险/评审/发现、baseline多对多、变更、归档清单与候选自检 |
| v1.0 发布验收 | 既定P0～P2质量门槛 | Windows原生运行、兼容与缩放、性能/容量/恢复测试、真实双周试用、安装/恢复手册 |
| 后续增强 | 原P3及局域网/手机 | 单独评估HTTPS/身份、手机能力、AI；不挤占本地数据可靠性工作 |

详细任务、依赖和完成证据见 [开发计划.md](开发计划.md)。

## 10.2 工作量估算（人天）

### P0 完整记录能力（原明细合计 28 人天）

| 任务 | 人天 | 说明 |
|---|---|---|
| 工程骨架 | 3 | 目录、net/http、embed、单实例、端口、开浏览器、打包脚本 |
| 数据层 | 4 | 迁移、仓储、事务、审计哈希链 |
| 项目与任务 | 5 | 项目 CRUD、WBS 树、拖拽、进度汇总 |
| 速记与附件 | 4 | 浮层、语法解析、上传、缩略图、去重 |
| 记录时间线 | 2 | 分组、筛选、灯箱 |
| 工作日志 | 2 | 草稿生成、编辑器 |
| 搜索 | 2 | FTS5、索引同步、面板 |
| 备份与设置 | 3 | 快照、zip、恢复、向导、设置页 |
| 联调与修复 | 3 | 真实数据试用 |
| **小计** | **28** | 原25为算术错误；这是原范围估算，不是本轮交付承诺 |

### P1 汇报与进度（原明细合计 31 人天）

| 任务 | 人天 |
|---|---|
| 看板 | 3 |
| 甘特 | 4 |
| 问题台账 | 4 |
| 周报生成 | 3 |
| Word 模板引擎 | 6 |
| Excel 导出 | 3 |
| 提醒 | 2 |
| 导出中心与模板管理 | 3 |
| 联调与修复 | 3 |
| **小计** | **31** |

### P2 能查（27 人天）

| 任务 | 人天 |
|---|---|
| 风险管理 | 3 |
| 评审管理 | 4 |
| 配置项与变更 | 5 |
| 度量看板 | 4 |
| 状态机与版本锁定 | 3 |
| 归档包与校验 | 3 |
| 证据自检 | 2 |
| 联调与修复 | 3 |
| **小计** | **27** |

### P3 能省（15 人天，按需）

手机端适配 5、AI 转写 5、需求跟踪矩阵 5（可裁剪）。

### 总量与折算

| 口径 | 计算 | 结果 |
|---|---|---|
| 总计 | 28 + 31 + 27 | **86 人天**（含原 P3 15 人天为 **101**） |
| 全职（8h/天） | 86 ÷ 5 | 约 **17.2 周**，尚未包含经实测确定的变更余量 |
| 业余（每天 2h，每周 6 天） | 86 × 8 ÷ 12 | 约 **57.3 周** |
| 业余（每天 3h，每周 6 天） | 86 × 8 ÷ 18 | 约 **38.2 周** |

> 上述数字仅修正原表算术，不构成完整性核准或日期承诺。原估算遗漏了目标Windows原生验证、损坏/容量故障恢复、真实模板适配、版本模型迁移等不确定项。采用v0.1最小闭环先验证，再按实测速度滚动估算。

## 10.3 里程碑

| 里程碑 | 前置条件 | 判定标准 |
|---|---|---|
| M1 本轮可运行闭环 | v0.1代码与本地构建 | 可启动、核心API/前端流程可走通；备份/CLI恢复有自动化证据 |
| M2 完整P0 | v0.2功能清单 | P0逐项通过；失败保存不清空草稿，跨标签冲突不静默覆盖 |
| M3 汇报可交付 | v0.3 + 用户提供模板 | 真实模板样板在目标Office/WPS打开，源数据/模板版本可追溯 |
| M4 证据结构完整 | v0.4模型与迁移 | 新旧revision、基线快照和归档清单可验证；候选映射由单位核对 |
| M5 v1.0发布 | M1～M4 + Windows环境 | 完成兼容性/性能/容量/恢复测试与连续两周实际使用，无阻断缺陷 |

未获得机器与使用数据前，不填写虚构“第几周达标”或默认全绿。测试用例不适用必须说明原因，不能用总绿项数量代替规则覆盖。

## 10.4 交付物清单

| 类别 | 交付物 |
|---|---|
| 程序 | `ProjNote.exe`（Windows x64） |
| 源码 | Git 仓库（含构建脚本、迁移 SQL） |
| 文档 | 本设计方案、使用手册、备份恢复手册、模板制作说明 |
| 模板 | 内置示例模板（周报、问题台账、评审记录）各 1 份 |
| 测试 | 测试用例表、验收记录 |

## 10.5 开发环境

| 项 | 要求 |
|---|---|
| Go | 本轮Go1.24.0，正式发布须用受支持基线复验 |
| Node | 与锁定Vite匹配的Node环境；本轮以package-lock.json、npm ci及实际构建记录为准 |
| 依赖获取 | 本环境可用缓存/已配置源；只有确需下载时按网络策略使用批准的源，不假定某个镜像必然可达 |
| 模块缓存 | 建议 `GOMODCACHE=./.gomodcache`（避免用户目录权限问题） |
| 构建 | 以仓库Makefile及README记录的命令为准；前端npm ci与npm run build，Go测试/构建独立执行 |
| 版本管理 | Git（建议本地裸库 + 定期打包备份） |

---

# 第 11 章 测试与验收

## 11.1 测试策略

| 类型 | 范围 | 方法 |
|---|---|---|
| 单元测试 | 编号生成、进度汇总、状态机、哈希链、模板替换 | `go test`，核心包覆盖率 ≥ 60% |
| 集成测试 | API 层 CRUD 与事务 | httptest + 临时 DB |
| 界面走查 | 全页面主流程 | 手工清单 |
| 兼容测试 | Office/WPS、浏览器、缩放 | 矩阵式手工 |
| 性能测试 | 1 万条列表、10 万条搜索、500 行导出 | 脚本造数 |
| 恢复测试 | 备份恢复、断电模拟 | 手工 + 杀进程 |
| 安全测试 | 路径穿越、XSS、超大文件 | 构造恶意输入 |

## 11.2 关键测试用例

| # | 用例 | 步骤 | 预期 |
|---|---|---|---|
| T01 | 首次启动 | 空目录双击 exe | 向导出现，3 步完成，示例数据可选 |
| T02 | 二次启动 | 已运行时再双击 | 不重复启动，直接打开浏览器 |
| T03 | 端口冲突 | 占用 18600 后启动 | 自动用 18601，功能正常 |
| T04 | 速记保存 | Alt+N 输入文字回车 | 3 秒内出现在今日流，编号正确 |
| T05 | 速记粘贴图 | Ctrl+V 截图后保存 | 图片上传、缩略图生成、大图可看 |
| T06 | 未归类 | 不选项目保存 | 进入未归类池，首页提示 |
| T07 | 批量归类 | 选 5 条归类到项目 | 全部更新，`classified=1` |
| T08 | WBS 拖拽 | 把 A 拖到 B 内 | 成为子任务，编码重算，无环 |
| T09 | 非法拖拽 | 把父拖到自己的子下 | 禁止并提示 |
| T10 | 进度汇总 | 3 个子任务 100/50/0 | 父任务显示 50% |
| T11 | 问题关闭校验 | 不填原因直接关闭 | 阻止，定位到字段 |
| T12 | 问题关闭 | 填原因与验证后关闭 | 成功，时间线新增，统计更新 |
| T13 | 延期判定 | 在配置时区跨日/修改plan_end | overdue派生正确，status不变，取消/完成不逾期 |
| T14 | 搜索 3 字 | 搜"电源温" | 命中，< 100ms |
| T15 | 搜索 2 字 | 搜"评审" | LIKE 兜底命中 |
| T16 | 周报生成 | 选周生成 | 内容覆盖本周任务与问题，工时正确 |
| T17 | Word 导出 | 套模板导出周报 | 占位符全替换，表格行数正确，Office/WPS 均可打开 |
| T18 | 模板缺字段 | 模板含未知占位符 | 保留原样并提示清单，不静默丢弃 |
| T19 | Excel 导出 | 导出问题台账 | 三个 sheet 齐全，冻结首行，逾期标红 |
| T20 | 完整备份恢复 | 同图多引用/旧附件 → 单个zip → 停服CLI恢复到新目录 | 数据/全部附件哈希一致；不依赖父备份，当前目录有回滚点 |
| T21 | 断电 | 写入过程中杀进程 | 重启后 DB 完整，无半条记录 |
| T22 | 路径穿越 | 附件ID/恶意zip路径/符号链接输入 | 受控拒绝，不泄露/不覆盖目录外文件 |
| T23 | 超大文件 | 上传 300MB | 413，提示明确 |
| T24 | 审计链 | 篡改中间记录/移除事件/恢复旧备份 | 可发现常规断链；说明管理员重算整链无法由本地链独立证明 |
| T25 | 归档 | 归档项目 | 只读，归档包结构完整，哈希可验 |
| T26 | 迁移 | 改数据目录 | 数据与附件完整迁移，旧目录保留 |

## 11.3 验收场景（端到端）

**场景 A：v0.3 以后的一周真实使用（不是首轮完成条件）**

1. 周一建项目、导入 WBS
2. 周二现场记 5 条（含 2 张图）
3. 周三录 2 条问题并流转
4. 周四生成日报并编辑
5. 周五生成周报 → 导出 docx → 发给领导
6. 验收：全程未打开 Excel；周报内容可直接使用

**场景 B：检查应对**

1. 打开“证据自检”，逐项列出通过/不通过/不适用/待核对，规则与单位检查单一致
2. 对红项逐条补齐
3. 生成归档包 → 解压 → 校验哈希
4. 验收：检查人员能在 3 分钟内找到任一问题的完整闭环证据

**场景 C：故障恢复**

1. 模拟误删 10 条记录
2. 从昨日备份恢复
3. 验收：数据回来，恢复过程有回滚点，操作有记录

## 11.4 缺陷分级

| 级别 | 定义 | 处理 |
|---|---|---|
| S1 致命 | 数据丢失、无法启动、导出文件损坏 | 立即修复，阻断发布 |
| S2 严重 | 主流程不可用（记不了、搜不到） | 本迭代修复 |
| S3 一般 | 次要功能异常、样式错位 | 下迭代修复 |
| S4 轻微 | 文案、提示不准确 | 择机修复 |

**发布门槛**：无 S1/S2 未关闭；P0 阶段核心用例 T01–T08、T20、T21 全通过。

---

# 第 12 章 运行维护

## 12.1 安装

```
1. 获取已验证版本及校验哈希，包体以实测为准
2. 放到任意可写目录（建议 D:\Tools\ProjNote\，不要放 Program Files）
3. 双击运行
4. 首次向导：选择数据目录 → 填写姓名 → 创建示例项目或跳过
5. 浏览器自动打开 http://127.0.0.1:18600
```

便携模式：程序与 data/ 可同目录，但必须停止服务后完整复制，或使用备份包。使用移动介质遵循单位许可，跨盘原子性与拔盘故障需单独测试。

## 12.2 升级

```
1. 关闭程序
2. 新 exe 覆盖旧 exe（数据目录不动）
3. 双击启动 → 先生成并验证升级前完整备份 → 执行迁移 → 验证后启动
4. 查看"关于"确认版本
```

迁移失败：停止启动，保留失败信息和升级前完整备份。只有回滚确实成功并经过校验时才显示“已回滚”，否则提示手动恢复，不能无条件报成功。

## 12.3 迁移到新电脑

```
1. 旧机：设置 → 数据 → 立即备份 → 拷贝最新 zip
2. 新机：首轮停止服务后用 CLI 将 zip 恢复到新数据目录；v0.2 再提供恢复向导
3. 校验：项目数、记录数、附件数一致
4. 旧机数据保留至少 1 个月再删除
```

## 12.4 备份与恢复 SOP

| 频率 | 动作 | 保存位置 |
|---|---|---|
| 每日 | 自动（启动时） | `data/backups/` |
| 每周 | 手动导出全量包 | 移动硬盘/单位网盘（如允许） |
| 每月 | 抽查恢复一次 | 测试目录 |
| 重大操作前 | 强制备份 | 同上 |

恢复步骤：

```
设置 → 创建完整备份并下载；恢复前停止服务
→ CLI恢复到指定数据目录（取得独占锁）
→ 校验zip清单、数据库/schema、全部引用附件和哈希
→ 生成并验证当前完整回滚点
→ 原子交换 → 写恢复记录 → 重启验证
→ 失败保留原数据并明确返回非零退出码
```

## 12.5 常见问题（FAQ）

| 问题 | 原因 | 处理 |
|---|---|---|
| 双击没反应 | 端口全占 / 杀软拦截 | 查看 `logs/`；换端口；加白名单 |
| 浏览器打不开 | 默认浏览器异常 | 手动访问 `http://127.0.0.1:18600` |
| 数据目录不可写 | 放在 Program Files | 迁移到用户目录 |
| 导出 Word 排版乱 | 模板用了文本框/域 | 改用段落与表格重做模板 |
| 搜索不到旧记录 | 索引未更新 | 设置 → 重建搜索索引 |
| 附件打不开 | 文件被移动/删除 | 运行"孤儿附件扫描"并恢复备份 |
| 中文变方块 | 打印页字体缺失 | 安装宋体/微软雅黑 |
| 提示数据库繁忙 | 并发写（多标签同时操作） | 等待重试；避免多窗口同时编辑同一条 |

## 12.6 容量与清理

| 项 | 策略 |
|---|---|
| 导出文件 | 保留 30 天，超期提示清理 |
| 回收站 | 后续实现30天保留策略，物理清理前校验引用；首轮不开放删除 |
| 备份 | 目标日备30/周备12与容量预算可配；首轮完整备份全部保留，无自动删除 |
| 日志 | 14 天 |
| 缩略图 | 随原图删除 |
| 孤儿附件 | 每月扫描一次，列出可清理项，人工确认 |

容量参考：1 万条记录 + 5000 张图（平均 1MB）≈ 5GB；50GB 可支撑 3–5 年。

---

# 第 13 章 风险与对策

| # | 风险 | 概率 | 影响 | 对策 |
|---|---|---|---|---|
| R1 | 真实数据部署制度与默认假设不同 | 待确认 | 高 | 使用合成/获准数据继续开发；真实上线前核对单位制度并单独评估部署 |
| R2 | 记录习惯坚持不下来 | 中 | 高 | 速记入口做到极致（3 秒）；首页正反馈；先用 2 周验证 |
| R3 | docx 模板兼容性差 | 高 | 中 | 只支持段落+表格；提供模板制作说明；降级 Markdown |
| R4 | 中文搜索召回不足 | 中 | 中 | trigram + LIKE 兜底；实测语料；必要时引入 bigram |
| R5 | 单人开发周期过长 | 高 | 中 | v0.1首轮闭环，v0.2补齐P0；按验收证据滚动估算，不用版本名冒充全部需求完成 |
| R6 | 数据丢失 | 低 | 极高 | 每日备份 + 审计 + 恢复演练 |
| R7 | 杀软误报 | 中 | 中 | 不 UPX；提供哈希；必要时签名 |
| R8 | 范围蔓延 | 高 | 中 | 非目标清单写死；新需求一律进 P3 池 |
| R9 | GJB 口径与单位体系不符 | 中 | 高 | 拿到实际模板与检查单后再定字段；保留可配置 |
| R10 | 性能随数据增长下降 | 中 | 低 | 索引与分页；1 万条实测；10 万条压测 |

---

# 第 14 章 待确认事项与假设

## 14.1 后续适配需确认的 5 件事（不阻断合成数据开发）

| # | 事项 | 选项 | 影响 |
|---|---|---|---|
| Q1 | **真实数据部署属性与允许设备** | 单位允许的介质/网络/设备/数据范围 | 默认仅以合成数据开发；部署方案另评估，不作简单+30%承诺 |
| Q2 | **标准版本、适用条款与检查单** | 单位体系负责人提供适用材料 | 用于核对候选映射、字段与导出，不预设成熟度对应关系或完整覆盖 |
| Q3 | **现有模板** | 有无 docx/xlsx 模板 | 无模板需先设计，工期 +5 人天，且导出效果依赖模板质量 |
| Q4 | **编号规则** | 现有规则 / 由工具生成 | 若需与现有系统对齐，需增加映射与校验 |
| Q5 | **目标运行与办公环境** | Windows版本、Office/WPS版本、缩放比例 | 本轮已采用Vue；环境用于发布兼容性验收，不能由Linux构建推定通过 |

## 14.2 关键决策点（可推翻，但需评估影响）

| # | 决策 | 当前选择 | 若改变的影响 |
|---|---|---|---|
| D1 | 单机 vs 多人 | 单机 | 多人作为独立架构范围重新估算，不能承诺仅+15人天 |
| D2 | 桌面优先 vs 手机优先 | 桌面优先 | 手机优先需重做布局与交互：+10 人天 |
| D3 | Markdown vs 富文本 | Markdown | 富文本存储与导出更复杂：+5 人天，且数据耐久性下降 |
| D4 | 模板导出 vs 重新排版 | 模板 | 重新排版工作量大且难对齐体系：+15 人天 |
| D5 | 单库 vs 分库 | 单库 | 由数据体量、写频率、查询计划、备份恢复窗口等实测决定，不以50万条硬阈值判定 |
| D6 | AI 现在做 vs 以后做 | 以后 | 现在做：+10 人天，且依赖网络/模型 |
| D7 | 记录锁定 vs 自由编辑 | 锁定 | 不锁定则证据可信度下降 |
| D8 | 是否内置示例数据 | 内置 | 无影响 |

## 14.3 需要你提供的材料

1. 一份真实的**周报**（脱敏即可）
2. 一份**问题台账/问题报告单**
3. 一份**评审记录**
4. **项目计划**模板
5. 编号规则说明（项目号、文档号）

拿到后，模板占位符与字段可一次性对齐，避免后期返工。

---

# 附录 A 术语表

| 术语 | 说明 |
|---|---|
| ULID | 26 位可排序唯一 ID，字典序即时间序 |
| WAL | SQLite 预写日志模式，提升并发与可靠性 |
| FTS5 | SQLite 全文检索扩展 |
| trigram | FTS5 的分词器，按 3 字符切分，适合中文子串检索 |
| bm25 | FTS5 的相关性排序算法 |
| CSP | 内容安全策略，防 XSS |
| 软删除 | 标记 `deleted_at` 而非物理删除 |
| 哈希链 | 每条审计含前链哈希，可发现常规断链；无外部可信锚点时不能阻止管理员重算 |
| 幂等键 | 防止重复提交的客户端生成唯一键 |
| 游标分页 | 使用与排序一致的复合边界翻页；首轮明确使用有上限的limit/offset |

# 附录 B 快捷键

| 快捷键 | 功能 | 生效范围 |
|---|---|---|
| `Alt+N` | 打开速记 | 全局 |
| `Ctrl+K` | 全局搜索 | 全局 |
| `Ctrl+Shift+N` | 新建任务 | 任务页/项目页 |
| `Ctrl+Shift+I` | 新建问题 | 问题页 |
| `Ctrl+S` | 保存当前编辑 | 编辑器 |
| `Esc` | 关闭浮层/取消 | 全局 |
| `↑` `↓` | 列表/搜索结果导航 | 列表 |
| `Enter` | 打开选中项 | 列表 |
| `Ctrl+Enter` | 保存并继续（速记） | 速记浮层 |
| `G` 然后 `T` | 跳转任务 | 全局 |
| `G` 然后 `P` | 跳转项目 | 全局 |
| `G` 然后 `I` | 跳转问题 | 全局 |
| `G` 然后 `R` | 跳转记录 | 全局 |
| `?` | 显示快捷键帮助 | 全局 |

# 附录 C 枚举字典

| category | code | label | 说明 |
|---|---|---|---|
| phase | demo | 论证 | |
| phase | scheme | 方案 | |
| phase | develop | 工程研制 | |
| phase | finalize | 设计定型 | |
| phase | produce | 生产 | |
| phase | maintain | 维护 | |
| task_type | task | 任务 | |
| task_type | activity | 活动 | |
| task_type | review | 评审 | |
| task_type | deliverable | 交付物 | |
| task_type | meeting | 会议 | |
| task_status | todo | 待办 | |
| task_status | doing | 进行中 | |
| task_status | done | 已完成 | |
| task_view | overdue | 已延期 | 派生展示字段，不属于task_status |
| task_status | canceled | 已取消 | |
| issue_source | site | 现场 | |
| issue_source | review | 评审 | |
| issue_source | test | 测试 | |
| issue_source | customer | 客户 | |
| issue_source | internal | 内部 | |
| issue_source | inspection | 巡检 | |
| issue_severity | critical | 严重 | |
| issue_severity | major | 一般 | |
| issue_severity | minor | 轻微 | |
| issue_severity | suggestion | 建议 | |
| issue_status | open | 新建 | |
| issue_status | analyzing | 分析中 | |
| issue_status | fixing | 处理中 | |
| issue_status | verifying | 待验证 | |
| issue_status | closed | 已关闭 | |
| issue_status | rejected | 已驳回 | |
| record_type | note | 速记 | |
| record_type | inspection | 巡检 | |
| record_type | abnormal | 异常 | |
| record_type | photo | 照片 | |
| record_type | decision | 决策 | |
| record_type | other | 其他 | |
| review_type | scheme | 方案评审 | |
| review_type | design | 设计评审 | |
| review_type | code | 代码评审 | |
| review_type | test | 测试评审 | |
| review_type | archive | 归档评审 | |
| review_conclusion | pass | 通过 | |
| review_conclusion | conditional | 有条件通过 | |
| review_conclusion | fail | 不通过 | |
| risk_level | high | 高 | |
| risk_level | medium | 中 | |
| risk_level | low | 低 | |
| doc_status | draft | 草稿 | |
| doc_status | pending | 待评审 | |
| doc_status | approved | 已批准 | |
| doc_status | archived | 已归档 | |

# 附录 D 页面清单

| 页面 | 路由 | 优先级 |
|---|---|---|
| 今日工作台 | `/` | P0 |
| 项目列表 | `/projects` | P0 |
| 项目详情 | `/projects/:id` | P0 |
| 任务列表 | `/tasks` | P0 |
| 任务看板 | `/tasks/board` | P1 |
| 甘特图 | `/tasks/gantt` | P1 |
| 记录时间线 | `/records` | P0 |
| 记录详情 | `/records/:id` | P0 |
| 会议纪要 | `/meetings/:id` | P1 |
| 工作日志 | `/logs` | P0 |
| 日志编辑 | `/logs/:id` | P0 |
| 问题台账 | `/issues` | P0 |
| 问题详情 | `/issues/:id` | P0 |
| 风险 | `/risks` | P2 |
| 评审 | `/reviews` | P2 |
| 配置项 | `/config-items` | P2 |
| 变更 | `/change-requests` | P2 |
| 导出中心 | `/export` | P1 |
| 设置 | `/settings` | P0 |
| 备份恢复 | `/settings/backup` | P0 |
| 证据自检 | `/audit/self-check` | P2 |
| 打印页 | `/print/:type/:id` | P1 |

---

**需求正文结束，实施边界见附录E**

> 本方案是已修订设计，用户已授权按计划开始开发；第14章按默认假设推进，待材料用于后续适配。原附件保持不变，本文件为工作区V2.1修订版。

# 附录 E 本轮实现与待验收边界（2026-11-04）

本轮按v0.1接口启动实现，首轮API和第4章演进目标的字段/哈希封装需经迁移对齐，不能混用。数据库使用复数表名、PRAGMA user_version=1、32位随机十六进制ID，与第4章完整目标SQL不逐字相同。默认目录为程序同级data，目录不可写则使用用户config目录；首次使用只设置操作人并显式选择导入示例，完整目录向导在v0.2。具体构建和测试输出由主任务交付记录给出。下面区分本轮开发范围与后续需求；“本轮范围”不等于所有目标环境已经验收。

| 项目 | 本轮范围 | 仍需验证或后续实现 |
|---|---|---|
| 本地运行 | Go服务、内嵌Vue、loopback请求保护、平台单实例、端口与打开浏览器；默认程序同级data，不可写时fallback用户config目录 | Windows原生双击/退出/杀软/锁、正式工具链、性能和包体实测 |
| 业务闭环 | 今日、项目、任务、记录、问题、日志、搜索、设置 | 完整P0的WBS拖拽/批量、完整灯箱、向导、快捷补全体验在v0.2 |
| 数据一致性 | 事务/审计、version冲突、逾期派生、问题终态校验、归档只读；API不开放DELETE | 软删除/回收站后续实现；长期运行、并发标签、跨日/时区边界及中断恢复按用例验证 |
| 附件 | PNG/JPEG/GIF原图上传、20MB/4000万像素限制、512长边PNG缩略图、元数据哈希、按ID读取与记录关联 | 不提供WEBP/视频/语音、物理去重；Range/容量回收与50GB场景待验收 |
| 备份恢复 | 每日启动/手动完整zip、每文件哈希、备份下载、停服CLI校验/恢复；首轮全部保留 | 在线恢复不提供；保留策略/容量预算/完整清单元数据、恢复向导/迁移和大容量故障矩阵后续验收 |
| 报告 | 日/周Markdown草稿预览与显式保存 | 工时time_entry、Office/WPS模板导出、PDF/Excel在v0.3 |
| 证据模型 | 操作留痕与本地链完整性校验 | document_revision/签署、baseline、发现项、风险/评审、归档在v0.4；标准候选映射待单位核对 |
| 发布验收 | 本地构建与自动化测试证据 | 真实双周试用、Windows运行、性能容量、真实模板仍需独立验收；未验证不称达标 |


**本轮已有验证证据（2026-11-04）**：主任务以临时合成数据运行 scripts/smoke.py，本地Linux编译二进制通过端口占用顺延、单实例、任务父子汇总/409、PNG原图上传与仅图记录、中文长短词搜索、问题终结校验/流转、草稿保存/唯一性、完整ZIP下载/独立验证、停服恢复/回滚点、重启原图与审计一致性。--verify-backup和--restore还调用审计链及记录引用图片元数据/尺寸/SHA检查；成功恢复追加restore事件。此项不等于Windows原生运行、完整浏览器UI、性能/容量或真实双周试用通过，其他检查以最终命令输出为准。
