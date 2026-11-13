"""F6.4 外部依赖接入后的回归与覆盖率复查闸门。

串联四个可复算脚本，检查 F6.1~F6.3 前置全 PASS，生成本功能点证据。
运行：python scripts/deps_regression_check.py
退出码：0 = PASS，1 = FAIL。证据写入 .agent/workflow/gate-records/F6.4.json
"""
import datetime
import json
import os
import pathlib
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
GATES = ROOT / ".agent" / "workflow" / "gate-records"
PYTHON = sys.executable

CHECKS = [
    ("覆盖率门禁复查（核心包每包 >=85%）", "coverage_check.py"),
    ("五场景一键验收复查", "acceptance_check.py"),
    ("全量回归复查", "regression_check.py"),
    ("高保真页面与联调契约复查", "pages_check.py"),
]


def main():
    env = os.environ.copy()
    env.setdefault("PETSTORE_TEST_DSN",
                   "root:root@tcp(127.0.0.1:3306)/pet_store?parseTime=true&charset=utf8mb4")
    env.setdefault("PETSTORE_TEST_REDIS_ADDR", "127.0.0.1:6379")

    checks = []
    for feature in ["F6.1", "F6.2", "F6.3"]:
        path = GATES / f"{feature}.json"
        ok = path.exists() and json.loads(path.read_text(encoding="utf-8")).get("result") == "PASS"
        checks.append({
            "name": f"S1 前置 {feature} 已通过", "cmd": f"read {path.relative_to(ROOT)}",
            "exit": 0 if ok else 1, "detail": "", "result": "PASS" if ok else "FAIL",
        })

    for name, script in CHECKS:
        cmd = [PYTHON, f"scripts/{script}"]
        proc = subprocess.run(cmd, cwd=ROOT, env=env, capture_output=True, text=True,
                              encoding="utf-8", errors="replace")
        out = (proc.stdout or "") + (proc.stderr or "")
        checks.append({
            "name": name, "cmd": " ".join(cmd), "exit": proc.returncode,
            "detail": out.strip()[-500:], "result": "PASS" if proc.returncode == 0 else "FAIL",
        })

    result = "PASS" if all(c["exit"] == 0 for c in checks) else "FAIL"
    evidence = {
        "feature": "F6.4",
        "result": result,
        "at": datetime.datetime.now().astimezone().isoformat(timespec="seconds"),
        "checks": checks,
        "tests_hit": 0,
        "requires": ["F6.1", "F6.2", "F6.3"],
        "notes": "串联覆盖率门禁、五场景验收、全量回归与页面核对；真实 MySQL/Redis 集成测试由环境变量启用。",
    }
    GATES.mkdir(parents=True, exist_ok=True)
    (GATES / "F6.4.json").write_text(
        json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    for c in checks:
        print(f"[{c['result']}] {c['name']}")
    print(f"F6.4 result={result}")
    sys.exit(0 if result == "PASS" else 1)


if __name__ == "__main__":
    main()
