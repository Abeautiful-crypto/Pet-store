package model

import (
	"errors"
	"fmt"
	"time"
)

// 基础设定常量（SRS 3.3 / 题目 2.4）。金额一律 int64 分，全程禁浮点。
const (
	// AmountFen 预约款即本次服务全款，固定 9900 分（¥99.00）。
	AmountFen int64 = 9900
	// ServiceRevenueFen 服务完成时记入已完成服务收入的金额，与预约款一致。
	ServiceRevenueFen int64 = 9900

	// SlotCapacity 每时段最多接待 3 只宠物。
	SlotCapacity = 3
	// HoldDuration 未付款占位时长（规则 2）。
	HoldDuration time.Duration = 5 * time.Minute
	// CancelLeadTime 取消政策分界线，以 slot.start_at 为基准、服务器时钟（规则 3）。
	CancelLeadTime time.Duration = 2 * time.Hour

	// StoreID 预置的单一门店；前台核销需校验门店归属（规则 5）。
	StoreID = "STORE-1"
	// ServiceItemID 预置的单一洗护项目。
	ServiceItemID = "ITEM-WASH-1"
)

// Store 门店（E1）。
type Store struct {
	ID   string
	Name string
}

// ServiceItem 洗护项目（E2）。
type ServiceItem struct {
	ID       string
	Name     string
	PriceFen int64
}

// Slot 可预约时段（E3）。OccupiedCount 是名额真相源的一部分，占用/释放必须原子条件更新。
type Slot struct {
	ID            string
	StoreID       string
	ServiceItemID string
	StartAt       time.Time
	EndAt         time.Time
	Capacity      int
	OccupiedCount int
	Status        SlotStatus
}

// Validate 校验时段：门店归属、起止时间、固定容量与占用数边界（FR-01）。
func (s Slot) Validate() error {
	if s.ID == "" {
		return errors.New("slot_id 必填")
	}
	if s.StoreID != StoreID {
		return fmt.Errorf("store_id 必须为预置门店 %s，实际 %s", StoreID, s.StoreID)
	}
	if s.StartAt.IsZero() || s.EndAt.IsZero() {
		return errors.New("start_at / end_at 必填")
	}
	if !s.EndAt.After(s.StartAt) {
		return errors.New("end_at 必须晚于 start_at")
	}
	if s.Capacity != SlotCapacity {
		return fmt.Errorf("capacity 固定为 %d，实际 %d", SlotCapacity, s.Capacity)
	}
	if s.OccupiedCount < 0 || s.OccupiedCount > s.Capacity {
		return fmt.Errorf("occupied_count 越界: %d", s.OccupiedCount)
	}
	return nil
}

// User / Pet 为预置资料（E4），本题不做档案编辑。
type User struct {
	ID   string
	Name string
}

type Pet struct {
	ID      string
	OwnerID string
	Name    string
}

// Appointment 预约单（E5）是全链路唯一主记录：用户、前台、客服围绕同一条记录协作。
// 状态变更只有一个写入点，并借助 Version 乐观锁保证取消×核销并发只有一个胜者（规则 6）。
type Appointment struct {
	ID              string
	StoreID         string
	SlotID          string
	UserID          string
	PetID           string
	Status          Status
	AmountFen       int64
	CreatedAt       time.Time
	PayDeadlineAt   time.Time
	ConfirmedAt     *time.Time
	CancelRequestID string
	VerifiedAt      *time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
	NoShowReason    string
	Version         int64
}

// NewAppointment 创建待支付占位预约，写入 5 分钟占位截止时间与初始乐观锁版本（FR-02 / FR-04）。
func NewAppointment(id, storeID, slotID, userID, petID string, now time.Time) *Appointment {
	return &Appointment{
		ID:            id,
		StoreID:       storeID,
		SlotID:        slotID,
		UserID:        userID,
		PetID:         petID,
		Status:        StatusPendingPay,
		AmountFen:     AmountFen,
		CreatedAt:     now,
		PayDeadlineAt: now.Add(HoldDuration),
		Version:       1,
	}
}

// Validate 校验预约主记录的关键不变量（金额固定 9900 分、状态合法、归属必填）。
func (a Appointment) Validate() error {
	if a.ID == "" || a.StoreID == "" || a.SlotID == "" || a.UserID == "" || a.PetID == "" {
		return errors.New("appointment_id / store_id / slot_id / user_id / pet_id 必填")
	}
	if a.AmountFen != AmountFen {
		return fmt.Errorf("amount_fen 固定为 %d 分，实际 %d", AmountFen, a.AmountFen)
	}
	if a.Status == StatusNone {
		return errors.New("status 必填")
	}
	if a.Version < 1 {
		return errors.New("version 必须 >= 1")
	}
	return nil
}

// Payment 支付单（E6）。channel_trade_no / notify_key 唯一，承接重复通知与迟到回调（FR-05 / FR-06）。
type Payment struct {
	ID             string
	AppointmentID  string
	AmountFen      int64
	ChannelTradeNo string
	NotifyKey      string
	Status         PaymentStatus
	PaidAt         *time.Time
	CallbackAt     *time.Time
}

// CancelRequest 取消申请（E7）。待审核期间名额保持且禁止核销（FR-08）。
type CancelRequest struct {
	ID            string
	AppointmentID string
	Reason        string
	ApplyAt       time.Time
	HoursToStart  float64
	Type          string
	Status        CancelRequestStatus
	Reviewer      string
	ReviewedAt    *time.Time
}

// Refund 退款单（E8）。状态独立于预约状态，失败可重试且不复活已取消预约（FR-10）。
type Refund struct {
	ID            string
	AppointmentID string
	PaymentID     string
	AmountFen     int64
	Status        RefundStatus
	RetryCount    int
	LastError     string
	CreatedAt     time.Time
	FinishedAt    *time.Time
}

// ServiceRecord 服务履约记录（E9）。收入只认已完成服务记录，且只记一次（FR-12 / FR-18）。
type ServiceRecord struct {
	ID            string
	AppointmentID string
	VerifyBy      string
	VerifyAt      *time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	RevenueFen    int64
	NoShowReason  string
}

// EventLog 操作与事件日志（E10）。只增不改，冲突/越权/异常均留痕（SRS 13 章 A8）。
type EventLog struct {
	ID            string
	AppointmentID string
	Action        string
	FromStatus    Status
	ToStatus      Status
	ActorRole     string
	ActorStoreID  string
	Detail        string
	CreatedAt     time.Time
}
