// Package model 定义宠物门店预约系统的领域实体与预约主状态机。
// 需求锚点：SRS V1.0 第 5 章状态机、第 6 章 20 条 FR、题目原文 8 条业务规则。
package model

// Status 预约主状态（SRS 5.4，共 9 态）。
// 状态值使用常量，禁止在各角色代码中散落魔法字符串。
type Status string

const (
	// StatusNone 表示空闲名额，仅作为"创建预约"迁移的起点，不是预约状态。
	StatusNone Status = ""

	StatusPendingPay   Status = "pending_pay"   // 待支付（占位中），SRS 5.4 / 规则 2
	StatusConfirmed    Status = "confirmed"     // 已确认，有效付款成功
	StatusCancelReview Status = "cancel_review" // 取消审核中，<2h 申请待客服处理
	StatusVerified     Status = "verified"      // 已核销，前台核验到店
	StatusInService    Status = "in_service"    // 服务中，服务人员登记开始
	StatusCompleted    Status = "completed"     // 已完成，登记完成并记收入一次
	StatusCancelled    Status = "cancelled"     // 已取消，提前自动受理或客服批准
	StatusClosed       Status = "closed"        // 已关闭，占位超时/主动取消/异常关闭
	StatusNoShow       Status = "no_show"       // 未到店，时段结束后店长标记
)

// Event 触发预约主状态迁移的业务事件。
type Event string

const (
	EventCreate          Event = "create"            // 创建预约
	EventPaySuccess      Event = "pay_success"       // 支付成功通知
	EventHoldTimeout     Event = "hold_timeout"      // 5 分钟占位超时
	EventCancelByUser    Event = "cancel_by_user"    // 未付款主动取消
	EventCancelEarly     Event = "cancel_early"      // 提前取消（≥2h）自动受理
	EventCancelApplyNear Event = "cancel_apply_near" // 临近取消（<2h）转人工审核
	EventCancelApprove   Event = "cancel_approve"    // 客服批准取消
	EventCancelReject    Event = "cancel_reject"     // 客服拒绝取消
	EventVerify          Event = "verify"            // 前台核销
	EventStartService    Event = "start_service"     // 登记服务开始
	EventFinishService   Event = "finish_service"    // 登记服务完成
	EventMarkNoShow      Event = "mark_no_show"      // 时段结束标记未到店
	EventLatePayCallback Event = "late_pay_callback" // 关闭后迟到的支付成功回调
)

// 支付单状态（SRS 5.4「支付 / 退款 / 名额状态」）。与预约主状态解耦记录。
type PaymentStatus string

const (
	PaymentUnpaid        PaymentStatus = "unpaid"
	PaymentProcessing    PaymentStatus = "processing"
	PaymentSucceeded     PaymentStatus = "succeeded"
	PaymentExceptionWait PaymentStatus = "exception_wait_refund"
	PaymentRefunded      PaymentStatus = "refunded"
)

// 退款单状态。独立于预约状态，失败不改变已取消终态（规则 4）。
type RefundStatus string

const (
	RefundNone       RefundStatus = "none"
	RefundPending    RefundStatus = "pending"
	RefundProcessing RefundStatus = "processing"
	RefundSucceeded  RefundStatus = "succeeded"
	RefundFailed     RefundStatus = "failed_retry"
)

// 取消申请状态（SRS 5.4 / FR-08）。
type CancelRequestStatus string

const (
	CancelRequestPending  CancelRequestStatus = "pending"
	CancelRequestApproved CancelRequestStatus = "approved"
	CancelRequestRejected CancelRequestStatus = "rejected"
)

// 时段状态（由服务器时间驱动，SRS 5.2）。
type SlotStatus string

const (
	SlotNotStarted SlotStatus = "not_started"
	SlotOngoing    SlotStatus = "ongoing"
	SlotEnded      SlotStatus = "ended"
)
