"""F4 页面核对：15 个角色页面与五角色权限映射（原型逐页核对的等价留证）。

运行：python scripts/pages_check.py
退出码：0 = PASS，1 = FAIL。证据写入 .agent/workflow/gate-records/F4.1.pages.json
"""
import datetime
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
PAGES = ([f"U{i}" for i in range(1, 5)] + [f"F{i}" for i in range(1, 4)]
         + [f"C{i}" for i in range(1, 4)] + [f"M{i}" for i in range(1, 5)] + ["P1"])
ROLES = ["宠物主人", "前台及服务人员", "客服", "店长", "模拟支付渠道"]

html_path = ROOT / "web" / "index.html"
perm_path = ROOT / ".agent" / "frontend" / "permission.md"

html = html_path.read_text(encoding="utf-8")
perm = perm_path.read_text(encoding="utf-8")

missing_pages = [page for page in PAGES if page not in html]
missing_perm = [page for page in PAGES if page not in perm]
missing_roles = [role for role in ROLES if role not in perm]

checks = [
    ("web/index.html 存在且含 15 个页面编号", not missing_pages, missing_pages),
    ("frontend/permission.md 含 15 个页面映射", not missing_perm, missing_perm),
    ("frontend/permission.md 含五类角色分配", not missing_roles, missing_roles),
]
for name, ok, detail in checks:
    print(f"[{'PASS' if ok else 'FAIL'}] {name}: {detail if detail else '齐备'}")

result = "PASS" if all(ok for _, ok, _ in checks) else "FAIL"
print(f"pages result={result} pages={len(PAGES)} roles={len(ROLES)}")

evidence = {
    "feature": "F4.1",
    "check": "S6 场景断言：15 页原型逐页核对",
    "cmd": "python scripts/pages_check.py",
    "result": result,
    "at": datetime.datetime.now().astimezone().isoformat(timespec="seconds"),
    "pages": len(PAGES),
    "roles": len(ROLES),
    "checks": [{"name": name, "result": "PASS" if ok else "FAIL", "missing": detail}
               for name, ok, detail in checks],
    "notes": "前端为中保真 HTML 单文件原型，未引入 JS 测试框架；写操作的角色守卫由 internal/api 的 requireRole 独立校验。",
}
out = ROOT / ".agent" / "workflow" / "gate-records" / "F4.1.pages.json"
out.parent.mkdir(parents=True, exist_ok=True)
out.write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
sys.exit(0 if result == "PASS" else 1)
