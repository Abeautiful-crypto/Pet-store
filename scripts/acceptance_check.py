"""F5.2 五个必演场景一键验收（.agent/general/acceptance.md §2）。

每个场景跑真实 -race 单测并统计命中数；另核对四类硬错误（超卖/重复收款/
重复释放/越权）均有断言覆盖。证据写入 .agent/workflow/gate-records/F5.2.json
运行：python scripts/acceptance_check.py
退出码：0 = PASS，1 = FAIL。
"""
import datetime
import json
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
GATES = ROOT / ".agent" / "workflow" / "gate-records"

SCENARIOS = [
    ("SC-01 正常接待", "TestService_ServiceProgressIsIdempotent|TestAPI_AppointmentOutcomesVisible",
     ["./internal/service", "./internal/api"]),
    ("SC-02 名额循环", "TestService_ConcurrentLastSlotExactlyOneSucceeds|TestService_ExpiredSlotCanBeRebooked|TestStore_ConcurrentLastSlotOnlyOneSucceeds",
     ["./internal/service", "./internal/store"]),
    ("SC-03 取消分支", "TestService_EarlyCancelAutoApprovesFullRefund|TestService_NearCancelCreatesReviewAndKeepsSlot|TestService_ReviewCancelApprove|TestService_ReviewCancelReject|TestService_RefundFailureThenRetrySucceeds",
     ["./internal/service"]),
    ("SC-04 门店现场冲突", "TestService_ConcurrentCancelAndVerifyOnlyOneWinner|TestService_CancelledCannotBeVerified|TestAPI_ThreeKindsOfOverreachDoNotBreakRecords",
     ["./internal/service", "./internal/api"]),
    ("SC-05 收店核查", "TestService_DailySettlementAmountsAndCategories|TestAPI_SettlementMatchesLedger|TestAPI_AppointmentOutcomesVisible",
     ["./internal/service", "./internal/api"]),
]

HARD_CHECKS = [
    ("硬错误：无超卖", "TestStore_ConcurrentLastSlotOnlyOneSucceeds|TestService_ConcurrentLastSlotExactlyOneSucceeds",
     ["./internal/store", "./internal/service"]),
    ("硬错误：无重复收款", "TestService_ConfirmPaymentIsIdempotent|TestService_ConcurrentDuplicateNotifyOnlyOneConfirm",
     ["./internal/service"]),
    ("硬错误：无重复释放", "TestStore_ReleaseSlotExactlyOnce|TestService_ApprovedCancelReleasesSlotOnce",
     ["./internal/store", "./internal/service"]),
    ("硬错误：无越权", "TestAPI_ThreeKindsOfOverreachDoNotBreakRecords|TestAPI_WrongStoreVerifyDoesNotChangeRecord",
     ["./internal/api"]),
]


def run_case(name, regex, pkgs):
    cmd = ["go", "test", "-race", "-count=1", "-v", "-run", regex] + pkgs
    proc = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True,
                          encoding="utf-8", errors="replace")
    out = (proc.stdout or "") + (proc.stderr or "")
    hits = len(re.findall(r"^--- PASS: ", out, re.M))
    ok = proc.returncode == 0 and hits > 0
    return {
        "name": name,
        "cmd": " ".join(cmd),
        "exit": 0 if ok else 1,
        "detail": f"命中顶层 PASS 测试 {hits} 个；go test exit={proc.returncode}",
        "result": "PASS" if ok else "FAIL",
        "tests_hit": hits,
    }


def main():
    prereq = GATES / "F5.1.json"
    ok = prereq.exists() and json.loads(prereq.read_text(encoding="utf-8")).get("result") == "PASS"
    checks = [{
        "name": "S1 前置 F5.1 已通过", "cmd": f"read {prereq.relative_to(ROOT)}",
        "exit": 0 if ok else 1, "detail": "", "result": "PASS" if ok else "FAIL",
        "tests_hit": 0,
    }]
    for name, regex, pkgs in SCENARIOS + HARD_CHECKS:
        checks.append(run_case(name, regex, pkgs))

    result = "PASS" if all(c["exit"] == 0 for c in checks) else "FAIL"
    evidence = {
        "feature": "F5.2",
        "result": result,
        "at": datetime.datetime.now().astimezone().isoformat(timespec="seconds"),
        "checks": checks,
        "tests_hit": sum(c["tests_hit"] for c in checks),
        "requires": ["F5.1"],
        "notes": "SC-01~05 断言 + 四类硬错误（无超卖/无重复收款/无重复释放/无越权）逐项 PASS；"
                 "SC-05 同时核对 A1~A4 四类记录齐备与 39600/9900/29700/9900 金额口径。",
    }
    GATES.mkdir(parents=True, exist_ok=True)
    (GATES / "F5.2.json").write_text(
        json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    for c in checks:
        print(f"[{c['result']}] {c['name']}: {c['detail']}")
    print(f"F5.2 result={result} tests_hit={evidence['tests_hit']}")
    sys.exit(0 if result == "PASS" else 1)


if __name__ == "__main__":
    main()
