"""逐功能点测试闸门执行器（.agent/general/feature-gate.md）。

执行 S1 前置证据、S2/S3 单测文件、S4 gofmt/go vet、S5 专项单测，
把真实命令与退出码写入 .agent/workflow/gate-records/<ID>.json。
退出码：0 = PASS，1 = FAIL。

示例：
  python scripts/gate.py --feature F1.1 --run "TestStateMachine|TestModel" \
      --pkg ./internal/model --requires F0.1 \
      --files internal/model/state_machine_test.go,internal/model/model_test.go
"""
import argparse
import datetime
import json
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
GATES = ROOT / ".agent" / "workflow" / "gate-records"


def run(cmd):
    proc = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True,
                          encoding="utf-8", errors="replace")
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def record(checks, name, cmd, exit_code, detail):
    checks.append({
        "name": name,
        "cmd": cmd,
        "exit": exit_code,
        "detail": detail.strip()[-300:],
        "result": "PASS" if exit_code == 0 else "FAIL",
    })


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--feature", required=True)
    ap.add_argument("--run", required=True, help="go test -run 正则")
    ap.add_argument("--pkg", required=True, help="被测包，如 ./internal/model")
    ap.add_argument("--requires", default="", help="前置功能点，逗号分隔")
    ap.add_argument("--files", default="", help="必写单测文件，逗号分隔")
    ap.add_argument("--notes", default="")
    args = ap.parse_args()

    requires = [x for x in args.requires.split(",") if x]
    files = [x for x in args.files.split(",") if x]
    checks = []

    # S1 前置功能点全部 PASS
    for req in requires:
        path = GATES / f"{req}.json"
        ok = path.exists() and json.loads(path.read_text(encoding="utf-8")).get("result") == "PASS"
        record(checks, f"S1 前置 {req} 已通过", f"read {path.relative_to(ROOT)}",
               0 if ok else 1, "" if ok else f"缺少或不是 PASS: {path}")

    # S2/S3 必写单测文件存在且非空
    for rel in files:
        path = ROOT / rel
        ok = path.exists() and path.stat().st_size > 0
        record(checks, f"S2/S3 单测文件 {rel}", f"test -s {rel}",
               0 if ok else 1, "" if ok else "文件不存在或为空")

    # S4 格式与静态检查
    rc, out = run(["gofmt", "-l", "."])
    record(checks, "S4 gofmt -l 无输出", "gofmt -l .", 0 if rc == 0 and not out.strip() else 1, out)
    rc, out = run(["go", "vet", "./..."])
    record(checks, "S4 go vet ./...", "go vet ./...", rc, out)

    # S5 功能点专项单测（-race -count=1，且至少命中 1 个测试）
    cmd = ["go", "test", "-race", "-count=1", "-v", "-run", args.run, args.pkg]
    rc, out = run(cmd)
    hits = len(re.findall(r"^--- PASS: ", out, re.M))
    record(checks, "S5 功能点专项单测", " ".join(cmd), 0 if rc == 0 and hits > 0 else 1,
           f"命中顶层 PASS 测试 {hits} 个\n{out[-300:]}" if rc != 0 or hits == 0 else f"命中顶层 PASS 测试 {hits} 个")

    result = "PASS" if all(c["exit"] == 0 for c in checks) else "FAIL"
    evidence = {
        "feature": args.feature,
        "result": result,
        "at": datetime.datetime.now().astimezone().isoformat(timespec="seconds"),
        "checks": checks,
        "tests_hit": hits,
        "requires": requires,
        "notes": args.notes,
    }
    GATES.mkdir(parents=True, exist_ok=True)
    path = GATES / f"{args.feature}.json"
    path.write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    for c in checks:
        print(f"[{c['result']}] {c['name']}")
    print(f"{args.feature} result={result} tests_hit={hits} evidence={path.relative_to(ROOT)}")
    sys.exit(0 if result == "PASS" else 1)


if __name__ == "__main__":
    main()
