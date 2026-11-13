"""F5.3 全量回归（.agent/general/acceptance.md §1）。

gofmt / go vet / go test -race -count=1 ./... 三条命令逐项留证。
证据写入 .agent/workflow/gate-records/F5.3.json
运行：python scripts/regression_check.py
退出码：0 = PASS，1 = FAIL。
"""
import datetime
import json
import pathlib
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
GATES = ROOT / ".agent" / "workflow" / "gate-records"

COMMANDS = [
    ("gofmt -l 无输出", ["gofmt", "-l", "."], lambda rc, out: rc == 0 and not out.strip()),
    ("go vet ./...", ["go", "vet", "./..."], lambda rc, out: rc == 0),
    ("go test -race -count=1 ./...", ["go", "test", "-race", "-count=1", "./..."], lambda rc, out: rc == 0),
]


def main():
    prereq = GATES / "F5.2.json"
    ok = prereq.exists() and json.loads(prereq.read_text(encoding="utf-8")).get("result") == "PASS"
    checks = [{
        "name": "S1 前置 F5.2 已通过", "cmd": f"read {prereq.relative_to(ROOT)}",
        "exit": 0 if ok else 1, "detail": "", "result": "PASS" if ok else "FAIL",
    }]

    for name, cmd, judge in COMMANDS:
        proc = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True,
                              encoding="utf-8", errors="replace")
        out = (proc.stdout or "") + (proc.stderr or "")
        passed = judge(proc.returncode, out)
        checks.append({
            "name": name,
            "cmd": " ".join(cmd),
            "exit": 0 if passed else 1,
            "detail": f"exit={proc.returncode}\n{out.strip()[-400:]}",
            "result": "PASS" if passed else "FAIL",
        })

    result = "PASS" if all(c["exit"] == 0 for c in checks) else "FAIL"
    evidence = {
        "feature": "F5.3",
        "result": result,
        "at": datetime.datetime.now().astimezone().isoformat(timespec="seconds"),
        "checks": checks,
        "tests_hit": 0,
        "requires": ["F5.2"],
        "notes": "全量回归为命令级门禁（非单测正则）；F5.1 覆盖率与 F5.2 五场景证据另存。",
    }
    GATES.mkdir(parents=True, exist_ok=True)
    (GATES / "F5.3.json").write_text(
        json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    for c in checks:
        print(f"[{c['result']}] {c['name']}")
    print(f"F5.3 result={result}")
    sys.exit(0 if result == "PASS" else 1)


if __name__ == "__main__":
    main()
