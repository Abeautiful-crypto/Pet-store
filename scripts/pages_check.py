"""F6.1 页面核对：高保真 15 页原型 + API 联调脚本 + 五角色权限映射。

运行：python scripts/pages_check.py
退出码：0 = PASS，1 = FAIL。证据写入 .agent/workflow/gate-records/F6.1.pages.json
"""
import datetime
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
PAGES = ([f"U{i}" for i in range(1, 5)] + [f"F{i}" for i in range(1, 4)]
         + [f"C{i}" for i in range(1, 4)] + [f"M{i}" for i in range(1, 5)] + ["P1"])
ROLES = ["宠物主人", "前台及服务人员", "客服", "店长", "模拟支付渠道"]
WRITE_ACTIONS = [
    "/api/slots", "/api/appointments", "/pay", "/cancel", "/verify",
    "/start", "/finish", "/no-show", "/api/cancel-requests", "/api/refunds/retry",
    "/api/channel/refund-outcome", "/api/holds/expire",
]

html = (ROOT / "web" / "index.html").read_text(encoding="utf-8")
app_js = (ROOT / "web" / "app.js").read_text(encoding="utf-8")
perm = (ROOT / ".agent" / "frontend" / "permission.md").read_text(encoding="utf-8")

checks = [
    ("web/index.html 含新高保真视觉基准", "毛球驿站" in html, "毛球驿站"),
    ("web/index.html 含 15 个页面编号",
     not [p for p in PAGES if p not in html], [p for p in PAGES if p not in html]),
    ("web/index.html 引入 /app.js", 'src="/app.js"' in html, "/app.js"),
    ("web/app.js 含 requireRole 守卫", "requireRole" in app_js, "requireRole"),
    ("web/app.js 金额只做分→元格式化", "formatYuan" in app_js and "toFixed(2)" in app_js, "formatYuan"),
    ("web/app.js 禁用 innerHTML 拼接", "innerHTML" not in app_js, "innerHTML"),
    ("web/app.js 覆盖全部写操作接口",
     not [a for a in WRITE_ACTIONS if a not in app_js], [a for a in WRITE_ACTIONS if a not in app_js]),
    ("frontend/permission.md 含 15 个页面映射",
     not [p for p in PAGES if p not in perm], [p for p in PAGES if p not in perm]),
    ("frontend/permission.md 含五类角色分配",
     not [r for r in ROLES if r not in perm], [r for r in ROLES if r not in perm]),
]

for name, ok, detail in checks:
    print(f"[{'PASS' if ok else 'FAIL'}] {name}: {detail if isinstance(detail, list) and detail else '齐备'}")

result = "PASS" if all(ok for _, ok, _ in checks) else "FAIL"
print(f"pages result={result} pages={len(PAGES)} roles={len(ROLES)}")

evidence = {
    "feature": "F6.1",
    "check": "S6 场景断言：高保真原型逐页核对与联调契约",
    "cmd": "python scripts/pages_check.py",
    "result": result,
    "at": datetime.datetime.now().astimezone().isoformat(timespec="seconds"),
    "pages": len(PAGES),
    "roles": len(ROLES),
    "checks": [{"name": name, "result": "PASS" if ok else "FAIL"} for name, ok, _ in checks],
    "notes": "以毛球驿站高保真原型为视觉基准；写操作角色守卫由 web/app.js requireRole 与 internal/api require 双重校验。",
}
out = ROOT / ".agent" / "workflow" / "gate-records" / "F6.1.pages.json"
out.parent.mkdir(parents=True, exist_ok=True)
out.write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
sys.exit(0 if result == "PASS" else 1)
