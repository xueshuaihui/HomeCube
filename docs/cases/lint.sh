#!/usr/bin/env bash
# P1 case 体系结构校验（只读）。退出码 0 = 全部 Gate 通过。
# 用法：bash docs/cases/lint.sh [-v]
set -uo pipefail
cd "$(dirname "$0")" || exit 1
exec python3 - "$@" <<'PY'
import re
import sys
import pathlib
from collections import Counter

ROOT = pathlib.Path(".").resolve()
VERBOSE = "-v" in sys.argv

TYPES = {"功能", "边界", "异常", "安全", "性能"}
PRIS = {"P0", "P1", "P2"}
# 用 tuple 而非 set：`next(startswith)` 依赖顺序，set 的迭代顺序随机，
# 一旦出现「一个 token 是另一个的前缀」就会随机归错类。
AUTOS = ("已实现(HTTP)", "已实现(分层)", "契约偏差", "待自动化", "未实现", "纯手动", "引用脚本", "CI门禁")
EXECS = {"未执行", "通过", "失败"}
SINGLE_FORBIDDEN = {"S7", "S8", "S9", "S10", "S11", "S12", "S15", "S16", "S17", "S18"}
SPRINTS = ({"S1", "S2", "S3", "S4", "S5", "S6", "S13", "S14", "S19",
            "S7-S8", "S9-S10", "S11-S12", "S15-S16", "S17-S18",
            "未点名(P1-M1)", "未点名(P1-M2)", "S6+S14"})

REQUIRED = ["- 关联需求：", "- 前置条件：", "- 步骤：", "- 预期结果：", "- 实现现状（快照）：", "- 执行状态："]
HEAD = re.compile(r"^## ((?:HOM|FIN)-[A-Z]+-\d{3})\s*·\s*(.+)$")

files = sorted(list((ROOT / "homeos").glob("*.md")) + list((ROOT / "finance").glob("*.md")))
errors, warns = [], []
ids, cases = [], {}

for f in files:
    rel = f.relative_to(ROOT).as_posix()
    text = f.read_text(encoding="utf-8")
    lines = text.splitlines()
    cur, buf = None, {}

    def flush(cid, buf, rel):
        if cid is None:
            return
        for k in REQUIRED:
            if not any(l.startswith(k) for l in buf["lines"]):
                errors.append(f"{rel} · {cid} 缺字段 `{k[:-1]}`")
        joined = "\n".join(buf["lines"])
        if "优先级：" not in joined:
            errors.append(f"{rel} · {cid} 缺字段 `优先级`")
        if "建议归属 Sprint：" not in joined:
            errors.append(f"{rel} · {cid} 缺字段 `建议归属 Sprint`")

        m = re.search(r"^- 类型：(\S+)", joined, re.M)
        if not m:
            errors.append(f"{rel} · {cid} 类型行格式不合规")
            t = ""
        else:
            t = m.group(1)
            if t not in TYPES:
                errors.append(f"{rel} · {cid} 类型 `{t}` 不在枚举内")

        m = re.search(r"优先级：(P\d)", joined)
        if not m:
            errors.append(f"{rel} · {cid} 优先级缺失")
            p = ""
        else:
            p = m.group(1)
            if p not in PRIS:
                errors.append(f"{rel} · {cid} 优先级 `{p}` 不在枚举内")

        m = re.search(r"建议归属 Sprint：(.+)$", joined, re.M)
        s = m.group(1).strip() if m else ""
        if s not in SPRINTS:
            if s in SINGLE_FORBIDDEN:
                errors.append(f"{rel} · {cid} Sprint `{s}` 为合并行单值（无原文依据）")
            else:
                errors.append(f"{rel} · {cid} Sprint `{s}` 不在白名单")

        m = re.search(r"^- 实现现状（快照）：(.+)$", joined, re.M)
        a = m.group(1).strip() if m else ""
        token = next((x for x in AUTOS if a.startswith(x)), None)
        dev = None
        if token is None:
            errors.append(f"{rel} · {cid} 实现现状 `{a[:20]}` 不在枚举内")
        else:
            if token.startswith("已实现") and not re.search(r":\d+", a):
                errors.append(f"{rel} · {cid} `{token}` 未带 `文件:行号` 证据")
            if token in ("未实现", "契约偏差") and len(a) <= 4:
                errors.append(f"{rel} · {cid} `{token}` 未写缺失物依据")
            if token == "引用脚本" and not re.search(r"make |deploy/scripts/|\.sh", a):
                errors.append(f"{rel} · {cid} `引用脚本` 未指向真实脚本")
            if token == "契约偏差":
                # D3：契约偏差必须同时有 文件:行号 与 DEV 编号，否则无法闭环
                if not re.search(r":\d+", a):
                    errors.append(f"{rel} · {cid} `契约偏差` 未带 `文件:行号` 证据")
                m2 = re.search(r"^- 偏差登记：(DEV-\d{2})", joined, re.M)
                if not m2:
                    errors.append(f"{rel} · {cid} `契约偏差` 缺 `- 偏差登记：DEV-NN`")
                else:
                    dev = m2.group(1)

        m = re.search(r"^- 执行状态：(.+)$", joined, re.M)
        e = m.group(1).strip() if m else ""
        # 执行状态允许带时间与 run-id：`通过 · 2026-10-27T14:32 · run-20261027-143201`
        etok = next((x for x in EXECS if e.split("·")[0].strip().startswith(x)), None)
        if etok is None:
            errors.append(f"{rel} · {cid} 执行状态 `{e[:20]}` 不在枚举内")
        if token == "契约偏差" and etok == "通过":
            errors.append(f"{rel} · {cid} `契约偏差` 的执行状态不得为 `通过`（禁止「通过（除路径外）」）")

        cases[cid] = {"type": t, "pri": p, "sprint": s, "auto": token, "exec": etok, "dev": dev}

    for ln in lines:
        m = HEAD.match(ln)
        if m:
            flush(cur, buf, rel)
            cur = m.group(1)
            ids.append(cur)
            buf = {"lines": []}
            continue
        if cur is not None:
            buf["lines"].append(ln)
    flush(cur, buf, rel)

# G-D1 ID 唯一
dups = [k for k, v in Counter(ids).items() if v > 1]
if dups:
    errors.append(f"重复 case ID：{sorted(dups)}")

total = len(ids)
if total != len(cases):
    errors.append(f"解析到 {total} 个标题但仅 {len(cases)} 条 case，存在字段断裂")

# G-D2 index.md 一致性
idx = (ROOT / "index.md").read_text(encoding="utf-8")
idx_ids = re.findall(r"^\|\s*((?:HOM|FIN)-[A-Z]+-\d{3})\s*\|", idx, re.M)
idx_dup = [k for k, v in Counter(idx_ids).items() if v > 1]
if idx_dup:
    errors.append(f"index.md 重复行：{sorted(idx_dup)}")
missing = sorted(set(cases) - set(idx_ids))
extra = sorted(set(idx_ids) - set(cases))
if missing:
    errors.append(f"index.md 缺行：{missing}")
if extra:
    errors.append(f"index.md 多余行：{extra}")

m = re.search(r"\|\s*\*\*总数\*\*\s*\|\s*\*\*(\d+)\s*条", idx)
if not m:
    errors.append("index.md 未找到「总数」统计行")
elif int(m.group(1)) != total:
    errors.append(f"index.md 总数写 {m.group(1)}，实测 {total}")

# 每个模块文件都要出现在目录索引里
for f in files:
    rel = f.relative_to(ROOT).as_posix()
    if f"`{rel}`" not in idx:
        errors.append(f"index.md 目录索引缺文件 `{rel}`")

# 统计三列与实测比对（按自动化 / 按类型 / 按优先级）
def check_stat(label, order, key):
    mm = re.search(rf"\|\s*\*\*{label}\*\*\s*\|\s*([^|]+)\|", idx)
    if not mm:
        errors.append(f"index.md 缺「{label}」统计行")
        return
    seg = mm.group(1)
    for k in order:
        want = sum(1 for c in cases.values() if c[key] == k)
        got = re.search(rf"{re.escape(k)}\s+(\d+)", seg)
        if want == 0 and not got:
            continue
        if not got:
            errors.append(f"index.md「{label}」缺 `{k}` 计数")
        elif int(got.group(1)) != want:
            errors.append(f"index.md「{label}」`{k}` 写 {got.group(1)}，实测 {want}")

check_stat("按类型", ["功能", "边界", "异常", "安全", "性能"], "type")
check_stat("按优先级", ["P0", "P1", "P2"], "pri")
check_stat("按实现现状（副指标·快照）", ["已实现(HTTP)", "已实现(分层)", "契约偏差", "待自动化", "未实现", "纯手动", "引用脚本", "CI门禁"], "auto")

# G-D3 crosscutting 引用的 case ID 必须存在
cc = ROOT / "crosscutting" / "acceptance-18.2-18.3.md"
if cc.exists():
    refs = set(re.findall(r"((?:HOM|FIN)-[A-Z]+-\d{3})", cc.read_text(encoding="utf-8")))
    ghost = sorted(refs - set(cases))
    if ghost:
        errors.append(f"crosscutting 引用了不存在的 case ID：{ghost}")

# G-G4 偏差闭环：契约偏差 ⇔ DEV 登记存在且状态为「开」
dev_path = ROOT / "DEVIATIONS.md"
devs = {}
if not dev_path.exists():
    errors.append("缺 DEVIATIONS.md（契约偏差的登记处缺失）")
else:
    for ln in dev_path.read_text(encoding="utf-8").splitlines():
        if not re.match(r"^\|\s*DEV-\d{2}\s*\|", ln):
            continue
        cells = [c.strip() for c in ln.strip().strip("|").split("|")]
        devs[cells[0]] = cells[-1]          # 登记表末列即状态
    if not devs:
        errors.append("DEVIATIONS.md 登记表为空（至少应有表头与编号行）")

dev_refs = {}
for cid, c in cases.items():
    if c["auto"] == "契约偏差":
        d = c["dev"]
        if d is None:
            continue                         # 缺编号已在字段校验里报过
        if d not in devs:
            errors.append(f"{cid} 引用的 {d} 未在 DEVIATIONS.md 登记")
        elif devs[d] != "开":
            errors.append(f"{cid} 仍标 `契约偏差`，但 {d} 已 `{devs[d]}`——关闭后必须改 `已实现(HTTP)`")
        else:
            dev_refs.setdefault(d, []).append(cid)
    elif c["dev"]:
        errors.append(f"{cid} 写了 `偏差登记：{c['dev']}`，但实现现状不是 `契约偏差`")
for d, st in devs.items():
    if st == "开" and d not in dev_refs:
        warns.append(f"{d} 状态为「开」，但没有任何 case 以 `契约偏差` 指向它（幽灵登记）")

# G-E4 crosscutting 引用的 IMPL/DEV 必须「在册」（撤销或迁移后不得仍被当作依赖）
blp = ROOT / "BACKLOG.md"
if blp.exists():
    bl_txt2 = blp.read_text(encoding="utf-8")
    live_impl = {"IMPL-" + n for n in re.findall(r"^\| IMPL-(\d+)\b", bl_txt2, re.M)}
    for ref in sorted(set(re.findall(r"(?:IMPL|DEV)-\d{2}", cc.read_text(encoding="utf-8")))):
        on_book = ref in live_impl if ref.startswith("IMPL-") else ref in devs
        if not on_book:
            errors.append(f"crosscutting 引用了不在册的编号 {ref}（已撤销/迁移的编号不得再当依赖）")

# G-A 抽查：PRD 章节号存在性（形如 4.5.1 / 18.2#4 / 21.5）
prd = (ROOT.parent / "prd-homecube.md").read_text(encoding="utf-8")
secs = set(re.findall(r"^#{2,4}\s+([0-9]+(?:\.[0-9]+)*)", prd, re.M))
bad_sec = set()
for cid, _ in cases.items():
    pass
for f in files:
    for ln in f.read_text(encoding="utf-8").splitlines():
        if not ln.startswith("- 关联需求："):
            continue
        for s in re.findall(r"(?<![0-9.])(\d{1,2}\.\d{1,2}(?:\.\d{1,2})?)(?=[\s、，,）)#:：]|$)", ln):
            if s not in secs:
                bad_sec.add(s)
if bad_sec:
    errors.append(f"关联需求引用了 PRD 不存在的章节号：{sorted(bad_sec)}")

# G-A 续：接口路径必须在 PRD 中出现（未实现接口也应「PRD 有、代码无」，不能凭空造路径）
bad_path = {}
for f in files:
    rel = f.relative_to(ROOT).as_posix()
    for ln in f.read_text(encoding="utf-8").splitlines():
        if not ln.startswith("- 关联需求："):
            continue
        for p in re.findall(r"/api/[A-Za-z0-9_{}\-/]+", ln):
            p = p.rstrip("/")
            if p not in prd:
                bad_path.setdefault(p, set()).add(rel)
for p, fs in bad_path.items():
    errors.append(f"接口路径 `{p}` 在 PRD 中不存在（出现于 {sorted(fs)}）")

# G-A 续：引用的文件名必须真实存在（排除 node_modules/.git 与代码块内的伪路径）
SKIP = {"node_modules", ".git", ".workbuddy", "dist", "build"}
basenames = set()
for p in ROOT.parent.parent.rglob("*"):
    if any(part in SKIP for part in p.parts):
        continue
    if p.is_file():
        basenames.add(p.name)
ALLOW = {".md", ".go", ".sh", ".yaml", ".yml", ".json"}
for f in files:
    rel = f.relative_to(ROOT).as_posix()
    for ln in f.read_text(encoding="utf-8").splitlines():
        if not ln.startswith("- 关联需求："):
            continue
        for name in re.findall(r"[A-Za-z0-9_.\-/]+\.(?:md|go|sh|yaml|yml|json)", ln):
            if name.split("/")[-1] not in basenames:
                errors.append(f"{rel} 引用了不存在的文件 `{name}`")

# G-E 需求覆盖率门禁：PRD 一期接口必须全部有 case 承接（调用生成器 --check）
import subprocess
gen = ROOT / "gen_traceability.py"
if gen.exists():
    r = subprocess.run([sys.executable, str(gen), "--check"], capture_output=True, text=True)
    if r.returncode != 0:
        for ln in (r.stdout or "").strip().splitlines():
            if ln.startswith("[FAIL]"):
                errors.append("G-E " + ln[7:].strip())
else:
    errors.append("缺 gen_traceability.py，无法校验需求覆盖率")

# G-E2 traceability.md 不得滞后于 case 变动（防止生成文件过期仍被当证据）
tr = ROOT / "traceability.md"
if not tr.exists():
    errors.append("缺 traceability.md（需求覆盖率判据缺失）")
else:
    m = re.search(r"覆盖 case (\d+) 条", tr.read_text(encoding="utf-8"))
    if not m:
        errors.append("traceability.md 未找到「覆盖 case N 条」统计")
    elif int(m.group(1)) != total:
        errors.append(f"traceability.md 记 {m.group(1)} 条 case，实测 {total}（请重跑 gen_traceability.py）")

# G-E3 BACKLOG.md 引用的 case ID 必须存在
bl = ROOT / "BACKLOG.md"
if not bl.exists():
    errors.append("缺 BACKLOG.md（反向开发清单缺失）")
else:
    ghost = sorted(set(re.findall(r"(?:HOM|FIN)-[A-Z]+-\d{3}", bl.read_text(encoding="utf-8"))) - set(cases))
    if ghost:
        errors.append(f"BACKLOG.md 引用了不存在的 case ID：{ghost}")

# G-F 需求点覆盖门禁：每个 PRD 需求点必须有 case 承接，且每条 case 必须挂到需求点
greq = ROOT / "gen_requirements.py"
if greq.exists():
    r = subprocess.run([sys.executable, str(greq), "--check"], capture_output=True, text=True)
    if r.returncode != 0:
        for ln in (r.stdout or "").strip().splitlines():
            if ln.startswith("  - "):
                errors.append("G-F " + ln[4:].strip())
else:
    errors.append("缺 gen_requirements.py，无法校验需求点覆盖")

# G-F2 requirements.md 不得滞后于 case 变动
rq = ROOT / "requirements.md"
if not rq.exists():
    errors.append("缺 requirements.md（需求点基线缺失）")
else:
    m = re.search(r"模块 case 总数 (\d+) 条", rq.read_text(encoding="utf-8"))
    if not m:
        errors.append("requirements.md 未找到「模块 case 总数 N 条」统计")
    elif int(m.group(1)) != total:
        errors.append(f"requirements.md 记 {m.group(1)} 条 case，实测 {total}（请重跑 gen_requirements.py）")

# G-F3 index 目录索引必须收录 requirements.md 与生成器，避免基线文件被遗忘
for must in ["`requirements.md`", "`gen_requirements.py`"]:
    if must not in idx:
        errors.append(f"index.md 未登记 {must}")

# 输出
print(f"扫描 {len(files)} 个模块文件，{total} 条 case")
print("实现现状（快照）：" + " / ".join(f"{k} {v}" for k, v in Counter(c['auto'] for c in cases.values()).most_common()))
if devs:
    print("偏差登记：" + " / ".join(f"{d} {st}" for d, st in sorted(devs.items())))
if VERBOSE:
    print("Sprint 分布：" + " / ".join(f"{k} {v}" for k, v in Counter(c['sprint'] for c in cases.values()).most_common()))
    print("类型分布：" + " / ".join(f"{k} {v}" for k, v in Counter(c['type'] for c in cases.values()).most_common()))

if warns:
    print(f"\n⚠ {len(warns)} 项提醒（不阻断）：")
    for w in warns:
        print("  - " + w)

if errors:
    print(f"\n✗ {len(errors)} 项未通过：")
    for e in errors:
        print("  - " + e)
    sys.exit(1)
print("\n✓ G-A 引用真实性 / G-B Sprint 依据 / G-C 实现现状证据 / G-D 结构一致性 / G-E 接口与编号覆盖 / G-F 需求点覆盖 / G-G4 偏差闭环 全部通过")
sys.exit(0)
PY
