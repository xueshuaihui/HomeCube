#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""重建 docs/cases/index.md 的目录索引、覆盖矩阵与统计（叙述段与 Gate 口径保留原样）。

用法：python3 docs/cases/gen_index.py
"""
import re
from collections import Counter, OrderedDict
from pathlib import Path

ROOT = Path(__file__).resolve().parent
HEAD = re.compile(r"^## ((?:HOM|FIN|CAL)-[A-Z]+-\d{3})\s*·\s*(.+)$", re.M)

ORDER = [
    "homeos/identity-family.md", "homeos/identity-member.md", "homeos/session.md",
    "homeos/auth.md", "homeos/architecture.md", "homeos/navigation.md",
    "homeos/collab.md", "homeos/notification.md", "homeos/home-summary.md",
    "homeos/search.md", "homeos/sync-data.md", "homeos/contracts.md",
    "homeos/data-lifecycle.md", "homeos/admin-ops.md", "homeos/app-config.md",
    "homeos/compliance.md",
    "finance/transaction.md", "finance/ledger.md", "finance/account.md",
    "finance/category.md", "finance/balance-stats.md", "finance/budget-bill.md",
    "finance/export-voice.md", "finance/loan-goal.md", "finance/split-asset.md",
    "finance/settings.md", "finance/demo.md",
    "calendar/time-collab.md", "calendar/due-center.md", "calendar/face-integration.md",
]

DESC = {
    "homeos/identity-family.md": ("HOM-FAM", "家庭 / 面配置 / 强制选面 / 解散冷存"),
    "homeos/identity-member.md": ("HOM-MEM", "成员 / 无账号代管 / 邀请四态 / 角色 / 访客 / 成员退出"),
    "homeos/session.md": ("HOM-SESS", "验证码 / 登录 / refresh / 切换重签 / 会话记录"),
    "homeos/auth.md": ("HOM-AUTH", "SDK / 50 格矩阵 / 字段级可见性 / 设备锁 / L2·L3 加密"),
    "homeos/navigation.md": ("HOM-NAV", "顶栏与底部五项 / 切面 3 步 / 路由与破坏性弹层 / 「＋」/ base URL"),
    "homeos/architecture.md": ("HOM-ARCH", "系统边界（不做/不存/不定规则）/ 分层 / 路由前缀 / claims / 事件总线七要素 / 全库兜底"),
    "homeos/collab.md": ("HOM-TIME", "底座协作三类：留言 / 投票 / 动态（时间语义 11 条已迁 `calendar/time-collab.md`）"),
    "homeos/notification.md": ("HOM-NOTI", "三分型 / 99+ / 同源同清 / 去重 / 免打扰 / @"),
    "homeos/home-summary.md": ("HOM-SUM", "首页四区聚合 / 前 3 条 / faces 四层 / 0 格态"),
    "homeos/search.md": ("HOM-SRCH", "关键词搜索 / 分组 / 时延 / 深链"),
    "homeos/sync-data.md": ("HOM-SYNC", "delta / 四类冲突 / 事件总线 / 幂等 / 死信 / 备份 / 事件契约"),
    "homeos/contracts.md": ("HOM-CT", "归属唯一可写方 / 跨面三方式 / fan-out ≤1 / 费用映射"),
    "homeos/data-lifecycle.md": ("HOM-DATA", "导出四格式 / 迁移 / 回滚 / 恢复顺序 / 保留期 / 图片分档"),
    "homeos/admin-ops.md": ("HOM-ADM", "审计查询 / 对账 / 备份状态 / 恢复演练 / 事件口 / 告警"),
    "homeos/app-config.md": ("HOM-APP", "活配置 / 多语言时区 / 主题三态 / 版本检查 / PIN / 首页态 / 客户端性能"),
    "homeos/compliance.md": ("HOM-CMP", "隐私政策与同意留痕 / 埋点字典 / 管理台二次认证 / 指标双维度"),
    "finance/transaction.md": ("FIN-TX", "记账 / 幂等 / 分页 / 回收站 / 私密行 / 标签 / 模板 / 金额精度 / 批量"),
    "finance/ledger.md": ("FIN-LED", "账本四态 / 归集过滤 / 非权限边界"),
    "finance/account.md": ("FIN-ACC", "账户 / 默认位 / 余额派生 / 归档与恢复 / 信用与负债"),
    "finance/category.md": ("FIN-CAT", "两级分类 / 停用 / 排序 / 预置不可删 / 跨面字典"),
    "finance/balance-stats.md": ("FIN-BAL", "余额 / 四视图 / period 三档 / 环比 / 累计 / 报表周期"),
    "finance/budget-bill.md": ("FIN-BUD", "预算（总/分类/成员）/ 周期归零 / 超支 / 账单状态机 / 逾期"),
    "finance/export-voice.md": ("FIN-EXP", "导出三参数与全员 / OCR / 语音 / 附件 / 图片导出"),
    "finance/loan-goal.md": ("FIN-LOAN", "借贷 / 还款计划 / 目标 / 自动存入"),
    "finance/split-asset.md": ("FIN-SPLIT", "AA 分账 / 结算 / 多币种 / 发票 / 净资产 / 报销"),
    "finance/settings.md": ("FIN-SET", "财务设置：默认值 / 部分更新 / 校验 / 活配置"),
    "finance/demo.md": ("FIN-DEMO", "跨服务五步 demo 的建与删（S19 零残留）"),
    "calendar/time-collab.md": ("CAL-TIME", "到期中心 / 待办 / 提醒 / 周期任务 / 日历多视图 / 到期注册与完成回写"),
    "calendar/due-center.md": ("CAL-DUE", "19.4#2-#5 判据 / 19 行注册表 / 不注册清单 / 触发出口 / 到达率两口径"),
    "calendar/face-integration.md": ("CAL-FACE", "19.4#1/#6/#7 / 分包按需加载 / 必选面停用被拒 / 日历行五格 / 命名域门禁 / 五步"),
}


def parse():
    files = []
    for rel in ORDER:
        p = ROOT / rel
        if p.exists():
            files.append(p)
    # 兜底：ORDER 之外的文件也要进表，避免新增文件被漏掉
    for d in ("homeos", "finance", "calendar"):
        for p in sorted((ROOT / d).glob("*.md")):
            if p not in files:
                files.append(p)

    data = OrderedDict()
    for p in files:
        rel = p.relative_to(ROOT).as_posix()
        txt = p.read_text(encoding="utf-8")
        items = []
        for m in HEAD.finditer(txt):
            cid, title = m.group(1), m.group(2).strip()
            block = txt[m.end():]
            nxt = HEAD.search(block)
            body = block[:nxt.start()] if nxt else block
            t = re.search(r"- 类型：(\S+)", body)
            pr = re.search(r"优先级：(P\d)", body)
            sp = re.search(r"建议归属 Sprint：(.+)$", body, re.M)
            au = re.search(r"^- 实现现状（快照）：(.+)$", body, re.M)
            ex = re.search(r"^- 执行状态：(\S+)", body, re.M)
            auto_raw = au.group(1).strip() if au else ""
            token = next((k for k in ["已实现(HTTP)", "已实现(分层)", "契约偏差", "待自动化",
                                      "未实现", "纯手动", "引用脚本", "CI门禁"]
                          if auto_raw.startswith(k)), "?")
            items.append({
                "id": cid, "title": title, "type": t.group(1) if t else "?",
                "pri": pr.group(1) if pr else "?", "sprint": sp.group(1).strip() if sp else "?",
                "auto": token, "exec": (ex.group(1) if ex else "?"),
            })
        data[rel] = items
    return data


def main():
    data = parse()
    all_cases = [c for items in data.values() for c in items]
    total = len(all_cases)

    # crosscutting 验收映射条数：从文件实测，别手写（曾写死 24，19.4 落位后是 31）
    cc_dir = ROOT / "crosscutting"
    cc_rows = 0
    if cc_dir.exists():
        for p in sorted(cc_dir.glob("*.md")):
            cc_rows += len(re.findall(r"^\| \d+ \|", p.read_text(encoding="utf-8"), re.M))

    idx_path = ROOT / "index.md"
    old = idx_path.read_text(encoding="utf-8")

    head_end = old.index("## 目录索引")
    tail_start = old.index("## 审核口径（每轮按此判定）")
    head, tail = old[:head_end], old[tail_start:]

    type_c = Counter(c["type"] for c in all_cases)
    pri_c = Counter(c["pri"] for c in all_cases)
    auto_c = Counter(c["auto"] for c in all_cases)
    sprint_c = Counter(c["sprint"] for c in all_cases)
    dom_c = Counter(k.split("/")[0] for k in data for _ in data[k])
    impl = auto_c["已实现(HTTP)"] + auto_c["已实现(分层)"]
    m2_keys = {"S15-S16", "S17-S18", "未点名(P1-M2)"}
    m2 = sum(v for k, v in sprint_c.items() if k in m2_keys)
    m1 = total - m2

    # 需求点数量（供头部两维度表引用）
    req_md = ROOT / "requirements.md"
    req_total = 0
    if req_md.exists():
        m = re.search(r"需求点 (\d+) 条", req_md.read_text(encoding="utf-8"))
        req_total = int(m.group(1)) if m else 0

    # 一期接口行 / 接口数：从 traceability.md 读，别手写（曾写死 102，实际 107）
    tr_md = ROOT / "traceability.md"
    ep_rows, ep_ifaces = 75, 0
    if tr_md.exists():
        m = re.search(r"一期接口行 (\d+) 条（合计 (\d+) 个接口", tr_md.read_text(encoding="utf-8"))
        if m:
            ep_rows, ep_ifaces = int(m.group(1)), int(m.group(2))

    head = re.sub(r"(\| \*\*需求覆盖率\*\*（主指标） \|)[^|]*(\|)[^|]*(\|)[^|]*(\|)",
                  rf"\1 PRD **规则级需求点** → 是否有 case 承接（不以接口为粒度） \2 需求点 {req_total} 条 **100% 承接** + 一期 {ep_rows} 接口行（{ep_ifaces} 个接口）**100% 承接** \3 `requirements.md`（主）+ `traceability.md`（接口侧），均可脚本校验 \4", head)
    head = re.sub(r"(\| \*\*实现覆盖度\*\*（副指标·快照） \|[^|]*\|)[^|]*(\|)",
                  rf"\1 已实现 {impl} · 待自动化 {auto_c['待自动化']} · 未实现 {auto_c['未实现']} · 契约偏差 {auto_c['契约偏差']} · 其他 {auto_c['纯手动'] + auto_c['引用脚本'] + auto_c['CI门禁']}（共 {total}） \2", head)
    head = re.sub(r"`未实现` 的 \d+ 条 case", f"`未实现` 的 {auto_c['未实现']} 条 case", head)
    head = head.replace("已实现 74 · 待自动化 69 · 未实现 30 · 纯手动 8 · 引用脚本 6 · CI门禁 1（共 188）",
                        f"已实现 {impl} · 待自动化 {auto_c['待自动化']} · 未实现 {auto_c['未实现']} · 纯手动 {auto_c['纯手动']} · 引用脚本 {auto_c['引用脚本']} · CI门禁 {auto_c['CI门禁']}（共 {total}）")

    out = [head.rstrip("\n"), "", "## 目录索引", ""]
    out.append("| 文件 | 范围 | case 数 | 主要 Sprint |")
    out.append("|---|---|---|---|")
    for rel, items in data.items():
        prefix, desc = DESC.get(rel, ("—", "（未登记说明）"))
        sps = Counter(i["sprint"] for i in items)
        sp_txt = " / ".join(k for k, _ in sps.most_common(3))
        out.append(f"| `{rel}` | **{prefix}** · {desc} | {len(items)} | {sp_txt} |")
    out.append(f"| **合计** | | **{total}** | |")
    out.append("")
    out.append("> 上表由 `gen_index.py` 从模块文件自动汇总，**新增文件/新增 case 后重跑即可**，不手改。")
    out.append("")
    out.append("**基线与工具**（本目录的判据源，勿只读 case 不看它们）：")
    out.append("")
    out.append("| 文件 | 作用 | 更新方式 |")
    out.append("|---|---|---|")
    out.append(f"| `requirements.md` | **需求点清单（{req_total} 条）**：PRD 规则级条目 → case，覆盖率的唯一主判据 | `gen_requirements.py` 生成 |")
    out.append(f"| `traceability.md` | 一期 {ep_rows} 接口行 / {ep_ifaces} 接口 → case 追溯（接口粒度，不与需求点重复） | `gen_traceability.py` 生成 |")
    out.append("| `gen_requirements.py` | 需求点生成器（含 `--check`，G-F 门禁） | 需求变更时先改 `REQS` |")
    out.append("| `gen_traceability.py` | 接口追溯生成器（含 `--check`，G-E 门禁） | PRD 接口表变更后重跑 |")
    out.append("| `gen_index.py` | 本文件生成器（目录索引 + 矩阵 + 统计） | 改完 case 后重跑 |")
    out.append("| `lint.sh` | 六道 Gate 校验（G-A~G-F），退出码 0 才算通过 | 新增校验规则时改 |")
    out.append("| `BACKLOG.md` | 反向开发清单：L1 需求缺口 / L2 实现缺口 / L3 验证缺口 | 每轮审核后回填 |")
    out.append("| `DEVIATIONS.md` | **契约偏差登记**（代码在、HTTP 面与 PRD 不符）+ 边界规则与关闭流程 | 发现偏差时登记并派卡 |")
    out.append("| `audit_baseline.py` | 证据体检：路由级交叉验证 `已实现/未实现/契约偏差` 是否还成立（STALE/DRIFT/WEAK） | 每轮 W0 与每周跑一次 |")
    out.append("| `SPEC-GAP-PROPOSAL.md` | 需求缺口补齐提案（8 项 + 建议值 + 影响 case + 可判判据） | 需求方确认后写进 PRD |")
    out.append("| `AUDIT-REPORT.md` | 多轮审核记录（发现 → 修复 → 结余） | 每轮追加 |")
    out.append(f"| `crosscutting/acceptance-19.2-19.3.md` | PRD 十九章验收口径逐条映射（19.2 / 19.3 / 19.4，实测 {cc_rows} 条） | 验收口径变更时改 |")
    out.append("")

    out.append(f"## 覆盖矩阵（{total} 条 · 列含义见「状态枚举」）")
    out.append("")
    for rel, items in data.items():
        prefix, desc = DESC.get(rel, ("—", ""))
        out.append(f"### {rel}（{prefix}）")
        out.append("")
        out.append("| Case ID | 标题 | 类型 | 优先级 | 实现现状 | 执行 |")
        out.append("|---|---|---|---|---|---|")
        for c in items:
            title = c["title"].replace("|", "\\|")
            out.append(f"| {c['id']} | {title} | {c['type']} | {c['pri']} | {c['auto']} | {c['exec']} |")
        out.append("")

    out.append("## 缺口清单（详版见 `BACKLOG.md`）")
    out.append("")
    out.append("> 本表只给计数与入口；**逐项的「缺什么交付物 / 依赖 / 阻塞哪些 case」在 `BACKLOG.md`**，避免两处维护产生偏差。")
    out.append("")
    out.append("| 线 | 含义 | 数量 | 详情 |")
    out.append("|---|---|---|---|")
    bl_txt = (ROOT / "BACKLOG.md").read_text(encoding="utf-8") if (ROOT / "BACKLOG.md").exists() else ""
    l1 = len(re.findall(r"^\| SPEC-GAP-\d+", bl_txt, re.M))
    l2 = len(re.findall(r"^\| IMPL-\d+", bl_txt, re.M))
    dev_txt = (ROOT / "DEVIATIONS.md").read_text(encoding="utf-8") if (ROOT / "DEVIATIONS.md").exists() else ""
    dev_open = len(re.findall(r"^\|\s*DEV-\d{2}\s*\|.*\|\s*开\s*\|\s*$", dev_txt, re.M))
    out.append(f"| **L1 · 需求缺口**（SPEC-GAP） | PRD 未写清，判据无法确定 | {l1} 项 | `BACKLOG.md` §一 |")
    out.append(f"| **L2 · 实现缺口**（IMPL-GAP） | 需求与判据都清楚，代码没做 | {l2} 项，阻塞 {auto_c['未实现']} 条 case | `BACKLOG.md` §二 |")
    out.append(f"| **L2.5 · 契约偏差**（DEV） | 代码在、HTTP 面与 PRD 不符（既非「已实现」也非「未实现」） | {auto_c['契约偏差']} 条 case · {dev_open} 项待关 | `DEVIATIONS.md` |")
    out.append(f"| **L3 · 验证缺口**（TEST-GAP） | 有实现，但测试证明不了验收口径 | {auto_c['待自动化']} 条待自动化 + 3 项特别提示 | `BACKLOG.md` §三 |")
    out.append("")
    out.append("## 统计（实测，非估算）")
    out.append("")
    out.append("| 维度 | 分布 |")
    out.append("|---|---|")
    out.append(f"| **总数** | **{total} 条 case**（homeos {dom_c['homeos']} + finance {dom_c['finance']} + calendar {dom_c['calendar']}）+ crosscutting {cc_rows} 条验收映射 |")
    out.append(f"| **需求覆盖率（主指标）** | 需求点 {req_total} 条 → **100% 有 case 承接**；一期 {ep_rows} 接口行 / {ep_ifaces} 接口 → **100% 承接**（`requirements.md` + `traceability.md`，均可脚本校验） |")
    out.append("| **按类型** | " + " / ".join(f"{k} {type_c[k]}" for k in ["功能", "边界", "异常", "安全", "性能"] if type_c[k]) + " |")
    out.append("| **按优先级** | " + " / ".join(f"{k} {pri_c[k]}" for k in ["P0", "P1", "P2"] if pri_c[k]) + " |")
    out.append("| **按实现现状（副指标·快照）** | " + " / ".join(
        f"{k} {auto_c[k]}" for k in ["已实现(HTTP)", "已实现(分层)", "契约偏差", "待自动化", "未实现", "纯手动", "引用脚本", "CI门禁"] if auto_c[k]) + " |")
    out.append(f"| **已实现合计** | {impl} / {total} = **{impl / total * 100:.1f}%**（HTTP 层 {auto_c['已实现(HTTP)']} 条） |")
    out.append(f"| **未实现** | {auto_c['未实现']} / {total} = **{auto_c['未实现'] / total * 100:.1f}%**，对应 `BACKLOG.md` 的 L2 交付物 |")
    out.append("| **按 Sprint** | " + " / ".join(f"{k} {v}" for k, v in sorted(sprint_c.items())) + " |")
    out.append(f"| **M1 / M2** | M1 期 {m1} 条；M2 期 {m2} 条 |")
    out.append("| **执行现状** | 全部「未执行」（规划态；落地后回填「通过 / 失败 / 阻塞」） |")
    out.append("")
    out.append("> 「已实现合计」是**快照**，会随开发推进上升；它**不改变分母**——"
               f"{total} 条始终是 P1 的验收总量。")
    cal_time = sum(1 for c in all_cases if c["id"].startswith("CAL-TIME-"))
    cal_due = sum(1 for c in all_cases if c["id"].startswith("CAL-DUE-"))
    cal_face = sum(1 for c in all_cases if c["id"].startswith("CAL-FACE-"))
    hom_time = sum(1 for c in all_cases if c["id"].startswith("HOM-TIME-"))
    # 不属于 ㉚ 平移的两条：CAL-TIME-017 是审核轮新增（编号恰好落在 CAL-TIME- 序列里，
    # 不能算「迁入」），HOM-SRCH-006 是复核轮补入的底座侧 case。
    cal_time_new = {"CAL-TIME-017"}
    hom_extra = {"HOM-SRCH-006"}
    cal_migrated = cal_time - sum(1 for c in all_cases if c["id"] in cal_time_new)
    cal_new = cal_due + cal_face + len(cal_time_new)
    baseline = total - cal_new - len(hom_extra)
    out.append(f"> **定版 ㉚ 的日历面拆分（只增不减）**：`calendar/` 域 {dom_c['calendar']} 条 = "
               f"从 `homeos/time-collab.md` **迁来**的 {cal_migrated} 条（ID `HOM-TIME-` → `CAL-TIME-`，数字不变，"
               f"步骤 / 预期结果 / 优先级 / Sprint 一字不改）+ 为 19.4 与面级形态**新增**的 {cal_new} 条"
               f"（`CAL-DUE-` {cal_due} + `CAL-FACE-` {cal_face} + 审核轮补的 `CAL-TIME-017` 1）；"
               f"`homeos/collab.md` 保留底座协作类 {hom_time} 条。"
               f"本轮未删任何 case：迁移前 {baseline} 条 → 现 {total} 条"
               f"（{baseline} + CAL 新增 {cal_new} + 复核轮 HOM-SRCH-006 1）。")
    out.append("")

    idx_path.write_text("\n".join(out) + "\n" + tail, encoding="utf-8")
    print(f"written index.md: {total} cases / {len(data)} files / 需求点 {req_total}")


if __name__ == "__main__":
    main()
