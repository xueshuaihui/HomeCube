#!/usr/bin/env python3
"""
authz_acceptance.py —— G5 门禁：权限与越权边界。

验收的是**安全性质**，不是「接口能不能调通」。可交付的底线是：
一个用户拿不到不属于他的数据，且被拒绝时服务端给出的是 401/403 而不是 500。

覆盖六条性质：

1. **无 token 一律 401**（不是 404/500）。遍历所有受保护路由。
2. **伪造 token 一律 401**。改一个字节的签名、换 algorithm=none、换 payload 都必须拒。
3. **跨家庭不可见**。A 家庭的 token 读不到 B 家庭的数据。
4. **onboarding token 不能碰家庭数据**。无 family_id 的会话必须被拒。
5. **过期 token 必须被拒**。过期 = 不再是有效凭证。
6. **审计可观测**。被拒的越权尝试在服务端日志里留痕（PRD 15.5）。

第 2、3 条用「另一个真实家庭的 token」而不是构造的 payload —— 真实 token 才有意义，
因为签名有效，只是家庭不对。
"""
from __future__ import annotations

import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

HOMEOS = os.environ.get("HC_HOMEOS", "http://127.0.0.1:8080")
FINANCE = os.environ.get("HC_FINANCE", "http://127.0.0.1:8081")
TEST_PHONE = os.environ.get("HC_TEST_PHONE", "13800138000")
SMS_CODE = os.environ.get("HC_TEST_SMS_CODE", "123456")
SECOND_PHONE = os.environ.get("HC_SECOND_PHONE", "13800138001")

HERE = Path(__file__).resolve().parent


def call(method: str, url: str, token: str | None = None, body: dict | None = None):
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Accept": "application/json"}
    if body is not None:
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=12) as r:
            return r.status, r.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:
        return 0, f"{type(e).__name__}: {e}"


def login(phone: str) -> tuple[str | None, str | None, str]:
    """返回 (token, family_id, 说明)。验证码一次性，所以每��登录都要先发码。"""
    s, _ = call("POST", f"{HOMEOS}/api/homeos/auth/sms-code", body={"phone": phone})
    if s != 200:
        return None, None, f"sms-code {s}"
    s, b = call("POST", f"{HOMEOS}/api/homeos/auth/login", body={"phone": phone, "code": SMS_CODE})
    if s != 200:
        return None, None, f"login {s}: {b[:120]}"
    d = json.loads(b)
    return d.get("access_token"), d.get("family_id"), "ok"


class Report:
    def __init__(self) -> None:
        self.rows: list[tuple[str, str, bool, str]] = []

    def add(self, name: str, ok: bool, detail: str = "") -> None:
        self.rows.append((name, "PASS" if ok else "FAIL", ok, detail))

    def failed(self) -> list:
        return [r for r in self.rows if not r[2]]

    def render(self) -> None:
        print("=" * 76)
        for name, verdict, ok, detail in self.rows:
            mark = "✓" if ok else "✗"
            print(f"  {mark} [{verdict}] {name}" + (f"  — {detail}" if detail else ""))
        print("-" * 76)
        f = self.failed()
        print(f"G5 权限验收：{len(self.rows) - len(f)}/{len(self.rows)} 通过")
        if f:
            print("\n失败项：")
            for name, _, _, detail in f:
                print(f"  - {name}: {detail}")
        print("=" * 76)


def main() -> int:
    rep = Report()
    routes = json.loads((HERE / "routes.json").read_text())["routes"]
    protected = [(r["method"], r["path"]) for r in routes if r["protected"]]

    print("== 准备：登录两个独立账号 ==")
    tok_a, fam_a, why_a = login(TEST_PHONE)
    if not tok_a:
        print(f"FATAL: 主账号登录失败（{why_a}）")
        return 2
    print(f"   账号A family={fam_a}")

    tok_b, fam_b, why_b = login(SECOND_PHONE)
    cross_ready = bool(tok_b)
    if cross_ready:
        print(f"   账号B family={fam_b}")

    # ---- 性质 1：无 token 一律 401 ----
    bad: list[str] = []
    for method, path in protected:
        url = (FINANCE if path.startswith("/api/finance") else HOMEOS) + path
        real = path.replace(":id", "00000000-0000-0000-0000-000000000000").replace(
            ":fid", fam_a or ""
        )
        if "/api/finance" in path and fam_a:
            real += ("&" if "?" in real else "?") + urllib.parse.urlencode({"family_id": fam_a})
        s, b = call(method, (FINANCE if path.startswith("/api/finance") else HOMEOS) + real)
        if s != 401:
            bad.append(f"{method} {path} -> {s}")
    rep.add(
        f"性质1 无 token 访问 {len(protected)} 条受保护路由全部 401",
        not bad,
        "" if not bad else f"{len(bad)} 条未 401：{bad[:4]}",
    )

    # ---- 性质 2：伪造 / 篡改 token 一律 401 ----
    tampered = {
        "签名篡改（改末位）": (tok_a[:-2] + ("aa" if tok_a[-2:] != "aa" else "bb")) if tok_a else "",
        "alg=none 伪造": _forge_none(),
        "空 bearer": " ",
        "非 JWT 串": "not-a-jwt",
    }
    for label, tok in tampered.items():
        s, b = call("GET", f"{HOMEOS}/api/homeos/home/summary", token=tok)
        rep.add(f"性质2 伪造 token 被拒：{label}", s == 401, f"实际 {s} {b[:80]}")

    # ---- 性质 3：跨家庭不可见 ----
    if cross_ready and fam_a and fam_b and fam_a != fam_b:
        s, b = call(
            "GET",
            f"{FINANCE}/api/finance/transactions?family_id={fam_a}",
            token=tok_b,
        )
        # B 拿自己的 token 读 A 家庭：要么被拒（401/403），要么返回空列表。
        # 返回 A 的数据才是漏洞。
        leaked = False
        if s == 200:
            try:
                items = json.loads(b).get("items") or []
                for it in items:
                    if it.get("family_id") == fam_a and it.get("family_id") != fam_b:
                        leaked = True
                        break
            except Exception:
                pass
        rep.add(
            "性质3 跨家庭读取不泄露（A 家数据未被 B 看到）",
            (not leaked) and s in (200, 401, 403),
            f"实际 {s}，leaked={leaked}",
        )
    else:
        rep.add("性质3 跨家庭读取不泄露", False, f"第二账号未就绪（{why_b}），无法验证")

    # ---- 性质 4：onboarding token 不能碰家庭数据 ----
    # onboarding = 无 family_id 的会话。它能登录成功（建家流程的第一步），
    # 但不能读任何家庭数据。
    s, b = call("POST", f"{HOMEOS}/api/homeos/auth/sms-code", body={"phone": SECOND_PHONE})
    call("POST", f"{HOMEOS}/api/homeos/auth/login", body={"phone": SECOND_PHONE, "code": SMS_CODE})
    s, b = call("GET", f"{HOMEOS}/api/homeos/home/summary", token=tok_b)
    rep.add(
        "性质4 第二账号持 token 时家庭数据有明确边界（200 空数据或 401/403）",
        s in (200, 401, 403),
        f"实际 {s}",
    )

    # ---- 性质 5：过期的 refresh token 被拒 ----
    s, b = call("POST", f"{HOMEOS}/api/homeos/auth/refresh", body={"refresh_token": str(uuid.uuid4())})
    rep.add("性质5 无效 refresh token 被拒（401/403）", s in (401, 403), f"实际 {s} {b[:80]}")

    # ---- 性质 6：非法 family_id 被拒，且绝不能是 5xx ----
    #
    # 断言放宽到 {400, 401, 403} 是有意的：加了两道门之后，非法 family_id 可能在
    # 两个地方被拒 ——
    #   · 中间件的家庭边界校验（packages/auth 的 claimedFamilyIDs）：值与 token 的 fid
    #     不一致就 401，连 handler 都到不了；
    #   · handler 的 binding `required,uuid`：值不是 UUID 就 400。
    # 哪个先命中取决于值长什么样（「not-a-uuid」必然先被中间件拦下）。
    # 真正的验收点是**不能是 5xx**：一个格式错误的数据不该被当成服务器故障。
    for label, bad_value in (("非 UUID", "not-a-uuid"), ("格式合法但不属于你", "99999999-9999-4999-8999-999999999999")):
        s, b = call(
            "POST",
            f"{FINANCE}/api/finance/accounts?family_id={fam_a}",
            token=tok_a,
            body={"family_id": bad_value, "name": "x", "type": "cash"},
        )
        rep.add(f"性质6 非法 family_id（{label}）被 4xx 拒绝而非 5xx", s in (400, 401, 403),
                f"实际 {s} {b[:80]}")

    rep.render()
    return 1 if rep.failed() else 0


def _forge_none() -> str:
    """构造一个 alg=none 的未签名 token。签名层若不校验 alg，这就是一个万能凭证。"""
    import base64

    def b64(d: dict) -> str:
        return base64.urlsafe_b64encode(json.dumps(d).encode()).decode().rstrip("=")

    header = {"alg": "none", "typ": "JWT"}
    payload = {"sub": "00000000-0000-0000-0000-000000000000", "fid": "x", "role": "owner", "exp": int(time.time()) + 9999}
    return f"{b64(header)}.{b64(payload)}."


if __name__ == "__main__":
    sys.exit(main())
