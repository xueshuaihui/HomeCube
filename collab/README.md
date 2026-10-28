# 协作规范（入口）

> 两个 agent 客户端、两个独立本地仓库、同一个 origin，并行开发 HomeCube P1。
> 本文 5 分钟读完；细节在分册，按需查阅。

**版本**：v0.3（草案，待人确认）
**适用范围**：P1 一期，67 张卡（T0~T5）
**最后更新**：2026-10-28

---

## 一句话说清

两张卡同时推进：各自从 GitHub Issue 池认领，在自己仓库的 `card/{ID}` 分支开发，push 后开 PR；对方独立复跑 + 反向验证后给判决；你在 PR 上打 `user-approved` 作为最终闸门；作者自合。

## 五条铁律

| # | 铁律 | 违反的代价 |
|---|---|---|
| 1 | **不改判据换通过** | 291 条 case 的需求基线被悄悄改写，上一轮投入作废 |
| 2 | **没有 check 不算通过** | 「已实现」是判断，「通过」是事实 |
| 3 | **改断言必重跑** | 断言一改，旧「通过」自动失效 |
| 4 | **失败不隐藏** | `失败` 比 `未执行` 诚实；禁止「基本通过」 |
| 5 | **认领先 push 分支，再打 label** | 顺序反了会出现「label 说归我、分支在对方手里」 |

## 环境速查

| 项 | A | B |
|---|---|---|
| 仓库路径 | `/Users/xuesh/www/HomeCube` | `/Users/xuesh/www/HomeCube-b` |
| compose project | `homecube` | `homecube-b` |
| `NGINX_HTTP_PORT` | 8080 | 8081 |
| PG 宿主端口 | 5432 | 5433 |
| NATS 宿主端口 | 4222 | 4223 |
| 常驻分支 | 无（一卡一分支） | 无（一卡一分支） |

> 端口与 project name 的隔离**尚未落地**，见 `ENVIRONMENT.md` 的 P0-1~P0-3。在落地之前，**禁止在 B 仓执行 `docker compose down -v`**。

## 文档导航

| 文件 | 什么时候读 |
|---|---|
| `PROTOCOL.md` | 开工前必读：角色、认领、状态机、分支、合并、DoD |
| `ENVIRONMENT.md` | 建 B 仓时读；每次起栈前扫一眼端口表 |
| `REVIEW.md` | 提审前（作者）与审卡前（审核者）各读一次 |
| `DECISIONS.md` | 拿不准某件事能不能自己定时查；做了黄区决策后追加记录 |
| `templates/AGENT.md` | 复制到两个仓库根目录，防串仓 |
| `templates/PR.md` | 开 PR 时照抄的 9 行判决单 |

## 流程全景

```
Issue(status:ready) ──认领(push 分支)──> status:wip
        │
        ├─ 开发 ──> 开 PR ──> status:review
        │                        │
        │            ┌───────────┴───────────┐
        │        对方审核                   你看 9 行判决单
        │            │                        │
        │     ┌──────┴──────┐                 │
        │  approved     changes              │
        │     │            │                 │
        │     │       status:rework ──修复───┘
        │     │            │
        │     └────── 两者都过 ──> merged ──> Issue closed
        │
        └─ 若 3 轮仍不过 ──> status:blocked ──> 升级给人
```

## 待你确认

见 `DECISIONS.md` §4。未确认前，本目录的规范作为**草案**执行，可以修改。
