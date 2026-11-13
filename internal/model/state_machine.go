package model

import (
	"errors"
	"fmt"
)

var (
	// ErrIllegalTransition 表示当前状态不接受该事件（SRS 5.4 状态机守卫）。
	ErrIllegalTransition = errors.New("非法状态迁移")
	// ErrTerminalStatus 表示预约已处于终态，不得再变更（规则 6 终态门禁）。
	ErrTerminalStatus = errors.New("终态不可再变更")
)

// TransitionRule 是预约主状态机的一条合法迁移。
// Rule 记录对应的题目规则 / SRS 章节，保证每条迁移可追溯。
type TransitionRule struct {
	From  Status
	Event Event
	To    Status
	Rule  string
}

// transitionRules 是 SRS 5.4「状态迁移矩阵（关键迁移）」的唯一落点。
// 状态机逻辑集中于此，禁止散落 if status == ... 判断。
var transitionRules = []TransitionRule{
	// SRS 5.4 / 规则 1 / FR-02~03：空闲名额 → 待支付占位，名额 +1，写 pay_deadline_at。
	{StatusNone, EventCreate, StatusPendingPay, "SRS 5.4 / 规则1 / FR-02~FR-03"},
	// SRS 5.4 / 规则 2 / FR-05：notify_key 首次出现且预约未关闭 → 已确认，confirmed_at 只写一次。
	{StatusPendingPay, EventPaySuccess, StatusConfirmed, "SRS 5.4 / 规则2 / FR-05"},
	// SRS 5.4 / 规则 2 / FR-04：5 分钟占位超时 → 已关闭，名额释放一次。
	{StatusPendingPay, EventHoldTimeout, StatusClosed, "SRS 5.4 / 规则2 / FR-04"},
	// SRS 5.4 / 规则 2 / FR-04：未付款主动取消 → 已关闭，名额释放一次。
	{StatusPendingPay, EventCancelByUser, StatusClosed, "SRS 5.4 / 规则2 / FR-04"},
	// SRS 5.4 / 规则 3 / FR-07：start_at - now >= 2h → 已取消，释放名额一次并发起全额退款。
	{StatusConfirmed, EventCancelEarly, StatusCancelled, "SRS 5.4 / 规则3 / FR-07"},
	// SRS 5.4 / 规则 3 / FR-08：start_at - now < 2h 且填原因 → 取消审核中，名额保持且禁核销。
	{StatusConfirmed, EventCancelApplyNear, StatusCancelReview, "SRS 5.4 / 规则3 / FR-08"},
	// SRS 5.4 / 规则 4 / FR-09：客服批准 → 已取消，释放名额一次、发起退款。
	{StatusCancelReview, EventCancelApprove, StatusCancelled, "SRS 5.4 / 规则4 / FR-09"},
	// SRS 5.4 / 规则 3 / FR-08：客服拒绝 → 回到已确认，名额保持，用户可见拒绝结果。
	{StatusCancelReview, EventCancelReject, StatusConfirmed, "SRS 5.4 / 规则3 / FR-08"},
	// SRS 5.4 / 规则 5 / FR-11：本店 + 已确认 + 无待处理取消申请 → 已核销，verify_at 只写一次。
	{StatusConfirmed, EventVerify, StatusVerified, "SRS 5.4 / 规则5 / FR-11"},
	// SRS 5.4 / 规则 5 / FR-12：登记服务开始 → 服务中。
	{StatusVerified, EventStartService, StatusInService, "SRS 5.4 / 规则5 / FR-12"},
	// SRS 5.4 / 规则 5 / FR-12：登记服务完成 → 已完成，收入只记一次。
	{StatusInService, EventFinishService, StatusCompleted, "SRS 5.4 / 规则5 / FR-12"},
	// SRS 5.4 / 规则 7 / FR-15：时段已结束、无核销记录 → 未到店，写原因，不自动退款。
	{StatusConfirmed, EventMarkNoShow, StatusNoShow, "SRS 5.4 / 规则7 / FR-15"},
	// SRS 5.4 迁移矩阵同时列出「已核销」；实际"无核销记录"守卫由 F3.10 服务层把关。
	{StatusVerified, EventMarkNoShow, StatusNoShow, "SRS 5.4 / 规则7 / FR-15"},
	// SRS 5.4 / 规则 2 / FR-06：已关闭后迟到支付成功回调 → 状态保持已关闭，
	// 旁路登记异常并退还款项，不恢复预约、不恢复名额。
	{StatusClosed, EventLatePayCallback, StatusClosed, "SRS 5.4 / 规则2 / FR-06"},
}

// Next 返回 from 在事件 event 下的目标状态。
// 找不到合法迁移时：终态返回 ErrTerminalStatus，其余返回 ErrIllegalTransition。
// 对应题目规则 6：状态变更只有一个写入点，非法迁移必须显式报错。
func Next(from Status, event Event) (Status, error) {
	for _, rule := range transitionRules {
		if rule.From == from && rule.Event == event {
			return rule.To, nil
		}
	}
	if IsTerminal(from) {
		return from, fmt.Errorf("%w: 预约处于终态 %s，拒绝事件 %s", ErrTerminalStatus, from, event)
	}
	return from, fmt.Errorf("%w: 状态 %s 不接受事件 %s", ErrIllegalTransition, from, event)
}

// TransitionRules 返回状态迁移矩阵的副本，供闸门核对与文档追溯。
func TransitionRules() []TransitionRule {
	out := make([]TransitionRule, len(transitionRules))
	copy(out, transitionRules)
	return out
}

// AllStatuses 返回 SRS 5.4 定义的 9 个预约主状态（不含空闲名额起点）。
func AllStatuses() []Status {
	return []Status{
		StatusPendingPay, StatusConfirmed, StatusCancelReview,
		StatusVerified, StatusInService, StatusCompleted,
		StatusCancelled, StatusClosed, StatusNoShow,
	}
}

// IsTerminal 判断是否为终态；终态不可再变更（规则 6 / FR-14）。
func IsTerminal(status Status) bool {
	switch status {
	case StatusCancelled, StatusClosed, StatusCompleted, StatusNoShow:
		return true
	default:
		return false
	}
}

// OccupiesSlot 判断该状态是否占用时段名额（SRS 5.4「是否占名额」列）。
func OccupiesSlot(status Status) bool {
	switch status {
	case StatusPendingPay, StatusConfirmed, StatusCancelReview, StatusVerified, StatusInService:
		return true
	default:
		return false
	}
}

// IsActive 判断是否为"有效预约"（SRS 13 章 A4 口径）。
// 用于同一宠物唯一有效预约、日清分类与名额占用，不含已取消/已关闭/已完成/未到店。
func IsActive(status Status) bool {
	switch status {
	case StatusPendingPay, StatusConfirmed, StatusCancelReview, StatusVerified, StatusInService:
		return true
	default:
		return false
	}
}
