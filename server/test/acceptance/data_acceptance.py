#!/usr/bin/env python3
"""
data_acceptance.py —— G4 门禁：数据层不变量。

API 层全绿不等于数据是对的。这一门禁直接查库，验那些「接口测不出来」的性质：

1. **迁移到位**：每条序列都在 head，且声明的表都真的存在。
2. **无孤儿外键**：每张业务表的 family_id 都能在 homeos 的家庭表里找到对应行。
3. **金额符号自洽**：income 为正、expense 为负（迁移 0001 的注释就是这么定的）。
4. **无重复幂等键**：client_request_id 在非空时应唯一（uk 部分唯一索引）。
5. **软删除一致**：deleted_at 非空的行必须同时有 deleted_by。
6. **版本号单调**：version >= 1。
7. **预算周期闭合**：start_date <= end_date。
8. **分账金额自平**：参与方金额之和 = 分账总额。
9. **统计聚合与明细一致**：按分类 SUM 的金额 == 该分类的流水 SUM。
10. **跨域家庭隔离**：finance 表里不应出现 homeos 不存在的 family_id。

这些是「数据坏了但接口照样 200」的典型故障面 —— 比如第 3 条：符号错了两处
（写入取反 + 展示取绝对值），接口全程 200，但账算错了。
"""
from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

# 从 env.local 读连接信息，不硬编码密码
ROOT = Path(__file__).resolve().parents[3]
ENV_FILE = ROOT / "deploy" / "env.local"
COMPOSE = ROOT / "deploy" / "docker-compose.yml"
ENV_LOCAL = ROOT / "deploy" / "env.local"


def psql(sql: str) -> tuple[int, str]:
    """在 postgres16 容器里跑一条 SQL，返回 (rc, 输出)。"""
    cmd = [
        "docker", "compose", "--env-file", str(ENV_LOCAL), "-f", str(COMPOSE),
        "exec", "-T", "postgres16", "psql", "-U", "postgres", "-d", "homecube",
        "-tAF", "|", "-c", sql,
    ]
    p = subprocess.run(cmd, capture_output=True, text=True)
    return p.returncode, (p.stdout or "") + (p.stderr or "")


class Checks:
    def __init__(self) -> None:
        self.rows: list[tuple[str, bool, str]] = []

    def add(self, name: str, ok: bool, detail: str = "") -> None:
        self.rows.append((name, ok, detail))

    def render(self) -> None:
        print("=" * 76)
        for name, ok, detail in self.rows:
            print(f"  {'✓' if ok else '✗'} [{'PASS' if ok else 'FAIL'}] {name}" + (f"  — {detail}" if detail else ""))
        print("-" * 76)
        f = [r for r in self.rows if not r[1]]
        print(f"G4 数据验收：{len(self.rows) - len(f)}/{len(self.rows)} 通过")
        for name, _, detail in f:
            print(f"  - {name}: {detail}")
        print("=" * 76)


def q(sql: str) -> list[str]:
    rc, out = psql(sql)
    if rc != 0:
        return [f"__ERR__ {out.strip()[:160]}"]
    return [ln for ln in out.strip().split("\n") if ln != ""]


def one(sql: str) -> str:
    r = q(sql)
    return r[0] if r else ""


def main() -> int:
    c = Checks()

    # ---- 1. 迁移到位 ----
    homeos_v = one("SELECT version FROM homeos.schema_migrations_homeos")
    finance_v = one("SELECT version FROM finance.schema_migrations_finance")
    c.add("1a homeos 迁移在 head（=11）", homeos_v.strip() == "11", f"实际 {homeos_v}")
    c.add("1b finance 迁移在 head（=15）", finance_v.strip() == "15", f"实际 {finance_v}")

    # 声明了但没建的表 = 迁移被手工改过版本号绕过
    for tbl in [
        "finance.finance_settings", "finance.finance_transaction", "finance.finance_account",
        "finance.finance_category", "finance.finance_budget", "finance.finance_change_log",
        "finance.finance_idempotency", "finance.finance_split_settlement", "finance.finance_goal",
    ]:
        got = one(f"SELECT COALESCE(to_regclass('{tbl}')::text, 'MISSING')")
        c.add(f"1c 表存在 {tbl}", got != "MISSING", f"实际 {got}")

    # 0015 新增的两列（PRD 15.4）
    for col in ["created_by", "visibility"]:
        got = one(
            "SELECT COALESCE((SELECT data_type FROM information_schema.columns "
            f"WHERE table_schema='finance' AND table_name='finance_transaction' AND column_name='{col}'), 'MISSING')"
        )
        c.add(f"1d 0015 新增列 finance_transaction.{col}", got != "MISSING", f"实际 {got}")

    # 0015 的 CHECK 约束（拼错 visibility 会让 L3 过滤静默失效）
    chk = one(
        "SELECT COUNT(*) FROM pg_constraint WHERE conrelid='finance.finance_transaction'::regclass "
        "AND conname='ck_finance_transaction_visibility'"
    )
    c.add("1e visibility 取值域 CHECK 约束存在", chk.strip() == "1", f"实际 {chk}")

    # ---- 2. 无孤儿外键 ----
    orphan = one(
        "SELECT count(*) FROM finance.finance_transaction t "
        "WHERE NOT EXISTS (SELECT 1 FROM homeos.homeos_families f WHERE f.id = t.family_id)"
    )
    c.add("2 finance 流水的 family_id 都能在 homeos 找到", orphan.strip() == "0", f"孤儿 {orphan} 行")

    orphan_acc = one(
        "SELECT count(*) FROM finance.finance_transaction t "
        "WHERE NOT EXISTS (SELECT 1 FROM finance.finance_account a WHERE a.id = t.account_id)"
    )
    c.add("3 流水的 account_id 都有对应账户", orphan_acc.strip() == "0", f"孤儿 {orphan_acc} 行")

    # ---- 4. 金额符号自洽（迁移 0001: positive for income, negative for expense）----
    bad_sign = one(
        "SELECT count(*) FROM finance.finance_transaction "
        "WHERE (type='income' AND amount_cents < 0) OR (type='expense' AND amount_cents > 0)"
    )
    c.add("4 金额符号自洽（income>0 / expense<0）", bad_sign.strip() == "0", f"符号错 {bad_sign} 行")

    zero_amt = one("SELECT count(*) FROM finance.finance_transaction WHERE amount_cents = 0")
    c.add("4b 无零金额流水", zero_amt.strip() == "0", f"零金额 {zero_amt} 行")

    # ---- 5. 幂等键唯一 ----
    dup = one(
        "SELECT count(*) FROM (SELECT client_request_id FROM finance.finance_transaction "
        "WHERE client_request_id IS NOT NULL GROUP BY client_request_id HAVING count(*)>1) x"
    )
    c.add("5 幂等键无重复", dup.strip() == "0", f"重复 {dup} 组")

    empty_key = one(
        "SELECT count(*) FROM finance.finance_transaction WHERE client_request_id IS NOT NULL "
        "AND client_request_id::text = ''"
    )
    c.add("5b 幂等键非空串", empty_key.strip() == "0", f"空串 {empty_key} 行")

    # ---- 6. 软删除一致 ----
    bad_del = one(
        "SELECT count(*) FROM finance.finance_transaction "
        "WHERE deleted_at IS NOT NULL AND deleted_by IS NULL"
    )
    c.add("6 软删除行都带 deleted_by", bad_del.strip() == "0", f"不一致 {bad_del} 行")

    # ---- 7. 版本号 ----
    bad_ver = one("SELECT count(*) FROM finance.finance_transaction WHERE version < 1")
    c.add("7 version 均 >= 1", bad_ver.strip() == "0", f"异常 {bad_ver} 行")

    # ---- 8. 预算周期闭合 ----
    bad_period = one(
        "SELECT count(*) FROM finance.finance_budget WHERE start_date > end_date"
    )
    c.add("8 预算 start_date <= end_date", bad_period.strip() == "0", f"倒挂 {bad_period} 行")

    # ---- 9. 分账金额自平 ----
    unbalanced = one(
        "SELECT count(*) FROM ("
        "  SELECT s.id FROM finance.finance_split_settlement s "
        "  LEFT JOIN finance.finance_participant p ON p.settlement_id = s.id "
        "  GROUP BY s.id, s.total_amount_cents "
        "  HAVING s.total_amount_cents <> COALESCE(SUM(p.share_amount_cents), 0)"
        ") x"
    )
    c.add("9 分账：参与方金额之和 = 总额", unbalanced.strip() == "0", f"不平 {unbalanced} 个")

    # ---- 10. 统计聚合与明细一致 ----
    # 分类聚合表是预聚合的，若与明细漂移，报表就会给出与流水页不同的数字。
    # finance_statistics_agg 是 EAV 形态（family_id/period/metric_type/value），
    # 不是按分类的宽表 —— 所以只能校验「家庭+周期」的总额与明细一致。
    drift = one(
        "SELECT count(*) FROM ("
        "  SELECT a.family_id, a.period FROM finance.finance_statistics_agg a "
        "  WHERE a.metric_type = 'expense' "
        "    AND a.value <> COALESCE((SELECT SUM(amount_cents) FROM finance.finance_transaction t "
        "                             WHERE t.deleted_at IS NULL AND t.type='expense' "
        "                               AND t.family_id = a.family_id "
        "                               AND to_char(t.occurred_at,'YYYY-MM') = a.period), 0)"
        ") x"
    )
    c.add("10 预聚合总额与流水明细一致（expense）", drift.strip() == "0", f"漂移 {drift} 行")

    # ---- 11. 家庭隔离：finance 不应出现未知家庭 ----
    unknown_fam = one(
        "SELECT count(*) FROM finance.finance_settings s "
        "WHERE NOT EXISTS (SELECT 1 FROM homeos.homeos_families f WHERE f.id = s.family_id)"
    )
    c.add("11 finance 设置无未知家庭", unknown_fam.strip() == "0", f"异常 {unknown_fam} 行")

    # ---- 12. 账户余额与流水一致（缓存字段不得漂移）----
    drift_bal = one(
        "SELECT count(*) FROM finance.finance_account a "
        "LEFT JOIN (SELECT account_id, SUM(amount_cents) s FROM finance.finance_transaction "
        "            WHERE deleted_at IS NULL GROUP BY account_id) t ON t.account_id = a.id "
        "WHERE COALESCE(t.s, 0) <> a.balance"
    )
    c.add("12 账户余额缓存 = 其流水净额", drift_bal.strip() == "0", f"漂移 {drift_bal} 个账户")

    c.render()
    return 1 if any(not ok for _, ok, _ in c.rows) else 0


if __name__ == "__main__":
    sys.exit(main())
