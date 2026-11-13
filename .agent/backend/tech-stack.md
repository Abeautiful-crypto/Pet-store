# 技术栈约束（Tech Stack）

> 当涉及选型、落库、加缓存、接 DB/Redis、写迁移、调连接池时遵循本文件。
> 本题的核心难点是**状态一致性**（名额抢占、5 分钟占位、2 小时取消、取消×核销互斥、迟到支付、退款失败不复活），技术栈用法必须服务于"每个时刻只有一种合法结果"，而不是为用而用。
> **选型沿用参考范本（Small-Commerce-Order/.agent/backend/tech-stack.md）**：本项目需求文档未指定技术栈，落地代码前如调整选型，先更新本文件。

## 技术栈总表

| 组件 | 选型 | 职责 | 指定方 |
| --- | --- | --- | --- |
| 后端语言 | **Go 1.22** | 业务编排、接口、状态机 | 沿用范本假设 |
| 持久化存储 | **MySQL 8.0+** | 门店、项目、时段、预约单、支付单、取消申请、退款单、服务记录、事件日志的**真相源** | 沿用范本假设（用户指定于范本项目） |
| 缓存 | **Redis 7+** | 时段名额读加速、支付幂等短期去重、登录态、削峰锁 | 沿用范本假设 |
| 前端 | `prototype/` HTML+JS 单文件原型（15 页）；后续可落 React/Vue | 五角色交互、权限拦截 | 本项目现状 |
| Schema 迁移 | 版本化 `up/down` SQL，禁止手改生产库 | 建表与约束 | 建议 |
| 连接池 | `database/sql` + `go-redis` 连接池 | 复用连接、限流 | 建议 |

> 演示原型期可用内存态快速验证交互；**真实后端一律以 MySQL 为真相源、Redis 仅做缓存/去重**。

---

## 1. MySQL（持久化 · 真相源）

### 1.1 Schema 要点（与 `backend/constraints.md` 铁律对齐）

- **金额**：统一 `BIGINT`（单位"分"），固定 9900；**绝不**用 `FLOAT`/`DOUBLE`。
- **时段名额**：`slots.capacity`（固定 3）、`occupied_count`；占用/释放一律
  `UPDATE slots SET occupied_count=occupied_count+1 WHERE id=? AND occupied_count<capacity`，**以影响行数判定成败**，禁止"先 SELECT 再 UPDATE"。
- **幂等-支付**：支付流水表对 `channel_trade_no` / `notify_key` 建 **UNIQUE 索引**；重复通知只插流水，不动账。
- **幂等-状态**：预约状态 CAS（`WHERE status='待支付占位' AND id=?`），失败即幂等命中；关闭/释放只发生一次。
- **唯一有效预约**：`(pet_id, slot_id, active)` 唯一约束/部分索引，保证同一宠物同一时段只有一条有效预约。
- **流水只增不改**：支付、退款、核销、异常、事件各自独立流水表，无 UPDATE/DELETE 历史行。
- **取消申请**：`reason`、`apply_at`、`hours_to_start`、`status(pending/approved/rejected)`、`reviewer`；待审核期间名额保持。
- **退款单来源与状态**：`amount_fen=9900`、`status(pending/processing/success/failed_retry)`、`retry_count`、`last_error`；状态独立于预约主记录。
- **未到店**：`no_show_reason` 必填；本题不自动产生退款单。

### 1.2 事务与一致性

- 所有"跨多表且须一致"的操作（占名额 + 写预约单 + 写事件流水；取消获准 + 释放名额 + 生成退款单）包在**一个事务**内，失败整体回滚。
- 强一致靠 **DB 约束 + 事务 + 行锁/条件更新**；取消与核销并发靠同一行的 CAS / `version` 决定唯一胜者。
- 服务完成记收入与状态变更在同一事务，`revenue_fen` 只写一次。

### 1.3 禁止项

- 禁止用 Redis 计数代替 MySQL 名额（多实例下必超卖）。
- 禁止手改生产库 schema，必须走迁移脚本。
- 禁止在应用层缓存"当前剩余名额"并据此扣减。

---

## 2. Redis（缓存 · 加速与去重，非真相源）

### 2.1 用途边界

| 用途 | 键设计 | 说明 |
| --- | --- | --- |
| 时段名额读加速 | `slot:{slot_id}` | 缓存剩余名额，**只读**；写（占/释）走 MySQL 事务，成功后再 `DEL` 该键 |
| 支付幂等短期去重 | `pay_dedup:{notify_key}` | `SETNX` + TTL，与 MySQL 唯一索引双保险；命中即视为重复通知 |
| 登录态 / 角色 / 门店 | `sess:{token}` | 存角色、用户标识与门店，供核销归属校验 |
| 占位到期扫描辅助 | `hold:{appointment_id}` | 可选；**超时关闭的正确性仍以 MySQL `pay_deadline_at` 扫描为准** |
| 削峰锁（抢名额） | `lock:book:{slot_id}` | `SET NX EX` 短时锁，仅减少无效请求，正确性仍由 MySQL 条件更新保证 |

### 2.2 缓存与 DB 一致性策略（Cache-Aside + 写失效）

- **读**：先查 Redis；未命中查 MySQL 并回写（带随机 TTL 防雪崩）。
- **写**：先更新 MySQL（事务提交成功），再 `DEL` Redis 对应键；**禁止"先更缓存再更 DB"**。
- 参考范本已沉淀的真实坑：任何存储实现接入缓存时，写提交后必须失效被改动的 `slot:{id}`，否则展示层读到旧名额。

### 2.3 禁止项

- 禁止用 Redis 名额值作为扣减依据（真相只在 MySQL）。
- 禁止把 5 分钟占位超时只放 Redis（丢数据会失守）→ 必须 MySQL `pay_deadline_at` 兜底扫描。
- 禁止把幂等去重只放 Redis → 必须 MySQL 唯一索引兜底。

---

## 3. 工程与迁移

- 工程结构见 `backend/constraints.md` 第 1 节；数据库访问集中在 `internal/store`，所有原子更新与唯一约束落在 SQL 层。
- 每次 schema 变更写迁移文件（up/down），提交前执行，保证环境一致。
- MySQL 连接池 `SetMaxOpenConns/SetMaxIdleConns` 按压测调；超时关闭类长事务避免占满连接。

## 4. 技术栈禁止项速查

1. 名额真相只在 MySQL，Redis 只缓存读、不计数。
2. 幂等必须 MySQL 唯一索引兜底，Redis 去重只是辅助。
3. 金额永远 BIGINT 分（9900），不用浮点。
4. 跨表一致走事务 + 行锁/条件更新，不靠内存锁。
5. 不引入 MQ / 分布式事务等重组件；5 分钟超时关闭优先定时扫描 DB。

## 5. 与 `backend/constraints.md` 的衔接

本文件的"MySQL/Redis 用法"是第 2 节铁律的**落地载体**：名额原子更新 → §1.1 条件更新；支付幂等 → §1.1 + §2.1 双保险；退款状态独立 → §1.1 退款单表；收入口径 → §1.1 服务记录 `revenue_fen`；取消×核销互斥 → §1.2 单行 CAS / `version`。
