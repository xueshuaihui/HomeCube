#!/usr/bin/env python3
"""证据体检：验证 docs/cases 里每条 case 的「实现现状（快照）」证据是否仍然成立。

这个脚本只回答一个问题：**基线里写的那些证据，今天还成不成立？**
它不改任何 case 文件，只出一份报告。

用法：
    python3 docs/cases/audit_baseline.py --audit
    python3 docs/cases/audit_baseline.py --only FIN-LED-003
    python3 docs/cases/audit_baseline.py --audit --json

四类证据与判定：
    A 文件:行号   → OK / GONE（文件不存在）/ OOR（行号越界）/ DRIFT（行内容不相关）
    B 零命中      → STALE（实际有命中，声明过时）/ WEAK（断言为真但检索形态不可靠）
    C 脚本/make   → OK / STALE（target 或脚本不存在）
    D 文件+行数   → OK / DRIFT（偏差 >20%）

退出码：0 无 STALE；1 有 STALE；2 解析失败。
"""

import argparse
import json
import re
import subprocess
import sys
from datetime import datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CASES = ROOT / "docs" / "cases"
MODULE_FILES = sorted(
    list((CASES / "homeos").glob("*.md")) + list((CASES / "finance").glob("*.md"))
)

# 状态值按长度降序，保证 `已实现(HTTP)` 不被 `已实现` 截断
STATUSES = [
    "已实现(HTTP)",
    "已实现(分层)",
    "契约偏差",
    "待自动化",
    "未实现",
    "纯手动",
    "引用脚本",
    "CI门禁",
]
IMPLEMENTED = {"已实现(HTTP)", "已实现(分层)"}

CASE_RE = re.compile(r"^## ((?:HOM|FIN)-[A-Z]+-\d{3}) · (.+)$")
STATUS_PREFIX = "- 实现现状（快照）："
EXEC_PREFIX = "- 执行状态："

# A. 文件:行号（允许 100-120 区间）
PATH_LINE = re.compile(r"([A-Za-z0-9_./\-]+\.[A-Za-z]{1,5}):(\d+)(?:-(\d+))?")
# B. 零命中断言
ZERO_WORDS = r"(?:零命中|无命中|零调用|无调用方|零调用方)"
ZERO_ASSERT = re.compile(r"`([^`]{2,80})`[^`]{0,60}?" + ZERO_WORDS)
BACKTICK = re.compile(r"`([^`]{1,120})`")
# C. 脚本
MAKE_TARGET = re.compile(r"\bmake\s+([a-z][a-z0-9\-]*)")
SH_SCRIPT = re.compile(r"((?:deploy/scripts|scripts)/[A-Za-z0-9_\-]+\.sh)")
# D. 文件 + 行数
LOC_CLAIM = re.compile(r"([A-Za-z0-9_./\-]+\.[A-Za-z]{1,5})\s+(\d+)\s*行")
# E. 接口（用于路由级交叉验证）
ENDPOINT = re.compile(r"\b(GET|POST|PUT|PATCH|DELETE)\s+(/api/[A-Za-z0-9_/{}\-]+)")
ROUTE_DEF = re.compile(r'([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*(GET|POST|PUT|PATCH|DELETE)\s*\(\s*"([^"]*)"')
GROUP_DEF = re.compile(r'([A-Za-z_][A-Za-z0-9_]*)\s*:=\s*[A-Za-z_][A-Za-z0-9_.]*\s*\.\s*Group\(\s*"([^"]*)"')

SKIP_FOR_IDENT = {"make", "server", "docs", "true", "false", "nil", "err"}


class Case:
    __slots__ = ("cid", "title", "module", "lineno", "status", "evidence")

    def __init__(self, cid, title, module, lineno, status, evidence):
        self.cid = cid
        self.title = title
        self.module = module
        self.lineno = lineno
        self.status = status
        self.evidence = evidence

    def brief(self, n=60):
        t = self.title if len(self.title) <= n else self.title[: n - 1] + "…"
        return f"{self.module}:{self.lineno} · {t}"


def parse_status(rest):
    """从「- 实现现状（快照）：」之后的内容里拆出状态与证据。"""
    for s in STATUSES:
        if rest.startswith(s):
            tail = rest[len(s):].strip()
            return s, tail
    # 未匹配到枚举：按第一个中文左括号切
    idx = rest.find("（")
    if idx > 0:
        return rest[:idx].strip(), rest[idx:].strip()
    return rest.strip(), ""


def strip_paren(text):
    """去掉包裹证据的中文括号。"""
    t = text.strip()
    if t.startswith("（") and t.endswith("）"):
        return t[1:-1]
    return t


def load_cases():
    out = []
    for f in MODULE_FILES:
        rel = f"{f.parent.name}/{f.name}"
        lines = f.read_text(encoding="utf-8").splitlines()
        cur = None
        for i, line in enumerate(lines, 1):
            m = CASE_RE.match(line)
            if m:
                if cur:
                    out.append(cur)
                cur = Case(m.group(1), m.group(2), rel, i, None, "")
                continue
            if cur and cur.status is None and line.startswith(STATUS_PREFIX):
                status, tail = parse_status(line[len(STATUS_PREFIX):])
                cur.status = status
                cur.evidence = strip_paren(tail)
        if cur:
            out.append(cur)
    return out


class FileCache:
    def __init__(self):
        self._lines = {}

    def get(self, path: Path):
        if path not in self._lines:
            try:
                self._lines[path] = path.read_text(encoding="utf-8", errors="replace").splitlines()
            except OSError:
                self._lines[path] = None
        return self._lines[path]


CACHE = FileCache()
INDEX = None


def resolve(rel: str):
    """把证据里的路径解析成真实文件。支持 `handler/auth.go` 这类简写。"""
    global INDEX
    p = (ROOT / rel).resolve()
    if p.exists() and p.is_file():
        return p
    # 简写：按文件名在常用目录下找
    if INDEX is None:
        INDEX = {}
        for d in ("server", "web", "admin", "deploy", "scripts", "docs", ".github"):
            base = ROOT / d
            if not base.exists():
                continue
            for f in base.rglob("*"):
                if f.is_file():
                    INDEX.setdefault(f.name, []).append(f)
    name = Path(rel).name
    hits = INDEX.get(name, [])
    if len(hits) == 1:
        return hits[0]
    # 同名多个时，用路径后缀匹配
    for h in hits:
        if str(h).endswith(rel):
            return h
    return None


def identifiers(evidence: str):
    """从证据里的反引号内容提取可校验的标识符（函数名/字段名/路由）。"""
    ids = []
    for token in BACKTICK.findall(evidence):
        token = token.strip()
        if not token or token in SKIP_FOR_IDENT:
            continue
        if re.search(r"\.(go|py|sh|md|sql|vue|ts|js|json|yaml|yml)$", token):
            continue  # 纯文件名，不做标识符
        if ":" in token and re.search(r":\d", token):
            continue  # 已是 path:line
        if re.fullmatch(r"[A-Za-z_][A-Za-z0-9_.]{2,}", token):
            ids.append(token)
        elif token.startswith("/") and len(token) > 2:
            ids.append(token)
    return ids


def path_tail(p: str, n: int = 2) -> str:
    """取路径末 n 段，用于「全路径 vs 组内相对注册」的比较。

    case 里写的是完整路径 `/api/homeos/family/switch`，而代码里注册的是
    `group.Group("/api/homeos")` + `protected.POST("/family/switch")`——
    字面不同、语义同一，只比全串会稳定误报。
    """
    parts = [x for x in p.strip("/").split("/") if x]
    return "/".join(parts[-n:])


SHORT_LINE = re.compile(r"(?<![A-Za-z0-9_.:])[:：](\d{2,4})(?!\d)")


def collect_pointers(evidence: str):
    """把证据里的代码指针按出现顺序收齐，含短写形式（`:1716`）。

    仓库里的证据写法既有 `repo/finance_test.go:1565`，也有承接上一个文件名写
    `、:1716` / `:1995`。只认全写形式时，短写指针的标识符会被错算到前一处头上，
    产生「A 处标识符拿去证明 B 处」的假 DRIFT。
    """
    ptrs = []          # (pos, end, rel, line)
    full = []          # 全写指针，供短写指针按位置就近归属文件名
    for m in PATH_LINE.finditer(evidence):
        full.append((m.start(), m.group(1)))
        ptrs.append((m.start(), m.end(), m.group(1), int(m.group(2))))
    for m in SHORT_LINE.finditer(evidence):
        # 落在某个全写指针内部（行号部分）就跳过
        if any(s <= m.start() < e for s, e, _, _ in ptrs):
            continue
        # 归属「位置在该短写之前、且最近」的那个全写指针的文件——
        # 一条证据常跨两个文件（`a.go:10`、`:20`，`b.go:30`、`:40`），
        # 只记全局 last_rel 会把 a 的行号错算到 b 头上。
        owner = None
        for pos, rel in full:
            if pos < m.start():
                owner = rel
            else:
                break
        if owner is None:
            continue
        ptrs.append((m.start(), m.end(), owner, int(m.group(1))))
    ptrs.sort()
    return ptrs


def check_path_line(case, findings):
    # 只有「声称已实现」的条目才要求「该行附近能找到声明的标识符」。
    # 对「未实现/待自动化」类，证据里的标识符恰恰是「不该存在」的那个，
    # 拿它做相关性检查会稳定误报。
    require_ident = case.status in IMPLEMENTED or case.status == "契约偏差"
    ptrs = collect_pointers(case.evidence)
    seen = set()
    for idx, (pos, end, rel, start) in enumerate(ptrs):
        if (rel, start) in seen:
            continue
        seen.add((rel, start))
        if rel.endswith(".md"):
            # 证据行里常引 `traceability.md:15` 之类的文档对照，那不是代码指针，
            # 对它做「附近有无标识符」会稳定误报成 DRIFT。
            continue
        # 标识符只取「前后相邻两个指针之间」那一段（双向窗口）：一条证据常引多处
        # `文件:行号`，用整条证据的标识符去验任一处，会把 A 处的标识符拿去证明 B 处。
        # 只向前取会把「`TestFoo` 见 `file:line`」这种写法漏掉，所以两侧都收。
        seg_start = ptrs[idx - 1][1] if idx > 0 else 0
        seg_end = ptrs[idx + 1][0] if idx + 1 < len(ptrs) else len(case.evidence)
        ids = identifiers(case.evidence[seg_start:seg_end])
        p = resolve(rel)
        if p is None:
            findings.append(("GONE", rel, f"{rel} 不存在"))
            continue
        lines = CACHE.get(p)
        if lines is None:
            findings.append(("GONE", rel, f"{rel} 不可读"))
            continue
        if start > len(lines):
            findings.append(("OOR", rel, f"{rel}:{start} 越界（文件仅 {len(lines)} 行）"))
            continue
        if not require_ident:
            continue
        # 窗口 ±12 行：证据常指向测试函数的起手行，标识符出现在函数体内。
        # `文件:1` 是「整个文件」的习惯写法，此时放开到全文件。
        if start <= 2:
            window = "\n".join(lines)
        else:
            window = "\n".join(lines[max(0, start - 12): start + 12])
        if not ids:
            continue
        # 路径类标识符（/healthz、/api/homeos/family/switch）在代码里常写成相对注册
        # 或去掉前导斜杠：既比全串，也比末两段。
        if any(i in window
               or (i.startswith("/") and (i.lstrip("/") in window or path_tail(i) in window))
               for i in ids):
            continue
        findings.append(
            ("DRIFT", rel, f"{rel}:{start} 附近未见 {ids[:2]}（实际行：{lines[start-1].strip()[:70]}）")
        )


def grep_hits(word: str, scope: Path):
    """返回命中文件数（排除 docs/cases 与 .git）。"""
    try:
        r = subprocess.run(
            ["grep", "-rIl", "--exclude-dir=.git", "--exclude-dir=node_modules",
             "--exclude-dir=dist", "--", word, str(scope)],
            capture_output=True, text=True, timeout=30,
        )
        files = [x for x in r.stdout.splitlines() if "/docs/cases/" not in x]
        return files
    except (subprocess.TimeoutExpired, OSError):
        return []


def check_zero_hit(case, findings):
    for m in ZERO_ASSERT.finditer(case.evidence):
        word = m.group(1).strip()
        # 纯目录名（server/、web/）不是检索词；过短的词（sms）会误命中无关内容
        if not word or word.endswith("/") or len(word) < 4:
            continue
        # 决定检索范围：证据里出现的目录优先
        scope = ROOT / "server"
        for cand in ("server", "web", "admin", "deploy"):
            if re.search(rf"\b{cand}/", case.evidence):
                scope = ROOT / cand
                break
        hits = grep_hits(word, scope)
        if hits:
            # 不管当前标什么状态，「声称零命中但实际有命中」都说明这条声明过时了
            rel_hits = [str(Path(h).relative_to(ROOT)) for h in hits[:3]]
            findings.append(
                ("STALE", word, f"声明「{word} 零命中」，实际命中 {len(hits)} 个文件：{', '.join(rel_hits)}")
            )
        elif case.status in ("未实现", "待自动化"):
            # 零命中为真，正好支持「未实现」的声明，不算问题
            continue
        else:
            findings.append(
                ("WEAK", word, f"标 {case.status} 却出现「{word} 零命中」的表述，自相矛盾，需人看")
            )


def check_scripts(case, findings):
    for m in MAKE_TARGET.finditer(case.evidence):
        tgt = m.group(1)
        mk = ROOT / "Makefile"
        if not mk.exists():
            findings.append(("GONE", "Makefile", "Makefile 不存在"))
            continue
        txt = mk.read_text(encoding="utf-8", errors="replace")
        if not re.search(rf"^{re.escape(tgt)}\s*:", txt, re.M):
            findings.append(("STALE", f"make {tgt}", f"Makefile 无 target `{tgt}`"))
    for m in SH_SCRIPT.finditer(case.evidence):
        rel = m.group(1)
        if not (ROOT / rel).exists():
            findings.append(("STALE", rel, f"脚本 {rel} 不存在"))


def load_routes():
    """解析两个 main.go 实际注册的路由，得到 {(svc, METHOD, 归一化路径)}。"""
    svcs = (
        ("homeos", ROOT / "server/services/svc-homeos/cmd/svc-homeos/main.go"),
        ("finance", ROOT / "server/services/svc-finance/cmd/svc-finance/main.go"),
    )
    routes = set()
    for svc, f in svcs:
        if not f.exists():
            continue
        txt = f.read_text(encoding="utf-8", errors="replace")
        groups = {m.group(1): m.group(2) for m in GROUP_DEF.finditer(txt)}
        for m in ROUTE_DEF.finditer(txt):
            var, meth, path = m.group(1), m.group(2), m.group(3)
            prefix = groups.get(var, "")
            full = "/" + "/".join(p for p in (prefix, path) if p)
            full = re.sub(r"/{2,}", "/", full).rstrip("/") or "/"
            routes.add((svc, meth, norm_path(full)))
    return routes


def norm_path(p: str) -> str:
    p = re.sub(r"^/api/(?:homeos|finance)", "", p)
    p = re.sub(r":\w+|\{[^}]+\}", "{id}", p)
    return p.rstrip("/") or "/"


def singular_toggle(p: str):
    """把路径里的复数段换成单数（用于识别 /family/ vs /families/ 这类偏差）。"""
    parts = []
    for seg in p.split("/"):
        if seg.endswith("ies"):
            parts.append(seg[:-3] + "y")
        elif seg.endswith("s") and seg not in ("", "status"):
            parts.append(seg[:-1])
        else:
            parts.append(seg)
    return "/".join(parts)


def check_endpoint(case, findings, routes):
    """路由级交叉验证：case 声明的接口在代码里到底有没有。"""
    text = f"{case.title} {case.evidence}"
    seen = set()
    for m in ENDPOINT.finditer(text):
        meth, raw = m.group(1), m.group(2)
        if "/api/homeos" in raw:
            svc = "homeos"
        elif "/api/finance" in raw:
            svc = "finance"
        else:
            continue
        path = norm_path(raw)
        key = (svc, meth, path)
        if key in seen:
            continue
        seen.add(key)

        exact = (svc, meth, path) in routes
        near = [p for s, mm, p in routes if s == svc and mm == meth and singular_toggle(p) == singular_toggle(path)]

        if case.status in ("未实现", "待自动化"):
            if exact:
                findings.append(
                    ("STALE", f"{meth} {path}", f"标「{case.status}」，但路由已注册（{meth} {path}）")
                )
            elif near:
                findings.append(
                    ("DRIFT", f"{meth} {path}", f"标「{case.status}」，存在近似路由 {meth} {near[0]}（疑为单复数/前缀偏差）")
                )
        elif case.status in IMPLEMENTED:
            if not exact and not near:
                findings.append(
                    ("STALE", f"{meth} {path}", f"标 {case.status}，但路由表无此接口")
                )


def check_loc(case, findings):
    for m in LOC_CLAIM.finditer(case.evidence):
        rel, claimed = m.group(1), int(m.group(2))
        p = resolve(rel)
        if p is None:
            continue
        lines = CACHE.get(p)
        if not lines:
            continue
        actual = len(lines)
        if actual == 0:
            continue
        if abs(actual - claimed) / actual > 0.20:
            findings.append(
                ("DRIFT", rel, f"声明 {claimed} 行，实际 {actual} 行（偏差 {abs(actual-claimed)/actual:.0%}）")
            )


def classify(findings):
    """把单条 case 的多个发现归并成一个判定。"""
    if not findings:
        return "OK", []
    kinds = {k for k, _, _ in findings}
    if "STALE" in kinds or "GONE" in kinds:
        return "STALE", findings
    if "OOR" in kinds or "DRIFT" in kinds:
        return "DRIFT", findings
    return "WEAK", findings


def audit(cases):
    routes = load_routes()
    results = []
    for c in cases:
        f = []
        check_endpoint(c, f, routes)
        if c.evidence:
            check_path_line(c, f)
            check_zero_hit(c, f)
            check_scripts(c, f)
            check_loc(c, f)
        verdict, fs = classify(f)
        results.append((c, verdict, fs))
    return results


def report(results, stamp):
    counts = {"OK": 0, "STALE": 0, "DRIFT": 0, "WEAK": 0}
    for _, v, _ in results:
        counts[v] = counts.get(v, 0) + 1
    out = []
    out.append(f"# 证据体检报告 · {stamp}")
    out.append("")
    out.append(f"> 扫描 {len(MODULE_FILES)} 个模块文件，{len(results)} 条 case。**只读**，不修改任何 case 文件。")
    out.append("")
    out.append("## 汇总")
    out.append("")
    out.append("| 判定 | 含义 | 条数 |")
    out.append("|---|---|---:|")
    out.append(f"| OK | 证据指向的位置与内容都成立 | {counts['OK']} |")
    out.append(f"| **STALE** | **声明已失效，必修**（文件/脚本不存在、零命中断言为假） | **{counts['STALE']}** |")
    out.append(f"| DRIFT | 需人看（行号存活但内容不相关、行数偏差 >20%） | {counts['DRIFT']} |")
    out.append(f"| WEAK | 断言方式不可靠（零命中为真，但检索词形态可疑） | {counts['WEAK']} |")
    out.append("")

    for verdict, title, note in (
        ("STALE", "一、STALE · 声明已失效（必修）", "这些条目的证据所指向的东西已不存在，或「零命中」的断言已被事实推翻。"),
        ("DRIFT", "二、DRIFT · 需人看", "位置还在，但内容对不上；多见于代码被重构后行号漂移。"),
        ("WEAK", "三、WEAK · 断言方式不可靠", "断言本身为真，但检索词可能是拼接字符串的片段，容易把「已实现」误判成「未实现」。"),
    ):
        rows = [(c, fs) for c, v, fs in results if v == verdict]
        if not rows:
            continue
        out.append(f"## {title}")
        out.append("")
        out.append(f"> {note}")
        out.append("")
        out.append("| case | 状态 | 发现 |")
        out.append("|---|---|---|")
        for c, fs in rows:
            detail = "；".join(d for _, _, d in fs).replace("|", "\\|")
            out.append(f"| `{c.cid}` | {c.status} | {detail} |")
        out.append("")
    return "\n".join(out), counts


def main():
    ap = argparse.ArgumentParser(description="case 证据体检（只读）")
    ap.add_argument("--audit", action="store_true", help="跑全量体检并写报告")
    ap.add_argument("--only", metavar="CASE", help="只体检某条 case（前缀匹配）")
    ap.add_argument("--json", action="store_true", help="以 JSON 输出（不写 md 报告）")
    args = ap.parse_args()

    if not MODULE_FILES:
        print("未找到模块文件", file=sys.stderr)
        return 2

    cases = load_cases()
    if args.only:
        cases = [c for c in cases if c.cid.startswith(args.only.upper())]
        if not cases:
            print(f"未找到 case：{args.only}", file=sys.stderr)
            return 2

    results = audit(cases)
    stamp = datetime.now().strftime("%Y-%m-%d")

    if args.json:
        payload = [
            {
                "case": c.cid,
                "module": c.module,
                "line": c.lineno,
                "status": c.status,
                "verdict": v,
                "findings": [{"kind": k, "subject": s, "detail": d} for k, s, d in fs],
            }
            for c, v, fs in results
        ]
        print(json.dumps(payload, ensure_ascii=False, indent=2))
    else:
        text, counts = report(results, stamp)
        print(text)
        print()
        print(f"合计：OK {counts['OK']} / STALE {counts['STALE']} / DRIFT {counts['DRIFT']} / WEAK {counts['WEAK']}")
        if args.audit:
            rd = CASES / "reports"
            rd.mkdir(parents=True, exist_ok=True)
            fp = rd / f"baseline-audit-{stamp.replace('-', '')}.md"
            fp.write_text(text + "\n", encoding="utf-8")
            print(f"报告已写入 {fp.relative_to(ROOT)}")

    return 1 if any(v == "STALE" for _, v, _ in results) else 0


if __name__ == "__main__":
    sys.exit(main())
