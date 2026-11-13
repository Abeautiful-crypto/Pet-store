# 03 · 前端代码规范

> 当写页面、写组件、改交互、处理数据时遵循本文件。视觉风格见 `frontend/ui-style.md`，角色权限见 `frontend/permission.md`，后端接口契约见 `backend/constraints.md`。
> 当前交付物：`prototype/宠物门店预约系统-角色页面原型.html`（5 类角色 / 15 个页面 / 中保真线框）。

## 1. 技术栈与目录

- 当前阶段：复用 `prototype/` 下的 HTML+JS 单文件原型（便于演示与逐页验收）。
- 若后续落地框架，推荐 **React / Vue 3 + TypeScript**，目录约定：

```
src/
  api/          # 接口封装（统一错误处理、越权 401/403 透传）
  components/   # 纯展示组件（无业务状态）
  features/     # 按角色/业务分模块（user/frontdesk/cs/manager/channel）
  store/        # 全局状态（单一真相源；见第 3 节）
  utils/        # money 换算、时间格式化、状态色映射
  styles/       # 设计变量（见 frontend/ui-style.md）
```

## 2. 命名规范

- 变量/函数 `camelCase`；组件/文件 `PascalCase`（React）或 `kebab-case`（Vue SFC）。
- 状态值、角色名用常量枚举：`STATUS_CANCEL_REVIEW`、`ROLE_FRONT_DESK`，禁止魔法字符串散落。
- CSS 类名 `BEM` 或 `kebab-case`，禁止无前缀的全局裸类名污染。
- 页面编号沿用原型图：`U1–U4` / `F1–F3` / `C1–C3` / `M1–M4` / `P1`，便于与 SRS 追溯矩阵对齐。

## 3. 状态管理（单一真相源）

- 全局业务状态（预约记录、时段名额、当前登录角色）集中在一处 store，**禁止在多个页面各存一份副本再手动同步**。
- **所有角色页面读写同一条 `appointment_id`**：页面顶部"共享记录条"必须显示 `status / amount_fen=9900 / version`，且与后端主记录一致。
- 派生数据（如"某时段有效预约数"）用 selector 计算，不单独存一份。
- 任何状态变更走统一 action/dispatcher，与后端 `Transition` 守卫对齐；前端不得自行"猜"状态推进。

## 4. 金额显示（与后端 `int64` 分对齐）

- 后端传来"分"，前端一律 `formatYuan(9900)` 显示 `¥99.00`；**禁止在前端做 `price/100` 后再参与任何算术**。
- 任何金额比较/汇总先在"分"整数域完成，最后再格式化展示。
- 页面展示的四个核算指标（已收款 39600 / 已退款 9900 / 未退款 29700 / 已完成服务收入 9900）必须来自后端日清接口，**不得前端自行累加推算**。

## 5. 防越权渲染（与 `frontend/permission.md` 配合）

- 即便 UI 按角色只渲染对应页面，**每个写操作函数仍必须过 `requireRole` 守卫**；隐藏按钮 ≠ 安全。
- 用户端列表按 `currentUser` 过滤（只看自己的宠物与预约），过滤放在 api/store 层，不在渲染时"顺手 filter 假过滤"。
- 前台核销页必须显式展示三条件准入检查清单（本店 / 已确认 / 无待处理取消申请），校验失败时按钮禁用并给出原因。

## 6. 禁止项（违反即打回）

- 禁止在业务代码里写死金额浮点运算。
- 禁止把同一份预约状态拆成多份副本手动同步。
- 禁止用 `alert` 满天飞做反馈；统一用 toast / 内联提示。
- 禁止把密钥、内部接口地址写死在前端源码。
- 禁止用 `innerHTML` 拼接用户可控内容（XSS）。
- 禁止一次提交混多个不相关改动的前端文件（与 `general/git-convention.md` 呼应）。

## 7. 前端一页速查

1. 金额是后端"分"→ 前端只格式化展示，不做浮点算术？
2. 15 个页面是否都指向同一份预约记录？派生数据用 selector？
3. 写操作过 `requireRole` 了吗？用户列表按当前用户过滤了吗？
4. 反馈用统一 toast，没用裸 `alert` / `innerHTML` 拼用户输入？
