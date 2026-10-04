#!/usr/bin/env python3
"""
route_discovery.py —— 从服务源码里抽出**实际注册**的 HTTP 路由。

为什么要自动化抽而不是手写清单：验收若基于「我以为有哪些接口」，就等于用我的记忆
当 oracle —— 上一轮就是这么漏掉 /statistics/summary 与 /members 这类不存在路径的。
路由的唯一真源是 cmd/*/main.go 里 group.GET/POST/PUT/DELETE/PATCH 的注册语句。

输出：JSON [{method, path, service, protected}]，protected 表示是否挂在 mw.Handler() 之后。
"""
import json
import re
import sys
from pathlib import Path

# 本文件位于 <repo>/server/test/acceptance/，因此 parents[2] == <repo>/server
ROOT = Path(__file__).resolve().parents[2] / "services"

# group.VERB("path", ...) 与 protected.VERB("path", ...)
# 前缀的命名约定：group = 无鉴权，protected = 挂了 mw.Handler()。
ROUTE_RE = re.compile(
    r'^\s*(?P<var>group|protected|(?P<sub>\w+)\.(?:GET|POST|PUT|DELETE|PATCH))\s*\.'
    r'(?P<method>GET|POST|PUT|DELETE|PATCH)\s*\(\s*"(?P<path>[^"]+)"'
)

# 子 group 的挂载路径：familyGroup := protected.Group("/families")
SUBGROUP_RE = re.compile(r'(?P<var>\w+)\s*:=\s*(?P<parent>protected|group)\.Group\("(?P<path>[^"]+)"')

# 领域前缀（来自 registry / RoutePrefix 常量），按 cmd 目录名区分
DOMAIN_PREFIX = {
    "svc-homeos": "/api/homeos/",
    "svc-finance": "/api/finance/",
}


def parse_service(svc_dir: Path) -> list[dict]:
    main_go = svc_dir / "cmd" / svc_dir.name / "main.go"
    if not main_go.exists():
        return []
    prefix = DOMAIN_PREFIX[svc_dir.name]
    lines = main_go.read_text(encoding="utf-8").splitlines()

    # 先收集子 group 的路径，供展开 var.VERB 用
    subgroups: dict[str, tuple[str, str]] = {}  # var -> (parent, path)
    for ln in lines:
        m = SUBGROUP_RE.search(ln)
        if m:
            subgroups[m.group("var")] = (m.group("parent"), m.group("path"))

    routes: list[dict] = []
    for ln in lines:
        m = ROUTE_RE.search(ln)
        if not m:
            continue
        var = m.group("var")
        base = prefix
        protected = False
        if var in ("protected",):
            protected = True
        elif var == "group":
            protected = False
        else:
            # <var>.<METHOD>(...)：可能是 protected/ group 的子 group
            if var in subgroups:
                parent, sub_path = subgroups[var]
                base = prefix + sub_path.lstrip("/")
                protected = parent == "protected"
            else:
                # 未识别的变量：保守地当成需要鉴权（宁可多验一次）
                protected = True
        path = m.group("path")
        full = base + path.lstrip("/")
        if not full.startswith("/"):
            full = "/" + full
        routes.append(
            {
                "service": svc_dir.name,
                "method": m.group("method"),
                "path": full,
                "protected": protected,
            }
        )
    return routes


def main() -> int:
    all_routes: list[dict] = []
    for svc in sorted(ROOT.iterdir()):
        if svc.is_dir() and svc.name in DOMAIN_PREFIX:
            all_routes.extend(parse_service(svc))

    if not all_routes:
        print("ERROR: 没解析出任何路由，注册语句的正则可能与代码不符", file=sys.stderr)
        return 2

    # 同一 method+path 注册两次是真实的路由冲突，验收时只报一次
    seen: dict[tuple[str, str], int] = {}
    for r in all_routes:
        k = (r["method"], r["path"])
        seen[k] = seen.get(k, 0) + 1
    dupes = [f"{m} {p} x{n}" for (m, p), n in seen.items() if n > 1]

    out = {"routes": all_routes, "duplicates": dupes}
    print(json.dumps(out, ensure_ascii=False, indent=2))
    if dupes:
        print(f"WARNING: 发现 {len(dupes)} 处重复注册: {dupes}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
