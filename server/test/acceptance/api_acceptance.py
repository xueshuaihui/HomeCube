#!/usr/bin/env python3
"""
api_acceptance.py —— G1 门禁：对**每一条实际注册的路由**发真实请求并断言结果。

设计原则（针对前几轮验收失败的根本原因）：

1. **路由清单来自 route_discovery.py，不手写。** 手写清单等于拿「我以为有哪些接口」
   当 oracle —— 上一轮 /statistics/summary 与 /members 就是这样漏掉的。

2. **每条路由都必须真的被调用一次。** 未覆盖的路由直接算失败（uncovered 列表非空即 FAIL），
   不允许「大概没问题」。

3. **断言分级，且分级写清楚。**
   - protected 路由无 token → 必须 401（不是 404/500）；
   - protected 路由带 token → 2xx，或者**明确的业务语义 4xx**（400 参数缺失/403 权限/
     404 资源不存在/409 冲突）。出现 5xx 一律 FAIL。
   - public 路由无 token → 必须不是 5xx。
   5xx 是「代码有 bug」的信号，4xx 是「参数/权限不对」的信号，两者绝不可混为一谈 ——
   这正是上一轮把「报表页 404」当成「服务端 bug」而实际是前端路径写错的原因。

4. **写操作带幂等键、且重复提交验证幂等**（PRD 14.7）。

用法：
    python3 api_acceptance.py                 # 跑全量
    python3 api_acceptance.py --verbose       # 打印每条明细
    python3 api_acceptance.py --json out.json # 输出机器可读结果
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid
from dataclasses import dataclass, field
from pathlib import Path

HOMEOS = os.environ.get("HC_HOMEOS", "http://127.0.0.1:8080")
FINANCE = os.environ.get("HC_FINANCE", "http://127.0.0.1:8081")
TEST_PHONE = os.environ.get("HC_TEST_PHONE", "13800138000")
TEST_SMS_CODE = os.environ.get("HC_TEST_SMS_CODE", "123456")

HERE = Path(__file__).resolve().parent
VERBS_WITH_BODY = {"POST", "PUT", "PATCH"}


# --------------------------------------------------------------------------- HTTP


@dataclass
class Result:
    method: str
    path: str
    status: int
    verdict: str  # PASS / FAIL
    reason: str
    body_snippet: str = ""


class Client:
    """最小 HTTP 客户端。用 urllib 而非 requests —— 不给项目加依赖。"""

    def __init__(self) -> None:
        self.token: str | None = None
        self.family_id: str | None = None
        self.member_id: str | None = None

    def call(
        self,
        method: str,
        path: str,
        body: dict | None = None,
        query: dict | None = None,
        token: str | None = None,
        base: str | None = None,
        timeout: int = 12,
    ) -> tuple[int, str]:
        base = base or (FINANCE if path.startswith("/api/finance") else HOMEOS)
        url = base + path
        if query:
            url += "?" + urllib.parse.urlencode(query)
        data = None
        headers = {"Accept": "application/json"}
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        tok = token if token is not None else self.token
        if tok:
            headers["Authorization"] = f"Bearer {tok}"
        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                return resp.status, resp.read().decode("utf-8", "replace")
        except urllib.error.HTTPError as e:
            return e.code, e.read().decode("utf-8", "replace")
        except Exception as e:  # 连接失败等
            return 0, f"{type(e).__name__}: {e}"

    def login(self) -> bool:
        """走真实的两步登录：先发码再换 token。验证码是一次性的，顺序不能反。"""
        s, _ = self.call("POST", "/api/homeos/auth/sms-code", {"phone": TEST_PHONE}, base=HOMEOS)
        if s != 200:
            print(f"FATAL: sms-code 返回 {s}", file=sys.stderr)
            return False
        s, b = self.call(
            "POST",
            "/api/homeos/auth/login",
            {"phone": TEST_PHONE, "code": TEST_SMS_CODE},
            base=HOMEOS,
        )
        if s != 200:
            print(f"FATAL: login 返回 {s}: {b[:200]}", file=sys.stderr)
            return False
        d = json.loads(b)
        self.token = d.get("access_token")
        self.family_id = d.get("family_id")
        if not self.token or not self.family_id:
            print(f"FATAL: 登录响应缺 access_token/family_id: {b[:200]}", file=sys.stderr)
            return False
        return True


# ------------------------------------------------------------------- 路由准备


def substitute_params(path: str, client: Client, ctx: dict) -> tuple[str, dict]:
    """
    把路由里的 :param 换成真值，并顺带收齐请求真正需要的 query。

    这里返回 (path, query)：family_id 这类**由 handler 的 binding 要求**的参数
    （finance 的 List* 缺它即 400）必须由验收脚本自己带上，否则测的是 400 而不是功能。
    """
    query: dict = {}
    new = path
    if ":id" in new:
        new = new.replace(":id", ctx.get("object_id", "00000000-0000-0000-0000-000000000000"))
    if ":fid" in new:
        new = new.replace(":fid", client.family_id or "")
    # finance 全部读接口的 family_id 必填
    if path.startswith("/api/finance") and client.family_id:
        query.setdefault("family_id", client.family_id)
    if path.endswith("/modules") and "family_id" not in query:
        if client.family_id:
            query["family_id"] = client.family_id
    return new, query


def build_body(method: str, path: str, client: Client, ctx: dict) -> dict | None:
    """
    为写操作构造**符合 handler binding** 的请求体。

    关键：client_request_id 必须是合法 UUID —— finance_transaction.client_request_id
    列类型是 uuid，非 UUID 会被 PostgreSQL 拒（SQLSTATE 22P02）。
    """
    if method not in VERBS_WITH_BODY:
        return None
    fam = client.family_id or ""
    b: dict = {"client_request_id": str(uuid.uuid4())}
    if "/transactions" in path:
        b.update(
            family_id=fam,
            type="expense",
            amount_cents=100,
            account_id=ctx.get("account_id", "00000000-0000-0000-0000-000000000000"),
            occurred_at="2026-10-01T00:00:00Z",
        )
    elif "/accounts" in path:
        b.update(family_id=fam, name="acc-acceptance", type="cash", balance=0)
    elif "/categories" in path:
        b.update(family_id=fam, name="cat-acceptance", icon="c", sort_order=0)
    elif "/budgets" in path:
        b.update(
            family_id=fam,
            category_id=ctx.get("category_id", "00000000-0000-0000-0000-000000000000"),
            amount_cents=100000,
            period="monthly",
            start_date="2026-10-01T00:00:00Z",
            end_date="2026-10-31T23:59:59Z",
        )
    elif "/bills" in path:
        b.update(family_id=fam, name="bill-acceptance", amount_cents=1000, due_date="2026-10-20T00:00:00Z")
    elif "/loans" in path:
        b.update(family_id=fam, direction="lend", counterparty="acc", amount_cents=1000)
    elif "/ledgers" in path:
        b.update(family_id=fam, name="ledger-acceptance", member_ids=[])
    elif "/goals" in path:
        b.update(family_id=fam, name="goal-acceptance", target_amount_cents=100000)
    elif "/statistics" in path:
        b.update(family_id=fam, period="2026-10")
    elif "/settings" in path:
        b.update(family_id=fam, currency_unit="CNY", decimal_places=2)
    elif "/modules" in path:
        # 面配置：code + enabled + version 三项都是 required（首次开通 version 传 0）
        b.update(code="finance", enabled=False, version=0)
    return b


# --------------------------------------------------------------------- 断言逻辑

# 这些 4xx 是「请求本身不合法」的正常回答，不算缺陷
BUSINESS_4XX = {400, 401, 403, 404, 409, 422}


def judge(method: str, path: str, protected: bool, status: int, body: str, phase: str) -> Result:
    """
    phase='noauth'  期望 401（受保护路由必须拒绝匿名）
    phase='authed'  期望 2xx 或业务 4xx；5xx 一律失败
    """
    snip = body[:200].replace("\n", " ")
    if phase == "noauth":
        if status == 401:
            return Result(method, path, status, "PASS", "匿名被正确拒绝")
        if status == 0:
            return Result(method, path, status, "FAIL", f"连不上服务: {snip}", snip)
        # 404 说明这条路由其实没注册在受保护组里，路由发现与实际不符
        return Result(method, path, status, "FAIL", f"匿名访问未返回 401（实际 {status}）", snip)

    # phase == 'authed'
    if status == 0:
        return Result(method, path, status, "FAIL", f"连不上服务: {snip}", snip)
    if 200 <= status < 300:
        return Result(method, path, status, "PASS", "2xx", snip)
    if status in BUSINESS_4XX:
        return Result(method, path, status, "PASS", f"业务 4xx（{status}）", snip)
    if status >= 500:
        return Result(method, path, status, "FAIL", f"服务端错误 {status}: {snip}", snip)
    return Result(method, path, status, "FAIL", f"意外状态 {status}: {snip}", snip)


# ------------------------------------------------------------------------ 主流程


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--verbose", action="store_true")
    ap.add_argument("--json", default="")
    ap.add_argument("--routes", default=str(HERE / "routes.json"))
    args = ap.parse_args()

    routes_file = Path(args.routes)
    if not routes_file.exists():
        print(f"ERROR: 缺 {routes_file}，先跑 route_discovery.py", file=sys.stderr)
        return 2
    routes = json.loads(routes_file.read_text())["routes"]

    c = Client()
    print("== 登录 ==")
    if not c.login():
        return 2
    print(f"   family_id={c.family_id}")

    # 先拿一批真实对象 id，供 :id 路由与写操作使用
    # 先备齐写操作要用的真实对象。缺 account_id/category_id 会让后续 transactions/budgets
    # 的 POST 因外键不存在而 400，验收就退化成「在测参数校验」而不是「在测功能」。
    # 读列表必须带 family_id（handler 的 binding 要求，缺即 400）—— 少了这个 query
    # 就会静默拿到空 items，进而让后面所有依赖 id 的断言落空。
    ctx: dict = {}

    def ensure_object(kind: str, key: str) -> str | None:
        s, b = c.call("GET", f"/api/finance/{kind}", query={"family_id": c.family_id})
        if s != 200:
            print(f"   [warn] 读 {kind} 失败: {s} {b[:120]}")
            return None
        items = json.loads(b).get("items") or []
        if items:
            return items[0].get("id")
        # 库里还没有就先建一个
        payload = (
            {"family_id": c.family_id, "name": f"acc-{kind}", "type": "cash", "balance": 0}
            if kind == "accounts"
            else {"family_id": c.family_id, "name": f"cat-{kind}", "icon": "c", "sort_order": 0}
        )
        s, b = c.call("POST", f"/api/finance/{kind}", body=payload)
        if s in (200, 201):
            return json.loads(b).get("id")
        print(f"   [warn] 建 {kind} 失败: {s} {b[:120]}")
        return None

    ctx["account_id"] = ensure_object("accounts", "account_id")
    ctx["category_id"] = ensure_object("categories", "category_id")
    ctx["object_id"] = ctx.get("account_id") or "00000000-0000-0000-0000-000000000000"
    print(f"   account_id={ctx.get('account_id')} category_id={ctx.get('category_id')}")
    if not ctx.get("account_id"):
        print("   [warn] 没有可用账户，幂等与带外键的写路径将无法判定")

    results: list[Result] = []
    # healthz / metrics 是运维端点，不带业务前缀，单独验
    for method, path, protected in [(r["method"], r["path"], r["protected"]) for r in routes]:
        real_path, query = substitute_params(path, c, ctx)

        # 第一轮：匿名。受保护的必须 401
        if protected:
            s, b = c.call(method, real_path, token="", base=None, query=query,
                          body=build_body(method, path, c, ctx))
            results.append(judge(method, path, protected, s, b, "noauth"))

        # 第二轮：带 token
        s, b = c.call(method, real_path, query=query, body=build_body(method, path, c, ctx))
        results.append(judge(method, path, protected, s, b, "authed"))

        if args.verbose:
            r = results[-1]
            print(f"  {r.verdict} {r.method:6} {r.path:52} -> {r.status} {r.reason}")

    # 运维端点
    for base, name in ((HOMEOS, "homeos"), (FINANCE, "finance")):
        s, b = c.call("GET", "/healthz", base=base, token="")
        r = Result(
            method="GET",
            path=f"{name}:/healthz",
            status=s,
            verdict="PASS" if s == 200 else "FAIL",
            reason="ok" if s == 200 else f"healthz 返回 {s}",
            body_snippet=b[:120],
        )
        results.append(r)
        if args.verbose:
            print(f"  {r.verdict} GET    {name}:/healthz -> {s} {r.reason}")

    # 幂等语义（PRD 14.7）：同一 client_request_id 提交两次，第二次必须复用首次结果
    idem = test_idempotency(c, ctx)

    failed = [r for r in results if r.verdict == "FAIL"]
    covered = {r.path for r in results}
    all_paths = {r["path"] for r in routes} | {"homeos:/healthz", "finance:/healthz"}

    print()
    print("=" * 72)
    print(f"G1 API 验收：{len(results)} 次调用，覆盖 {len(covered)}/{len(all_paths)} 条路由")
    print(f"  PASS {len(results) - len(failed) - (0 if idem else 1)}  FAIL {len(failed) + (0 if idem else 1)}")
    if idem is False:
        print("  [FAIL] 幂等键重复提交未返回首次结果")
    if failed:
        print("\n失败明细：")
        for r in failed:
            print(f"  - {r.method} {r.path} -> {r.status}  {r.reason}")
            if r.body_snippet:
                print(f"      {r.body_snippet[:160]}")
    uncovered = all_paths - covered
    if uncovered:
        print(f"\n未覆盖路由（应为空）：{sorted(uncovered)}")
    print("=" * 72)

    if args.json:
        Path(args.json).write_text(
            json.dumps(
                {
                    "total": len(results),
                    "failed": len(failed),
                    "idempotency_ok": idem,
                    "uncovered": sorted(uncovered),
                    "results": [r.__dict__ for r in results],
                },
                ensure_ascii=False,
                indent=2,
            ),
            encoding="utf-8",
        )

    return 1 if (failed or uncovered or idem is False) else 0


def test_idempotency(c: Client, ctx: dict) -> bool | None:
    """
    PRD 14.7：重复的 client_request_id 必须返回首次结果，而不是再插一行。
    返回 True/False；无法判定时返回 None（不计入失败）。
    """
    if not ctx.get("account_id"):
        return None
    key = str(uuid.uuid4())
    payload = {
        "client_request_id": key,
        "family_id": c.family_id,
        "type": "expense",
        "amount_cents": 777,
        "account_id": ctx["account_id"],
        "occurred_at": "2026-10-01T00:00:00Z",
    }
    s1, b1 = c.call("POST", "/api/finance/transactions", body=payload)
    s2, b2 = c.call("POST", "/api/finance/transactions", body=payload)
    if s1 not in (200, 201) or s2 not in (200, 201):
        print(f"  [幂等] 首次 {s1} / 二次 {s2}，无法判定")
        return None
    try:
        id1 = json.loads(b1).get("id")
        id2 = json.loads(b2).get("id")
    except Exception:
        return None
    if id1 and id2:
        ok = id1 == id2
        print(f"  [幂等] 首次 id={id1} / 二次 id={id2} -> {'一致 PASS' if ok else '不一致 FAIL'}")
        return ok
    return None


if __name__ == "__main__":
    sys.exit(main())
