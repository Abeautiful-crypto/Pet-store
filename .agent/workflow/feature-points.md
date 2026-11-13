# AI 开发工作流 · 阶段与功能点清单

> 本文件把 SRS 第 6 章 20 条 FR 拆成**可逐个开发、逐个验收**的功能点；`general/feature-gate.md` 规定"测完 PASS 才能推进"，`workflow/progress.md` 追踪状态。
> 字段说明：`ID` 唯一编号｜`功能点` 本次交付的最小能力｜`规则锚点` 题目规则 / SRS 章节｜`模块` 允许改动的包｜`必写单测` 闸门 S3 检查｜`断言` 判定"没问题"的依据｜`前置` 必须先 PASS 的点。

## 一、阶段划分（按依赖顺序，不可跳阶段）

| 阶段 | 主题 | 功能点 | 进入条件 | 阶段出口 |
| --- | --- | --- | --- | --- |
| 0 | 基线 | F0.1 | — | 需求/原型/SRS 三者一致，状态机迁移齐备 |
| 1 | 领域模型与配置 | F1.1 → F1.2 | F0.1 PASS | 状态机守卫、金额 9900 分、启动守卫 |
| 2 | 存储与缓存 | F2.1 → F2.2 → F2.3 | F1.1 PASS | 名额原子更新、CAS、唯一索引、Cache-Aside |
| 3 | 业务规则（8 条） | F3.1 → … → F3.11 | 阶段 1/2 相关点 PASS | 20 条 FR 单测全绿 |
| 4 | 接口与 15 个原型页 | F4.1 → … → F4.5 | F3.11 PASS | 五角色页面、权限矩阵、越权演示可验收 |
| 5 | 验收与交付 | F5.1 → F5.2 → F5.3 | F4.5 PASS | 覆盖率 ≥85%、5 个场景、全量回归 |

## 二、功能点清单

### 阶段 0 · 基线

| ID | 功能点 | 规则锚点 | 模块 | 必写单测 | 断言 | 前置 |
| --- | --- | --- | --- | --- | --- | --- |
| F0.1 | 需求 / 原型 / SRS 基线自检（不写业务代码） | SRS 第 2、12 章 | 无 | — | 8 条规则全部可追溯到 FR；5 个场景有 AC 断言；15 个页面与角色分配一致；状态机迁移矩阵齐备 | — |

> 项目当前处于**需求 + 原型阶段**：本点的闸门以文档/原型逐项核对为对象，结论写入 `gate-records/F0.1.json`，不得因"还没代码"跳过。

### 阶段 1 · 领域模型与配置

| ID | 功能点 | 规则锚点 | 模块 | 必写单测 | 断言 | 前置 |
| --- | --- | --- | --- | --- | --- | --- |
| F1.1 | 预约状态机（9 态）与领域实体 | SRS 5.4 / 规则 1、6 | `internal/model` | `state_machine_test.go`、`model_test.go` | 合法迁移通过、非法迁移明确报错、终态不可再改、金额 `int64` 9900 | F0.1 |
| F1.2 | 配置解析与启动 / 生产守卫 | `backend/tech-stack.md §3` | `internal/app` | `app_test.go` | 生产模式拒绝内存存储、必需配置缺失报错 | F1.1 |

### 阶段 2 · 存储与缓存

| ID | 功能点 | 规则锚点 | 模块 | 必写单测 | 断言 | 前置 |
| --- | --- | --- | --- | --- | --- | --- |
| F2.1 | 时段名额原子更新 / CAS / 唯一有效预约 | 规则 1 / `backend/tech-stack.md §1` | `internal/store` | `store_test.go`、`mysql_test.go` | `WHERE occupied<capacity`；同一 `pet_id+slot_id` 唯一；事务失败整体回滚 | F1.1 |
| F2.2 | 内存回退实现与缓存写失效 | `backend/tech-stack.md §2.2` | `internal/store` | `store_test.go` | 写提交后失效变化的 `slot:{id}`、回滚不误失效、Reset 失效全部 | F2.1 |
| F2.3 | Redis 名额读缓存 / 支付去重 / 会话 / 降级 | `backend/tech-stack.md §2` | `internal/cache` | `cache_test.go` | Cache-Aside 读、`SETNX` TTL、Redis 不可用降级 | F1.1 |

### 阶段 3 · 业务规则（20 条 FR 唯一落点）

| ID | 功能点 | FR / 规则 | 模块 | 必写单测 | 断言 | 前置 |
| --- | --- | --- | --- | --- | --- | --- |
| F3.1 | 时段发布与预约创建（起止时间、唯一性、可约校验） | FR-01~03 / 规则 1 | `internal/service` | `appointment_test.go` | 名额不足或已开始拒绝；并发抢最后 1 名额恰好 1 人成功 | F2.1/F2.3 |
| F3.2 | 5 分钟占位与超时 / 主动取消释放 | FR-04 / 规则 2 | `internal/service` | `appointment_test.go` | 超时后名额精确释放一次、释放后可重新预约（SC-02） | F3.1 |
| F3.3 | 支付确认幂等与关闭后迟到回调 | FR-05、FR-06 / 规则 2 | `internal/service` | `payment_test.go` | 重复通知只确认一次；迟到成功 → 异常登记 + 退款、不复活 | F3.2 |
| F3.4 | 提前取消自动受理与全额退款 | FR-07 / 规则 3 | `internal/service`、`api` | `cancel_test.go` | ≥2h 自动受理、退款单 9900、名额释放一次 | F3.3 |
| F3.5 | 临近取消申请与客服批准 / 拒绝 | FR-08 / 规则 3 | `internal/service` | `cancel_test.go` | <2h 原因必填；待审核期间名额保持且禁核销；批准/拒绝结果不同（SC-03） | F3.4 |
| F3.6 | 取消获准后禁核销、名额仅释放一次 | FR-09 / 规则 4 | `internal/service` | `cancel_test.go` | 取消获准后核销被拒；`occupied_count` 只 −1 一次 | F3.5 |
| F3.7 | 退款状态独立与失败跟进重试 | FR-10 / 规则 4 | `internal/service` | `refund_test.go` | 失败→重试→成功状态链可查；预约不复活、名额不恢复 | F3.6 |
| F3.8 | 前台核销三条件准入与服务进度幂等 | FR-11、FR-12 / 规则 5 | `internal/service` | `reception_test.go` | 本店+已确认+无待处理申请；重复核销/完成收入仍只记一次 | F3.7 |
| F3.9 | 取消×核销并发唯一结果与终态门禁 | FR-13、FR-14 / 规则 6 | `internal/service` | `reception_test.go` | 只有一个胜者、冲突留痕；已核销/服务中/已完成禁入取消（SC-04） | F3.8 |
| F3.10 | 时段结束未到店标记（写原因、不自动退款） | FR-15、FR-16 / 规则 7 | `internal/service` | `reception_test.go` | 仅时段结束且无核销记录；原因必填；无自动退款单 | F3.9 |
| F3.11 | 日清五类查询与金额三分类 / 收入口径 | FR-17、FR-18 / 规则 8 | `internal/service` | `settlement_test.go` | 39600 / 9900 / 29700 / 9900 逐笔可对账；已付款未全计收入（SC-05） | F3.10 |

### 阶段 4 · 接口与原型页

| ID | 功能点 | 规则锚点 | 模块 | 必写单测 / 等价要求 | 断言 | 前置 |
| --- | --- | --- | --- | --- | --- | --- |
| F4.1 | 用户端 4 页（U1–U4）与接口鉴权 | `frontend/permission.md` | `internal/api`、`web/` | `server_test.go` + 逐页点击核对 | 只能选自己的宠物；四种结局分别可见；越权 403 | F3.11 |
| F4.2 | 前台 3 页（F1–F3）与核销准入展示 | FR-11、FR-19 | `internal/api`、`web/` | `permission_test.go` + 逐页核对 | 三条件检查清单可见；非本店员工操作拦截且记录不变 | F4.1 |
| F4.3 | 客服 3 页（C1–C3）与回显 | FR-08、FR-10 | `internal/api`、`web/` | `server_test.go` + 逐页核对 | 批准/拒绝/重试结果用户可见且与店长一致 | F4.1 |
| F4.4 | 店长 4 页（M1–M4）与日清核算 | FR-01、15、17、18 | `internal/api`、`web/` | `server_test.go` + 逐页核对 | 四指标与逐笔对账表一致；未到店原因可见 | F4.1 |
| F4.5 | 渠道控制台（P1）与越权演示 | FR-20 / SC-04 | `internal/api`、`web/` | `permission_test.go` + 越权演示 | 重复通知/迟到回调/退款失败可配置；三类越权均不破坏记录 | F4.2/F4.3/F4.4 |

### 阶段 5 · 验收与交付

| ID | 功能点 | 规则锚点 | 断言 | 前置 |
| --- | --- | --- | --- | --- |
| F5.1 | 覆盖率门禁 ≥85% | `general/unit-test.md T8` | 整体 + 核心包每包 ≥85% | F4.5 |
| F5.2 | 五个必演场景一键验收 | `general/acceptance.md §2` | SC-01~05 断言全 PASS；四类记录齐全；无四类硬错误 | F5.1 |
| F5.3 | 全量回归 | `general/acceptance.md §1` | `gofmt` / `go vet` / `go test -race -count=1 ./...` 全绿 | F5.2 |

## 三、闸门命令（每个功能点完成后立刻执行）

```bash
gofmt -l .
go vet ./...
go test -race -count=1 -run '<本功能点正则>' ./internal/<模块>
# 涉及场景时补跑原型逐页核对 / 一键验收脚本
```

退出码 `0 = PASS`（才允许推进）、`1 = FAIL`（先写 issue-log 再修、原闸门重跑）。
证据写入 `workflow/gate-records/<ID>.json`；`progress.md` 状态必须与证据一致。
原型阶段（尚无 Go 代码）以文档/页面逐项核对为闸门对象，结论同样要落证据，不得跳过。

## 四、清单维护规则

1. 新增模块 → 先在清单加功能点（含模块、必写单测、断言、前置），再写实现。
2. 新增测试 → 同步更新该功能点的正则，保证闸门能命中。
3. **禁止**为让闸门变绿而删功能点、放宽断言、跳过依赖或调低覆盖率阈值；规则变更走 `issue-log.md` → 更新约束文件的闭环。

## 五、阶段 6 · 高保真前端与本地外部依赖接入（2026-11-13 追加）

> 需求：以 `毛球驿站-宠物门店预约系统-高保真原型.html` 为视觉基准生成前端页面，并接入本机 MySQL 8（真相源）与 Redis 7（缓存/去重）。
> 阶段出口：F6.1~F6.4 闸门 PASS，覆盖率仍 ≥85%，五场景重跑全绿。

| ID | 功能点 | 规则锚点 | 模块 | 必写单测 | 断言 | 前置 |
| --- | --- | --- | --- | --- | --- | --- |
| F6.1 | 高保真 15 页前端生成与 API 联调 | `frontend/*.md`、FR-01~20 | `web/`、`internal/api` | `internal/api/frontend_test.go`、`scripts/pages_check.py` | 新原型 15 页编号齐备且被静态托管；`requireRole` 守卫覆盖写操作；金额只做分→元格式化；API 数据驱动面板可读写 | F5.3 |
| F6.2 | 本地 MySQL 真相源接入 | `backend/tech-stack.md §1` | `internal/store`、`internal/app`、`cmd/server` | `internal/app/deps_test.go`、`internal/store/mysql_integration_test.go` | DSN 必填守卫；迁移可执行；真实 MySQL 上名额原子条件更新不超卖、`uk_pet_slot_active` 唯一索引生效 | F5.3 |
| F6.3 | 本地 Redis 缓存/去重/会话与降级 | `backend/tech-stack.md §2` | `internal/cache` | `internal/cache/redis_test.go` | Cache-Aside 读、SETNX TTL、写失效、Redis 不可用降级为不阻塞业务 | F5.3 |
| F6.4 | 外部依赖接入后的回归与覆盖率复查 | `general/acceptance.md §1`、`unit-test.md T8` | `scripts/` | `scripts/coverage_check.py`、`scripts/acceptance_check.py`、`scripts/regression_check.py` | 全部闸门 PASS；核心包每包 ≥85%；SC-01~05 重跑全绿；`gofmt`/`go vet`/`-race` 全绿 | F6.1/F6.2/F6.3 |
