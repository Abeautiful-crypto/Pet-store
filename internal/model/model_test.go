package model

import (
	"testing"
	"time"
)

func TestModel_AmountIsInt64Fen9900(t *testing.T) {
	var amount int64 = AmountFen
	if amount != 9900 {
		t.Fatalf("预约款应为 9900 分，实际 %d", amount)
	}
	if ServiceRevenueFen != AmountFen {
		t.Fatalf("服务全款即收入基数，实际收入 %d 分", ServiceRevenueFen)
	}
}

func TestModel_BaselineConstants(t *testing.T) {
	if SlotCapacity != 3 {
		t.Fatalf("每时段容量应为 3，实际 %d", SlotCapacity)
	}
	if HoldDuration != 5*time.Minute {
		t.Fatalf("占位时长应为 5 分钟，实际 %s", HoldDuration)
	}
	if CancelLeadTime != 2*time.Hour {
		t.Fatalf("取消分界线应为 2 小时，实际 %s", CancelLeadTime)
	}
	if StoreID == "" || ServiceItemID == "" {
		t.Fatal("单店单项目预置标识不得为空")
	}
}

func TestModel_NewAppointment(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	a := NewAppointment("A1", "S1", "SL1", "U1", "P1", now)
	if a.ID != "A1" || a.StoreID != "S1" || a.SlotID != "SL1" || a.UserID != "U1" || a.PetID != "P1" {
		t.Fatalf("预约主记录字段未正确写入: %+v", a)
	}
	if a.Status != StatusPendingPay {
		t.Fatalf("新建预约应为待支付占位，实际 %s", a.Status)
	}
	if a.AmountFen != AmountFen {
		t.Fatalf("金额应为 %d 分，实际 %d", AmountFen, a.AmountFen)
	}
	if want := now.Add(HoldDuration); !a.PayDeadlineAt.Equal(want) {
		t.Fatalf("pay_deadline_at = %s, want %s", a.PayDeadlineAt, want)
	}
	if a.Version != 1 {
		t.Fatalf("乐观锁初始 version 应为 1，实际 %d", a.Version)
	}
	if a.ConfirmedAt != nil || a.VerifiedAt != nil || a.StartedAt != nil || a.FinishedAt != nil {
		t.Fatal("新建预约不得预写确认/核销/履约时间")
	}
}

func TestModel_SlotValidate(t *testing.T) {
	start := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		slot    Slot
		wantErr bool
	}{
		{
			name: "合法时段",
			slot: Slot{ID: "SL1", StoreID: StoreID, StartAt: start, EndAt: start.Add(time.Hour), Capacity: SlotCapacity},
		},
		{
			name:    "结束时间不晚于开始时间",
			slot:    Slot{ID: "SL2", StoreID: StoreID, StartAt: start, EndAt: start, Capacity: SlotCapacity},
			wantErr: true,
		},
		{
			name:    "容量不是固定 3",
			slot:    Slot{ID: "SL3", StoreID: StoreID, StartAt: start, EndAt: start.Add(time.Hour), Capacity: 2},
			wantErr: true,
		},
		{
			name:    "占用数超过容量",
			slot:    Slot{ID: "SL4", StoreID: StoreID, StartAt: start, EndAt: start.Add(time.Hour), Capacity: SlotCapacity, OccupiedCount: 4},
			wantErr: true,
		},
		{
			name:    "门店不属于预置门店",
			slot:    Slot{ID: "SL5", StoreID: "OTHER", StartAt: start, EndAt: start.Add(time.Hour), Capacity: SlotCapacity},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.slot.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("期望校验失败，实际通过")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望校验通过，实际: %v", err)
			}
		})
	}
}

func TestModel_AppointmentValidate(t *testing.T) {
	a := NewAppointment("A1", StoreID, "SL1", "U1", "P1", time.Now())
	if err := a.Validate(); err != nil {
		t.Fatalf("合法预约应通过校验: %v", err)
	}
	a.AmountFen = 9901
	if err := a.Validate(); err == nil {
		t.Fatal("金额不等于 9900 分应被拒绝")
	}
}
