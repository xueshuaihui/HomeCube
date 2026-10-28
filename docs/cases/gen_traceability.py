#!/usr/bin/env python3
"""生成 docs/cases/traceability.md —— PRD 接口 → case 追溯矩阵。

定位：本项目的 case 是**需求基线**（由 PRD 派生），实现向 case 收敛。
因此「PRD 里每个一期接口，是否都有 case 承接」必须是可机械校验的，不能靠人记。

数据来源：
  - 接口清单：docs/prd-homecube.md 的 3.7（HomeOS）、4.8（Finance）与 5.8（Calendar，㉚ 迁入）表格行
  - case：docs/cases/{homeos,finance,calendar}/*.md 的 `## XXX-NNN · 标题`
输出：
  - 一期接口追溯表（每个接口 → 承接 case）
  - 非一期接口（P2+ 面 / P6 能力）——不进入分母，单列以免误判为缺口
  - 未承接清单（应为空；非空即 case 覆盖缺口，脚本退出码 1）

用法：python3 docs/cases/gen_traceability.py [--check]
  --check 只校验不写文件（供 lint 调用）
"""
import re
import sys
import pathlib

ROOT = pathlib.Path(__file__).resolve().parents[2]
PRD = ROOT / "docs/prd-homecube.md"
CASES = ROOT / "docs/cases"
OUT = CASES / "traceability.md"

# ── 人工维护：PRD 接口（行内首个路径）→ 承接 case ────────────────────────────
# 键 = 该 PRD 行内出现的第一个 METHOD/path；值 = case ID 列表（可多个）
# 约定：一个接口至少一个 case；一个 case 可承接多个接口（如设置页读写）。
MAP = {
    # ── 3.7 HomeOS ────────────────────────────────────────────────────────
    "GET /api/homeos/family": ["HOM-SESS-002", "HOM-FAM-004"],
    "GET /api/homeos/families": ["HOM-FAM-004"],
    "GET /api/homeos/members": ["HOM-MEM-011"],
    "POST /api/homeos/members/invite": ["HOM-MEM-004"],
    "GET /api/homeos/permissions": ["HOM-AUTH-014", "HOM-AUTH-005"],
    "POST /api/calendar/reminders": ["CAL-TIME-005"],
    "POST /api/calendar/todos": ["CAL-TIME-002", "CAL-TIME-011", "CAL-TIME-012"],
    "GET /api/homeos/notifications": ["HOM-NOTI-003", "HOM-NOTI-007"],
    "POST /api/homeos/devices/push-token": ["HOM-NOTI-005"],
    "POST /api/homeos/events/publish": ["HOM-ADM-005"],
    "POST /api/homeos/rules": [],  # P6，规则引擎随 P6 交付
    "GET /api/homeos/family/modules": ["HOM-FAM-009", "HOM-FAM-007"],
    "PUT /api/homeos/family/modules": ["HOM-FAM-006", "HOM-FAM-008"],
    "GET /api/homeos/app/bundles": ["HOM-SYNC-014", "CAL-FACE-004"],
    "GET /api/homeos/app/bundles?codes=": ["HOM-SYNC-014"],
    "GET /api/homeos/home/summary": ["HOM-SUM-001", "HOM-SUM-003", "HOM-SUM-004"],
    "GET /api/homeos/permissions/snapshot": ["HOM-AUTH-014"],
    "GET /api/{code}/sync/changes": ["HOM-SYNC-001", "HOM-SYNC-002"],
    "GET /api/homeos/search": ["HOM-SRCH-001", "HOM-SRCH-002"],
    "POST /api/homeos/ai/classify": [],  # P6
    "GET /api/homeos/settings": ["HOM-APP-001", "HOM-APP-002"],
    "PUT /api/homeos/settings": ["HOM-APP-001", "HOM-APP-002"],
    "GET /api/homeos/app/version": ["HOM-APP-005", "HOM-APP-006"],
    "POST /api/calendar/events": ["CAL-TIME-010"],
    "POST /api/homeos/votes": ["HOM-TIME-007"],
    "POST /api/homeos/auth/sms-code": ["HOM-SESS-001"],
    "POST /api/homeos/families": ["HOM-FAM-001"],
    "POST /api/homeos/family/invite/accept": ["HOM-FAM-003"],
    "POST /api/homeos/members": ["HOM-MEM-001", "HOM-MEM-002"],
    "GET /api/calendar/events": ["CAL-TIME-001", "CAL-DUE-001", "CAL-FACE-002", "CAL-FACE-008"],
    "GET /api/calendar/todos": ["CAL-TIME-002", "CAL-TIME-004", "CAL-DUE-004"],
    "GET /api/calendar/reminders": ["CAL-TIME-005", "CAL-TIME-006", "CAL-DUE-008"],
    "GET /api/homeos/dynamics": ["HOM-TIME-009"],
    "GET /api/homeos/board/messages": ["HOM-TIME-008"],
    "GET /api/homeos/votes/{id}": ["HOM-TIME-007"],
    "POST /api/homeos/data/export": ["HOM-SYNC-013"],
    "DELETE /api/homeos/family/invites/{id}": ["HOM-MEM-005"],
    "DELETE /api/homeos/members/{id}": ["HOM-MEM-009"],
    "PUT /api/homeos/members/{id}/role": ["HOM-MEM-007", "HOM-MEM-008"],
    "POST /api/homeos/family/transfer-owner": ["HOM-MEM-010"],
    "POST /api/homeos/family/leave": ["HOM-FAM-011", "HOM-FAM-012"],
    "GET /api/homeos/data/backup-status": ["HOM-ADM-003", "HOM-SYNC-011"],
    "GET /api/homeos/audit-events": ["HOM-ADM-001", "HOM-SESS-005"],
    "GET /api/homeos/reconciliation-report": ["HOM-ADM-002"],
    "GET /api/{code}/dead-letters": ["HOM-SYNC-010", "HOM-SYNC-008"],
    "GET /api/homeos/dead-letters": ["HOM-SYNC-010"],
    # ── 4.8 Finance ───────────────────────────────────────────────────────
    "POST /api/finance/transactions": ["FIN-TX-002", "FIN-TX-003"],
    "GET /api/finance/transactions": ["FIN-TX-004", "FIN-TX-005"],
    "PUT /api/finance/recurring": ["FIN-BUD-006"],
    "GET /api/finance/transactions/{id}": ["FIN-TX-006"],
    "PUT /api/finance/transactions/{id}": ["FIN-TX-007"],
    "DELETE /api/finance/transactions/{id}": ["FIN-TX-008"],
    "GET /api/finance/accounts": ["FIN-ACC-003", "FIN-ACC-004"],
    "POST /api/finance/budgets": ["FIN-BUD-001"],
    "GET /api/finance/reports/summary": ["FIN-BAL-002", "FIN-BAL-009"],
    "GET /api/finance/reports/export": ["FIN-EXP-001", "FIN-EXP-002"],
    "POST /api/finance/ledgers": ["FIN-LED-001"],
    "POST /api/finance/settlements": ["FIN-SPLIT-002", "FIN-SPLIT-003"],
    "POST /api/finance/loans": ["FIN-LOAN-001"],
    "POST /api/finance/goals": ["FIN-LOAN-004"],
    "GET /api/finance/categories": ["FIN-CAT-001", "FIN-CAT-003"],
    "POST /api/finance/categories": ["FIN-CAT-001", "FIN-CAT-007"],
    "PUT /api/finance/categories/{id}/archive": ["FIN-CAT-003", "FIN-CAT-005"],
    "POST /api/finance/accounts": ["FIN-ACC-001", "FIN-ACC-002"],
    "POST /api/finance/accounts/{id}/archive": ["FIN-ACC-004", "FIN-ACC-005"],
    "GET /api/finance/budgets": ["FIN-BUD-002", "FIN-BUD-004"],
    "PUT /api/finance/ledgers/{id}": ["FIN-LED-003"],
    "GET /api/finance/bills": ["FIN-BUD-007", "FIN-BUD-009"],
    "GET /api/finance/recurring": ["FIN-BUD-005", "FIN-BUD-006"],
    "POST /api/finance/transactions/{id}/restore": ["FIN-TX-008", "FIN-TX-009"],
    "GET /api/finance/transactions?deleted=true": ["FIN-TX-009"],
    "GET /api/finance/ledgers": ["FIN-LED-002", "FIN-LED-004"],
    "GET /api/finance/bills/{id}": ["FIN-BUD-009"],
    "GET /api/finance/settings": ["FIN-SET-001", "FIN-SET-004"],
    "POST /api/finance/invoices": ["FIN-SPLIT-005", "FIN-SPLIT-007"],
    "GET /api/finance/demo-items": ["FIN-DEMO-001", "FIN-DEMO-002"],
    "GET /api/finance/transactions/{id}/attachments": ["FIN-TX-014", "FIN-EXP-007"],
}

P1_FACES = {"homeos", "finance", "calendar"}
METHODS = ("GET", "POST", "PUT", "PATCH", "DELETE")


def parse_prd():
    """返回 [(line_no, methods_paths, face, raw)]，仅 3.7 / 4.8 的接口表格行。"""
    lines = PRD.read_text(encoding="utf-8").splitlines()
    rows = []
    for i, ln in enumerate(lines, 1):
        if not ln.startswith("|"):
            continue
        found = re.findall(r"\b(" + "|".join(METHODS) + r")\s+(/api/[A-Za-z0-9_\-{}/\.\?=&]+)", ln)
        if not found:
            continue
        paths = [f"{m} {p}" for m, p in found]
        face = found[0][1].split("/")[2] if len(found[0][1].split("/")) > 2 else "?"
        rows.append((i, paths, face, ln))
    return rows


def parse_cases():
    """返回 {case_id: (file, title)}。"""
    out = {}
    for f in sorted(list((CASES / "homeos").glob("*.md")) + list((CASES / "finance").glob("*.md"))
                    + list((CASES / "calendar").glob("*.md"))):
        for ln in f.read_text(encoding="utf-8").splitlines():
            m = re.match(r"^## ((?:HOM|FIN|CAL)-[A-Z]+-\d{3})\s*·\s*(.+)$", ln)
            if m:
                out[m.group(1)] = (f.relative_to(CASES).as_posix(), m.group(2).strip())
    return out


def main():
    check_only = "--check" in sys.argv
    rows = parse_prd()
    cases = parse_cases()

    p1, other, unmapped = [], [], []
    for line_no, paths, face, raw in rows:
        if face not in P1_FACES:
            other.append((line_no, paths, face))
            continue
        key = paths[0]
        ids = MAP.get(key)
        if ids is None:
            unmapped.append((line_no, key))
            continue
        if not ids:  # 显式空 = 非 P1（P6 能力），单列
            other.append((line_no, paths, "P6"))
            continue
        p1.append((line_no, paths, ids))

    # 校验：引用的 case 必须真实存在
    bogus = [(lid, cid) for _, _, ids in p1 for cid in ids if cid not in cases]
    # 校验：每个 MAP 键必须能在 PRD 找到
    prd_keys = {paths[0] for _, paths, _, _ in rows}
    ghost_keys = [k for k in MAP if k not in prd_keys]

    problems = []
    if unmapped:
        problems.append("PRD 一期接口未在 MAP 中登记：" + "、".join(f"{k}(PRD:{n})" for n, k in unmapped))
    if bogus:
        problems.append("引用了不存在的 case：" + "、".join(f"{l}→{c}" for l, c in bogus))
    if ghost_keys:
        problems.append("MAP 中存在 PRD 查不到的接口键：" + "、".join(ghost_keys))

    if check_only:
        for p in problems:
            print("[FAIL] " + p)
        print(f"[OK] 一期接口 {len(p1)} 条 / 非一期 {len(other)} 条 / case {len(cases)} 条")
        return 1 if problems else 0

    # ── 生成 markdown ────────────────────────────────────────────────────
    out = []
    out.append("# PRD 接口 → Case 追溯矩阵（自动生成）\n")
    out.append("> 本文件由 `docs/cases/gen_traceability.py` 从 `docs/prd-homecube.md` 生成，**请勿手改**——改 MAP 后重跑。")
    out.append("> 用途：证明「一期 PRD 的每个接口都有 case 承接」。这是**需求覆盖率**的唯一判据；")
    out.append("> `自动化` / `实现现状` 是另一条线，不影响本表——**case 不因实现缺席而消失**。")
    out.append("> 非一期接口（P2-P6 面、P6 能力）单列在末节，**不进入分母**，避免被误判为缺口。\n")
    api_cnt = sum(len(paths) for _, paths, _ in p1)
    other_cnt = sum(len(paths) for _, paths, _ in other)
    out.append(f"**统计**：一期接口行 {len(p1)} 条（合计 {api_cnt} 个接口，全部有 case 承接）· 非一期 {len(other)} 行（{other_cnt} 个接口）· 覆盖 case {len(cases)} 条\n")
    out.append("## 一、一期接口追溯表\n")
    out.append("| PRD 行 | 接口 | 承接 case |")
    out.append("|---|---|---|")
    for line_no, paths, ids in p1:
        api = "<br>".join(f"`{p}`" for p in paths)
        cs = "<br>".join(f"`{i}`" for i in ids)
        out.append(f"| :{line_no} | {api} | {cs} |")
    out.append("\n## 二、非一期接口（不进入分母）\n")
    out.append("| PRD 行 | 接口 | 归属 | 说明 |")
    out.append("|---|---|---|---|")
    for line_no, paths, face in other:
        why = "P6 能力（规则引擎 / AI 归类随 P6 交付）" if face == "P6" else f"{face} 面属 P2-P6 出生期"
        api = "<br>".join(f"`{p}`" for p in paths)
        out.append(f"| :{line_no} | {api} | {face} | {why} |")
    out.append("\n## 三、校验结论\n")
    if problems:
        for p in problems:
            out.append(f"- ❌ {p}")
    else:
        out.append("- ✅ 一期接口全部有 case 承接；无悬空 case 引用；无 MAP 幽灵键。")
    out.append("")

    OUT.write_text("\n".join(out), encoding="utf-8")
    print(f"written {OUT}  ({len(p1)} 一期接口 / {len(other)} 非一期 / {len(cases)} case)")
    for p in problems:
        print("[FAIL] " + p)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
