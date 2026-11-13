"""F0.1 基线自检：核对需求/原型/约束三者一致性，并把闸门证据落盘。

原型阶段尚无 Go 代码，闸门对象为文档与页面逐项核对（见 .agent/general/feature-gate.md 第 4 节）。
运行：python scripts/baseline_check.py
退出码：0 = PASS，1 = FAIL。证据写入 .agent/workflow/gate-records/F0.1.json
"""
import datetime
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
CMD = "python scripts/baseline_check.py"

srs = (ROOT / "docs" / "SRS-需求规格说明书.md").read_text(encoding="utf-8")
proto = (ROOT / "prototype" / "宠物门店预约系统-角色页面原型.html").read_text(encoding="utf-8")
perm = (ROOT / ".agent" / "frontend" / "permission.md").read_text(encoding="utf-8")

checks = []


def check(name, ok, detail):
    checks.append({
        "name": name,
        "cmd": CMD,
        "exit": 0 if ok else 1,
        "detail": detail,
        "result": "PASS" if ok else "FAIL",
    })
    return ok


def missing(items, text):
    return [item for item in items if item not in text]


rules = [f"规则 {i}" for i in range(1, 9)]
check("8 条业务规则可追溯到 FR", not missing(rules, srs),
      f"规则 1~8 全部出现；缺：{missing(rules, srs)}")

frs = [f"FR-{i:02d}" for i in range(1, 21)]
check("20 条功能需求齐备", not missing(frs, srs),
      f"FR-01~FR-20 全部出现；缺：{missing(frs, srs)}")

scs = [f"SC-{i:02d}" for i in range(1, 6)]
check("5 个必演场景齐备", not missing(scs, srs),
      f"SC-01~SC-05 全部出现；缺：{missing(scs, srs)}")

acs = [f"AC-0{i}-{j}" for i, n in ((1, 3), (2, 3), (3, 3), (4, 3), (5, 3))
       for j in range(1, n + 1)]
check("5 个场景均有 AC 验收断言", not missing(acs, srs),
      f"15 条 AC 断言齐备；缺：{missing(acs, srs)}")

states = ["待支付", "已确认", "取消审核中", "已核销", "服务中",
          "已完成", "已取消", "已关闭", "未到店"]
check("预约主状态机 9 态齐备", not missing(states, srs),
      f"9 态定义齐备；缺：{missing(states, srs)}")

transitions = ["创建预约", "支付成功通知", "5 分钟超时", "提前取消", "临近取消申请",
               "客服批准", "客服拒绝", "前台核销", "登记开始 / 完成", "迟到支付成功回调",
               "时段结束仍未到店"]
check("状态机迁移矩阵齐备", not missing(transitions, srs),
      f"关键迁移事件齐备；缺：{missing(transitions, srs)}")

pages = ([f"U{i}" for i in range(1, 5)] + [f"F{i}" for i in range(1, 4)]
         + [f"C{i}" for i in range(1, 4)] + [f"M{i}" for i in range(1, 5)] + ["P1"])
check("原型 15 个页面齐备", not missing(pages, proto),
      f"U1-U4/F1-F3/C1-C3/M1-M4/P1 齐备；缺：{missing(pages, proto)}")

roles = ["宠物主人", "前台及服务人员", "客服", "店长", "模拟支付渠道"]
check("五角色权限矩阵齐备", not missing(roles, perm),
      f"5 类角色齐备；缺：{missing(roles, perm)}")

result = "PASS" if all(c["result"] == "PASS" for c in checks) else "FAIL"
evidence = {
    "feature": "F0.1",
    "result": result,
    "at": datetime.datetime.now().astimezone().isoformat(timespec="seconds"),
    "checks": checks,
    "tests_hit": 0,
    "requires": [],
    "notes": (
        "原型阶段逐项核对（尚无 Go 代码，tests_hit=0）。"
        "观察项：SRS 第 12 章追溯矩阵引用 AC-02-4，但第 10 章仅定义 AC-02-1~3；"
        "已登记 .agent/general/issue-log.md，不改动需求原文；"
        "不影响 8 规则→FR 追溯与 5 场景 AC 断言齐备。"
    ),
}

out = ROOT / ".agent" / "workflow" / "gate-records" / "F0.1.json"
out.parent.mkdir(parents=True, exist_ok=True)
out.write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

for c in checks:
    print(f"[{c['result']}] {c['name']}: {c['detail']}")
print(f"F0.1 result={result} evidence={out.relative_to(ROOT)}")
sys.exit(0 if result == "PASS" else 1)
