#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
check_anchors.py —— PRD 锚点漂移检查器（只读：不写文件、不改文件、不跑 git 写操作）

用途
----
本仓库唯一权威源是 `docs/prd-homecube.md`；其余文档一律以 `章:行号`（如 `16.3:2038`）
作为证据锚点引用它。PRD 一旦插行，被插点之下的所有行号整体位移，锚点就会「悄悄漂」到
别的章节里去。加行之后跑一遍本脚本即可抓出来：

    python3 docs/cases/check_anchors.py                 # 全量扫描，有漂移退出码 1
    python3 docs/cases/check_anchors.py --fix-report    # 另外打印每条命中的建议落点
    python3 docs/cases/check_anchors.py --advisory      # 另外打印「可精化」提示（不计退出码）
    python3 docs/cases/check_anchors.py --list-whitelist   # 只看白名单与时效声明校验（信息模式）

判定口径
--------
1) 由 PRD 的 `## / ### / ####` 标题序列建「行号 → 所属章号」映射；
2) 抓文档里的 `章:行号` 锚点，四种书写形态全部校验：
   - 基本形态   `16.3:2038`
   - 区间/并列  `21.2:2535-2538`、`4.8:704/716/721`（每个数字各自判定）
   - 行号索引   `19.2#4:2353`、`15.5#9:1928`、`19.2#12③:2361`
                既判章，又判「该行首列编号 == #N」，不等即算漂移（同一章里指错一行同样是漂）
   - 带词述     `19.2 P1 末附加门禁:2369`（章号与冒号之间允许 ≤3 个短词；
                词述里不允许出现括号——`0.2 裁决记录（需求方 04:06 定）` 这类是版本号 + 注文，
                不是锚点，允许括号会把它们误判成命中）
3) 严重度分级：
   - 「漂移」= 锚点行不属于所引章，或 `#N` 行号索引与该行编号不符 → 计入退出码；
   - 「可精化」= 锚点落在空行或表格分隔行（`|---|`）→ 默认不打印、`--advisory` 才打印，
     且**永不计入退出码**。理由：生成表 `requirements.md` 的整表锚点存在系统性 -1（构造期
     就落在内容行的上一行），那是「整表重基线」的另一件事，混进本脚本会让门禁天天红、
     把真正的跨章漂移淹没掉。

白名单（WHITELIST）准入标准
--------------------------
三条同时满足才允许加入，不满足即按普通文件照常判定：
  A. 文件是**时点快照类**文档（审计报告、复盘、偏差登记等）：它的锚点表达的是「当时核对到
     的是哪一行」，就地改写会破坏审计记录本身；
  B. 文件开头（前 20 行内）**自带时效声明**——出现「基准源」或「时效」字样并带基线时的
     PRD 行数或日期。这一条由脚本机器校验（`check_declaration`），声明缺失则白名单条目
     自动失效、照常报错，防止白名单本身腐烂成"永远绿灯"；
  C. 条目后必须写明准入理由（哪份快照、声明在第几行）。
白名单只免除「章不匹配」的退出码，文件仍被扫描并打印命中数——命中看得见，只是不拦。
"""

import argparse
import glob
import os
import re

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..'))
PRD_REL = 'docs/prd-homecube.md'
PRD_PATH = os.path.join(ROOT, PRD_REL)

# 扫描范围：case 文档与生成物 + v2 屏卡 + p1 技术方案（PRD 自身除外）。
GLOBS = ['docs/cases/**/*.md', 'docs/cases/*.py', 'docs/v2/**/*.md', 'docs/p1-*.md']

# 白名单：时点快照类文档（准入标准见文件头 A/B/C）。
WHITELIST = {
    # 审计快照：第 3 行「> 基准源：`prd-homecube.md`（2422 行）…」即其时效声明。
    'docs/cases/AUDIT-REPORT.md',
}

HEADING_RE = re.compile(r'^#{2,4} (\d+(?:\.\d+){0,2})')
DECL_RE = re.compile(r'基准源|时效')
BLANK_RE = re.compile(r'^\s*$')
TABLE_SEP_RE = re.compile(r'^\|\s*:?-{2,}\s*(\|\s*:?-{2,}\s*)+\|?\s*$')
ROW_NUM_RE = re.compile(r'^\|\s*(\d+)\s*\|')
TAIL_NUM_RE = re.compile(r'\d{2,4}')

# 章号 [#行号索引 [圈码]] [词述] : 行号 [区间/并列]
ANCHOR_RE = re.compile(
    r'(?<![\d.])'
    r'(\d{1,2}\.\d(?:\.\d)?)'                 # 1 章号
    r'(?:#(\d+)([\u2460-\u2477])?)?'          # 2 行号索引 N，3 可选圈码
    r'((?: [^\s:：`|、（）()「」『』〈〉《》【】“”‘\'"]{1,14}){0,3})?'  # 4 可选词述：不含冒号/反引号/顿号/括号（括号多为注文，见下）
    r':'
    r'(\d{2,4})'                              # 5 起始行号
    r'((?:\s*[-/–]\s*\d{2,4})*)'              # 6 可选区间 / 并列行号
)


def load_prd():
    lines = open(PRD_PATH, encoding='utf-8').read().split('\n')
    owner, cur = {}, None
    for i, line in enumerate(lines, 1):
        m = HEADING_RE.match(line)
        if m:
            cur = m.group(1)
        owner[i] = cur
    return lines, owner


PRD_LINES, PRD_OWNER = load_prd()


def rel(path):
    return os.path.relpath(path, ROOT)


def iter_files():
    seen, out = set(), []
    for pat in GLOBS:
        for p in sorted(glob.glob(os.path.join(ROOT, pat), recursive=True)):
            if not os.path.isfile(p) or os.path.basename(p) == 'prd-homecube.md':
                continue
            rp = rel(p)
            if rp in seen:
                continue
            seen.add(rp)
            out.append((rp, p))
    return out


def section_span(sec):
    nums = [n for n, s in PRD_OWNER.items() if s == sec]
    return (min(nums), max(nums)) if nums else (None, None)


def check_declaration():
    """白名单准入标准 B 的机器校验；返回失效条目。"""
    invalid = []
    for rp in sorted(WHITELIST):
        path = os.path.join(ROOT, rp)
        if not os.path.isfile(path):
            invalid.append((rp, '文件不存在'))
            continue
        head = open(path, encoding='utf-8').read().split('\n')[:20]
        if not any(DECL_RE.search(l) for l in head):
            invalid.append((rp, '前 20 行内没有「基准源/时效」声明，白名单条目失效'))
    return invalid


def row_index_check(sec, row, line_no):
    """`#N` 行号索引：该行必须是编号表行且编号等于 N。"""
    text = PRD_LINES[line_no - 1] if 1 <= line_no <= len(PRD_LINES) else ''
    m = ROW_NUM_RE.match(text.strip())
    if not m:
        return f'{sec}#{row} 指向的第 {line_no} 行不是编号表行'
    if m.group(1) != row:
        return f'行号索引不符：第 {line_no} 行是 {sec} 的第 {m.group(1)} 行，不是第 {row} 行'
    return None


def scan(advisory=False):
    drift, notes, wl_hits = [], [], []
    for rp, path in iter_files():
        whitelisted = rp in WHITELIST
        for ln, line in enumerate(open(path, encoding='utf-8'), 1):
            for m in ANCHOR_RE.finditer(line):
                sec, row, _, _, start, tail = m.groups()
                shown = m.group(0)
                targets = [int(start)] + [int(x) for x in TAIL_NUM_RE.findall(tail or '')]
                bad = next((t for t in targets if PRD_OWNER.get(t) != sec), None)
                if bad is not None:
                    item = (rp, ln, shown, sec, bad, PRD_OWNER.get(bad))
                    (wl_hits if whitelisted else drift).append(item)
                    continue
                if row:
                    why = row_index_check(sec, row, int(start))
                    if why:
                        drift.append((rp, ln, shown, f'{sec}#{row}', int(start), why))
                if advisory:
                    text = PRD_LINES[int(start) - 1] if int(start) <= len(PRD_LINES) else ''
                    if BLANK_RE.match(text):
                        notes.append((rp, ln, shown, '锚点落在空行，可精化到具体内容行'))
                    elif TABLE_SEP_RE.match(text.strip()):
                        notes.append((rp, ln, shown, '锚点落在表格分隔行，可精化到具体表行'))
    return drift, notes, wl_hits


def print_fix_report(drift):
    print('\n—— 建议落点（--fix-report；只读，不写文件，逐条人工核对后再改）——')
    for rp, ln, shown, sec, bad, actual in drift:
        lo, hi = section_span(sec)
        hint = ''
        if lo:
            cands = []
            for n in range(lo, hi + 1):
                t = PRD_LINES[n - 1].strip()
                if t and not BLANK_RE.match(t) and not TABLE_SEP_RE.match(t):
                    cands.append(f'{n} {t[:30]}')
                if len(cands) == 3:
                    break
            hint = f'｜本章 {sec} 正文行 {lo}-{hi}，起首几行：' + ' ／ '.join(cands)
        print(f'{rp}:{ln}  {shown}  实际落在 {actual}  {hint}')


def main():
    ap = argparse.ArgumentParser(description='PRD 锚点漂移检查（只读）')
    ap.add_argument('--fix-report', action='store_true', help='为每条漂移打印建议落点')
    ap.add_argument('--advisory', action='store_true', help='额外打印「可精化」提示（不计入退出码）')
    ap.add_argument('--list-whitelist', action='store_true', help='打印白名单及其时效声明校验结果')
    args = ap.parse_args()

    if args.list_whitelist:
        print('—— 白名单（仍扫描、仍打印命中，只是不计入退出码；准入标准 A/B/C 见文件头）——')
        for rp in sorted(WHITELIST):
            path = os.path.join(ROOT, rp)
            if not os.path.isfile(path):
                print(f'  {rp}  [失效] 文件不存在')
                continue
            head = open(path, encoding='utf-8').read().split('\n')[:20]
            decl = next((f'{i + 1}: {l.strip()[:72]}' for i, l in enumerate(head) if DECL_RE.search(l)), None)
            print(f'  {rp}  时效声明→ {decl or "（前 20 行未找到，条目失效）"}')
        invalid = check_declaration()
        for rp, why in invalid:
            print(f'白名单条目失效：{rp} — {why}')
        print(f'（--list-whitelist 为信息模式：不判定漂移，退出码 {1 if invalid else 0}）')
        return 1 if invalid else 0

    drift, notes, wl_hits = scan(advisory=args.advisory)
    files = iter_files()

    per_file = {}
    for rp, _ln, _shown, _sec, _bad, _actual in drift:
        per_file[rp] = per_file.get(rp, 0) + 1

    print(f'PRD {PRD_REL} 共 {len(PRD_LINES)} 行；扫描 {len(files)} 个文件（含生成物与本脚本）')
    print(f'不匹配锚点 {len(drift)} 处（章不匹配 / 行号索引不符）')
    for rp in sorted(per_file):
        print(f'  {rp}: {per_file[rp]}')
    for rp, ln, shown, _sec, _bad, actual in drift:
        print(f'{rp}:{ln}  引 {shown}  实际落在 {actual}')

    if wl_hits:
        print(f'白名单内命中 {len(wl_hits)} 处（不计入退出码，处置见各文件自身的时效声明）')
        for rp in sorted({h[0] for h in wl_hits}):
            print(f'  {rp}: {sum(1 for h in wl_hits if h[0] == rp)}')

    invalid = check_declaration()
    for rp, why in invalid:
        print(f'白名单条目失效：{rp} — {why}')

    if notes:
        print(f'可精化提示 {len(notes)} 处（不计入退出码）')
        for rp, ln, shown, why in notes:
            print(f'  {rp}:{ln}  {shown}  → {why}')
    elif args.advisory:
        print('可精化提示 0 处')

    if args.fix_report:
        print_fix_report(drift)

    return 1 if (drift or invalid) else 0


if __name__ == '__main__':
    raise SystemExit(main())
