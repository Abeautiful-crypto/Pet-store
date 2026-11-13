package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

func confirmedService(t *testing.T) (*Service, *mutableClock, *model.Appointment) {
	t.Helper()
	ctx := context.Background()
	svc, clock, appt := paidService(t)
	if err := svc.ConfirmPayment(ctx, appt.ID, "NOTIFY-1", "TRADE-1"); err != nil {
		t.Fatal(err)
	}
	return svc, clock, appt
}

func nearCancelService(t *testing.T) (*Service, *mutableClock, *model.Appointment) {
	t.Helper()
	ctx := context.Background()
	svc, clock, appt := confirmedService(t)
	clock.Advance(3 * time.Hour) // 距 start_at 剩 1 小时，进入 <2h 人工审核分支
	_ = ctx
	return svc, clock, appt
}

// ---- F3.4 提前取消自动受理与全额退款（FR-07 / 规则 3）----

func TestService_EarlyCancelAutoApprovesFullRefund(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := confirmedService(t)

	refund, err := svc.Cancel(ctx, appt.ID, "U1", "")
	if err != nil {
		t.Fatalf("提前取消应自动受理: %v", err)
	}
	if refund == nil || refund.AmountFen != model.AmountFen {
		t.Fatalf("应生成 9900 分全额退款单，实际 %+v", refund)
	}
	if refund.Status != model.RefundSucceeded {
		t.Fatalf("自动受理退款应成功，实际 %s", refund.Status)
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusCancelled {
		t.Fatalf("提前取消后应为已取消，实际 %s", got.Status)
	}
	slot, _ := svc.store.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 0 {
		t.Fatalf("取消获准应释放名额，occupied_count = %d", slot.OccupiedCount)
	}
	req, ok := svc.ledger.CancelRequestOf(appt.ID)
	if !ok || req.Type != "自动受理" || req.Status != model.CancelRequestApproved {
		t.Fatalf("自动受理应留可查的取消申请，实际 %+v", req)
	}
}

func TestService_ExactlyTwoHoursGoesAutoBranch(t *testing.T) {
	ctx := context.Background()
	svc, clock, appt := confirmedService(t)
	clock.Advance(2 * time.Hour) // 恰等于 2h，按 A2 口径走自动受理

	refund, err := svc.Cancel(ctx, appt.ID, "U1", "")
	if err != nil || refund == nil {
		t.Fatalf("等于 2 小时应自动受理并全额退款: refund=%+v err=%v", refund, err)
	}
}

func TestService_EarlyCancelReleasesSlotOnce(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := confirmedService(t)
	if _, err := svc.Cancel(ctx, appt.ID, "U1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Cancel(ctx, appt.ID, "U1", ""); err == nil {
		t.Fatal("重复取消必须显式报错")
	}
	slot, _ := svc.store.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 0 {
		t.Fatalf("重复取消不得二次扣减名额，occupied_count = %d", slot.OccupiedCount)
	}
	if refunds := svc.ledger.Refunds(); len(refunds) != 1 {
		t.Fatalf("重复取消不得重复生成退款单，实际 %d 条", len(refunds))
	}
}

func TestService_CancelRejectsOtherUser(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := confirmedService(t)
	if _, err := svc.Cancel(ctx, appt.ID, "U2", ""); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("他人不得取消预约，实际: %v", err)
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusConfirmed {
		t.Fatalf("越权取消不得改动记录，实际 %s", got.Status)
	}
	slot, _ := svc.store.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 1 {
		t.Fatalf("越权取消不得释放名额，occupied_count = %d", slot.OccupiedCount)
	}
}

func TestService_CancelRejectsUnpaidHold(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := paidService(t)
	if _, err := svc.Cancel(ctx, appt.ID, "U1", ""); !errors.Is(err, model.ErrIllegalTransition) {
		t.Fatalf("未付款占位不得走取消退款流程，实际: %v", err)
	}
}

// ---- F3.5 临近取消申请与客服批准 / 拒绝（FR-08 / 规则 3）----

func TestService_NearCancelRequiresReason(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := nearCancelService(t)
	if _, err := svc.Cancel(ctx, appt.ID, "U1", ""); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("临近取消必须填原因，实际: %v", err)
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusConfirmed {
		t.Fatalf("校验失败不得改动状态，实际 %s", got.Status)
	}
}

func TestService_NearCancelCreatesReviewAndKeepsSlot(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := nearCancelService(t)
	refund, err := svc.Cancel(ctx, appt.ID, "U1", "临时有事")
	if err != nil || refund != nil {
		t.Fatalf("临近取消应转人工审核、暂不退款: refund=%+v err=%v", refund, err)
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusCancelReview {
		t.Fatalf("应为取消审核中，实际 %s", got.Status)
	}
	slot, _ := svc.store.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 1 {
		t.Fatalf("待审核期间名额必须保持，occupied_count = %d", slot.OccupiedCount)
	}
	req, ok := svc.ledger.CancelRequestOf(appt.ID)
	if !ok || req.Status != model.CancelRequestPending || req.Reason != "临时有事" {
		t.Fatalf("应留下待审核申请与原因，实际 %+v", req)
	}
}

func TestService_PendingReviewBlocksVerify(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := nearCancelService(t)
	if _, err := svc.Cancel(ctx, appt.ID, "U1", "临时有事"); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if _, _, err := svc.store.Transition(ctx, appt.ID, model.StatusCancelReview,
		model.EventVerify, got.Version); !errors.Is(err, model.ErrIllegalTransition) {
		t.Fatalf("待审核期间禁止核销，实际: %v", err)
	}
	slot, _ := svc.store.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 1 {
		t.Fatalf("被拒核销不得改动名额，occupied_count = %d", slot.OccupiedCount)
	}
}

func TestService_ReviewCancelApprove(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := nearCancelService(t)
	if _, err := svc.Cancel(ctx, appt.ID, "U1", "临时有事"); err != nil {
		t.Fatal(err)
	}
	req, _ := svc.ledger.CancelRequestOf(appt.ID)
	refund, err := svc.ReviewCancel(ctx, req.ID, "CS-1", true)
	if err != nil || refund == nil {
		t.Fatalf("批准应生成退款单: refund=%+v err=%v", refund, err)
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusCancelled {
		t.Fatalf("批准后应为已取消，实际 %s", got.Status)
	}
	slot, _ := svc.store.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 0 {
		t.Fatalf("批准后应释放名额，occupied_count = %d", slot.OccupiedCount)
	}
	req2, _ := svc.ledger.CancelRequestOf(appt.ID)
	if req2.Status != model.CancelRequestApproved || req2.Reviewer != "CS-1" {
		t.Fatalf("审核结果应留痕且用户可见，实际 %+v", req2)
	}
}

func TestService_ReviewCancelReject(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := nearCancelService(t)
	if _, err := svc.Cancel(ctx, appt.ID, "U1", "临时有事"); err != nil {
		t.Fatal(err)
	}
	req, _ := svc.ledger.CancelRequestOf(appt.ID)
	refund, err := svc.ReviewCancel(ctx, req.ID, "CS-1", false)
	if err != nil || refund != nil {
		t.Fatalf("拒绝不得产生退款单: refund=%+v err=%v", refund, err)
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusConfirmed {
		t.Fatalf("拒绝后应回到已确认，实际 %s", got.Status)
	}
	slot, _ := svc.store.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 1 {
		t.Fatalf("拒绝后名额必须保持，occupied_count = %d", slot.OccupiedCount)
	}
	req2, _ := svc.ledger.CancelRequestOf(appt.ID)
	if req2.Status != model.CancelRequestRejected || req2.Reviewer != "CS-1" {
		t.Fatalf("拒绝结果应留痕，实际 %+v", req2)
	}
	if refunds := svc.ledger.Refunds(); len(refunds) != 0 {
		t.Fatalf("拒绝不得产生退款单，实际 %d 条", len(refunds))
	}
}

// ---- F3.6 取消获准后禁核销、名额仅释放一次（FR-09 / 规则 4）----

func TestService_CancelledCannotBeVerified(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := confirmedService(t)
	if _, err := svc.Cancel(ctx, appt.ID, "U1", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if _, _, err := svc.store.Transition(ctx, appt.ID, model.StatusCancelled,
		model.EventVerify, got.Version); !errors.Is(err, model.ErrTerminalStatus) {
		t.Fatalf("取消获准后不得核销，实际: %v", err)
	}
	if refunds := svc.ledger.Refunds(); len(refunds) != 1 {
		t.Fatalf("被拒核销不得改动金额记录，实际 %d 条退款单", len(refunds))
	}
}

func TestService_ApprovedCancelReleasesSlotOnce(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := nearCancelService(t)
	if _, err := svc.Cancel(ctx, appt.ID, "U1", "临时有事"); err != nil {
		t.Fatal(err)
	}
	req, _ := svc.ledger.CancelRequestOf(appt.ID)
	if _, err := svc.ReviewCancel(ctx, req.ID, "CS-1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReviewCancel(ctx, req.ID, "CS-1", true); !errors.Is(err, ErrCancelAlreadyReviewed) {
		t.Fatalf("重复批准必须被拒绝，实际: %v", err)
	}
	if _, err := svc.Cancel(ctx, appt.ID, "U1", "再次取消"); err == nil {
		t.Fatal("重复点击取消必须显式报错")
	}
	slot, _ := svc.store.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 0 {
		t.Fatalf("名额只能释放一次，occupied_count = %d", slot.OccupiedCount)
	}
	if refunds := svc.ledger.Refunds(); len(refunds) != 1 {
		t.Fatalf("不得重复生成退款单，实际 %d 条", len(refunds))
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusCancelled {
		t.Fatalf("已取消终态不得复活，实际 %s", got.Status)
	}
}

func TestService_RepeatedCancelDoesNotReopen(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := confirmedService(t)
	if _, err := svc.Cancel(ctx, appt.ID, "U1", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := svc.Cancel(ctx, appt.ID, "U1", "重复"); err == nil {
			t.Fatal("重复取消不得让预约重新可用")
		}
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusCancelled || !model.IsTerminal(got.Status) {
		t.Fatalf("已取消必须是终态，实际 %s", got.Status)
	}
}
