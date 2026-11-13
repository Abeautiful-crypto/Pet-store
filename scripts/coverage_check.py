"""F5.1 覆盖率门禁（.agent/general/unit-test.md T8）。

采集 go test 覆盖率，按核心业务包与整体分别校验 >=85%，把真实命令与结果落盘。
main.go 仅入口装配，按 unit-test.md 豁免清单排除在"整体"之外。
运行：python scripts/coverage_check.py
退出码：0 = PASS，1 = FAIL。证据写入 .agent/workflow/gate-records/F5.1.json
"""
import datetime
import json
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
GATES = ROOT / ".agent" / "workflow" / "gate-records"
THRESHOLD = 85.0
CORE = ["internal/api", "internal/app", "internal/cache", "internal/model",
        "internal/service", "internal/store"]


def run(cmd):
    proc = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True,
                          encoding="utf-8", errors="replace")
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def record(checks, name, cmd, exit_code, detail=""):
    checks.append({"name": name, "cmd": cmd, "exit": exit_code,
                   "detail": detail.strip()[-400:],
                   "result": "PASS" if exit_code == 0 else "FAIL"})


def parse_profile(path):
    per_file = {}
    for line in path.read_text(encoding="utf-8").splitlines()[1:]:
        m = re.match(r"^(.+?):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$", line.strip())
        if not m:
            continue
        file_path, num_stmt, count = m.group(1), int(m.group(6)), int(m.group(7))
        total, covered = per_file.get(file_path, (0, 0))
        total += num_stmt
        if count > 0:
            covered += num_stmt
        per_file[file_path] = (total, covered)
    return per_file


def main():
    checks = []
    prereq = GATES / "F4.5.json"
    ok = prereq.exists() and json.loads(prereq.read_text(encoding="utf-8")).get("result") == "PASS"
    record(checks, "S1 前置 F4.5 已通过", f"read {prereq.relative_to(ROOT)}", 0 if ok else 1)

    rc, out = run(["gofmt", "-l", "."])
    record(checks, "S4 gofmt -l 无输出", "gofmt -l .", 0 if rc == 0 and not out.strip() else 1, out)
    rc, out = run(["go", "vet", "./..."])
    record(checks, "S4 go vet ./...", "go vet ./...", rc, out)

    cmd = ["go", "test", "./...", "-count=1", "-covermode=atomic", "-coverprofile=coverage.out"]
    rc, out = run(cmd)
    record(checks, "T8 覆盖率采集", " ".join(cmd), rc, out)

    profile = ROOT / "coverage.out"
    per_pkg = {}
    file_rows = []
    if profile.exists():
        for file_path, (total, covered) in parse_profile(profile).items():
            pct = 100.0 * covered / total if total else 100.0
            short = file_path.replace("github.com/Abeautiful-crypto/Pet-store/", "")
            file_rows.append((short, pct))
            pkg = short.rsplit("/", 1)[0]
            pt, pc = per_pkg.get(pkg, (0, 0))
            per_pkg[pkg] = (pt + total, pc + covered)

    coverage = {pkg: (100.0 * c / t if t else 100.0) for pkg, (t, c) in per_pkg.items()}
    core_total = sum(per_pkg.get(pkg, (0, 0))[0] for pkg in CORE)
    core_covered = sum(per_pkg.get(pkg, (0, 0))[1] for pkg in CORE)
    overall = 100.0 * core_covered / core_total if core_total else 0.0

    record(checks, "T8 整体覆盖率 >=85%（核心业务包）",
           "parse coverage.out", 0 if overall >= THRESHOLD else 1,
           f"overall={overall:.1f}%")
    for pkg in CORE:
        pct = coverage.get(pkg, 0.0)
        record(checks, f"T8 {pkg} >=85%", "parse coverage.out",
               0 if pct >= THRESHOLD else 1, f"{pkg}={pct:.1f}%")

    result = "PASS" if all(c["exit"] == 0 for c in checks) else "FAIL"
    evidence = {
        "feature": "F5.1",
        "result": result,
        "at": datetime.datetime.now().astimezone().isoformat(timespec="seconds"),
        "checks": checks,
        "coverage": {pkg: round(coverage.get(pkg, 0.0), 1) for pkg in CORE},
        "overall": round(overall, 1),
        "tests_hit": 0,
        "requires": ["F4.5"],
        "notes": "覆盖率为门禁对象（非单测正则）；main.go 按 unit-test.md 豁免清单排除；阈值 85% 不得调低。",
    }
    GATES.mkdir(parents=True, exist_ok=True)
    (GATES / "F5.1.json").write_text(
        json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    for c in checks:
        print(f"[{c['result']}] {c['name']}: {c['detail']}")
    if result == "FAIL":
        print("--- 低覆盖文件（供补测）---")
        for short, pct in sorted(file_rows, key=lambda r: r[1]):
            if pct < THRESHOLD:
                print(f"  {pct:5.1f}%  {short}")
    print(f"F5.1 result={result} overall={overall:.1f}%")
    sys.exit(0 if result == "PASS" else 1)


if __name__ == "__main__":
    main()
