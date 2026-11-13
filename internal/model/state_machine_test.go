package model

import (
	"errors"
	"testing"
)

func TestStateMachine_LegalTransitions(t *testing.T) {
	cases := []struct {
		name  string
		from  Status
		event Event
		want  Status
	}{
		{"创建预约占用名额", StatusNone, EventCreate, StatusPendingPay},
		{"支付成功确认", StatusPendingPay, EventPaySuccess, StatusConfirmed},
		{"占位超时关闭", StatusPendingPay, EventHoldTimeout, StatusClosed},
		{"未付款主动取消关闭", StatusPendingPay, EventCancelByUser, StatusClosed},
		{"提前取消自动受理", StatusConfirmed, EventCancelEarly, StatusCancelled},
		{"临近取消转审核", StatusConfirmed, EventCancelApplyNear, StatusCancelReview},
		{"客服批准取消", StatusCancelReview, EventCancelApprove, StatusCancelled},
		{"客服拒绝回到已确认", StatusCancelReview, EventCancelReject, StatusConfirmed},
		{"前台核销", StatusConfirmed, EventVerify, StatusVerified},
		{"登记服务开始", StatusVerified, EventStartService, StatusInService},
		{"登记服务完成", StatusInService, EventFinishService, StatusCompleted},
		{"已确认时段结束未到店", StatusConfirmed, EventMarkNoShow, StatusNoShow},
		{"已核销时段结束未到店", StatusVerified, EventMarkNoShow, StatusNoShow},
		{"迟到支付回调保持已关闭", StatusClosed, EventLatePayCallback, StatusClosed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Next(tc.from, tc.event)
			if err != nil {
				t.Fatalf("合法迁移不应报错: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Next(%s, %s) = %s, want %s", tc.from, tc.event, got, tc.want)
			}
		})
	}
}

func TestStateMachine_IllegalTransitions(t *testing.T) {
	cases := []struct {
		name  string
		from  Status
		event Event
	}{
		{"未付款不能核销", StatusPendingPay, EventVerify},
		{"未核销不能登记开始", StatusConfirmed, EventStartService},
		{"待支付不能登记完成", StatusPendingPay, EventFinishService},
		{"取消审核中禁止核销", StatusCancelReview, EventVerify},
		{"空闲名额不能直接确认", StatusNone, EventPaySuccess},
		{"已完成不能登记完成", StatusInService, EventVerify},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Next(tc.from, tc.event); !errors.Is(err, ErrIllegalTransition) {
				t.Fatalf("期望非法迁移错误 ErrIllegalTransition，实际: %v", err)
			}
		})
	}
}

func TestStateMachine_TerminalStates(t *testing.T) {
	terminals := []Status{StatusCancelled, StatusClosed, StatusCompleted, StatusNoShow}
	events := []Event{
		EventCreate, EventPaySuccess, EventHoldTimeout, EventCancelByUser,
		EventCancelEarly, EventCancelApplyNear, EventCancelApprove,
		EventCancelReject, EventVerify, EventStartService,
		EventFinishService, EventMarkNoShow,
	}
	for _, status := range terminals {
		if !IsTerminal(status) {
			t.Fatalf("%s 应为终态", status)
		}
		for _, event := range events {
			t.Run(string(status)+"/"+string(event), func(t *testing.T) {
				if _, err := Next(status, event); !errors.Is(err, ErrTerminalStatus) {
					t.Fatalf("终态 %s 应拒绝 %s，实际: %v", status, event, err)
				}
			})
		}
	}
}

func TestStateMachine_LateCallbackDoesNotReviveClosed(t *testing.T) {
	got, err := Next(StatusClosed, EventLatePayCallback)
	if err != nil {
		t.Fatalf("迟到回调应可登记异常，不应报错: %v", err)
	}
	if got != StatusClosed {
		t.Fatalf("迟到回调不得复活预约，实际状态: %s", got)
	}
}

func TestStateMachine_AllNineStatuses(t *testing.T) {
	want := []Status{
		StatusPendingPay, StatusConfirmed, StatusCancelReview,
		StatusVerified, StatusInService, StatusCompleted,
		StatusCancelled, StatusClosed, StatusNoShow,
	}
	got := AllStatuses()
	if len(got) != len(want) {
		t.Fatalf("状态机应恰有 9 态，实际 %d", len(got))
	}
	for i, status := range want {
		if got[i] != status {
			t.Fatalf("第 %d 态 = %s, want %s", i, got[i], status)
		}
	}
}

func TestStateMachine_OccupiesSlot(t *testing.T) {
	cases := []struct {
		status Status
		want   bool
	}{
		{StatusPendingPay, true},
		{StatusConfirmed, true},
		{StatusCancelReview, true},
		{StatusVerified, true},
		{StatusInService, true},
		{StatusCompleted, false},
		{StatusCancelled, false},
		{StatusClosed, false},
		{StatusNoShow, false},
		{StatusNone, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.status), func(t *testing.T) {
			if got := OccupiesSlot(tc.status); got != tc.want {
				t.Fatalf("OccupiesSlot(%s) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestStateMachine_ActiveStatuses(t *testing.T) {
	cases := []struct {
		status Status
		want   bool
	}{
		{StatusPendingPay, true},
		{StatusConfirmed, true},
		{StatusCancelReview, true},
		{StatusVerified, true},
		{StatusInService, true},
		{StatusCompleted, false},
		{StatusCancelled, false},
		{StatusClosed, false},
		{StatusNoShow, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.status), func(t *testing.T) {
			if got := IsActive(tc.status); got != tc.want {
				t.Fatalf("IsActive(%s) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestStateMachine_TransitionMatrixIsComplete(t *testing.T) {
	rules := TransitionRules()
	if len(rules) < 14 {
		t.Fatalf("关键迁移矩阵条目过少: %d", len(rules))
	}
	seen := map[Status]map[Event]bool{}
	for _, r := range rules {
		if r.Rule == "" {
			t.Fatalf("迁移 %s+%s 缺少题目规则/SRS 锚点注释", r.From, r.Event)
		}
		if seen[r.From] == nil {
			seen[r.From] = map[Event]bool{}
		}
		if seen[r.From][r.Event] {
			t.Fatalf("迁移 %s+%s 重复定义", r.From, r.Event)
		}
		seen[r.From][r.Event] = true
	}
}
