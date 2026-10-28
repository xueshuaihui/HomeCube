# 协作协议

> 定义「谁做什么、怎么交接、什么时候能合」。判据细节见 `REVIEW.md`，环境细节见 `ENVIRONMENT.md`。

---

## 1. 角色

| | A | B |
|---|---|---|
| 身份 | 开发同学 A | 开发同学 B |
| 仓库 | `/Users/xuesh/www/HomeCube` | `/Users/xuesh/www/HomeCube-b` |
| 审核对象 | B 的 PR | A 的 PR |
| 任务来源 | 全局 Issue 池，先到先得 | 同左 |
| 额外职责 | 无（**不设集成人**，见 §6） | 无 |

两个人**没有主次**。全局池下不预设谁做哪个域，谁先领到谁做；跨域依赖靠 Issue 的 `blocked` 挡住，不靠人记。

## 2. 任务池：Issue + label

每张卡一个 Issue，标题 `[C-XX] 卡标题`，正文用 `templates/` 的 Issue 格式（DoD、覆盖 case、依赖）。

label 是唯一状态真相，清单：

| 维度 | 取值 | 说明 |
|---|---|---|
| 状态 | `status:ready` `wip` `review` `rework` `merged` `blocked` | 一卡同时只允许一个 |
| 归属 | `owner:a` `owner:b` | 认领后加，合并后保留 |
| 判决 | `review:approved` `review:changes` | 由审核者加 |
| 终审 | `user-approved` | 只有人能加 |
| 分类 | `wave:0`~`wave:6` `track:t0`~`t5` | 建 Issue 时打好 |
| 阻塞 | `blocked`（正文写 `Blocked by: #N`） | 依赖未清时加 |

## 3. 认领：原子性靠 push，不靠 label

label 的写入**不是互斥的**，两人可能同时加 `owner:a` 和 `owner:b`。真正原子的操作是 push 同名分支——第二次 push 必然被拒。

```bash
# 1. 筛池子：ready 且未被 blocked
gh issue list --label status:ready --search "-label:blocked" --limit 20

# 2. 抢占（原子裁决点）
git switch -c card/C-XX origin/develop/p1
git push -u origin card/C-XX        # 被拒 = 对方已占，换下一张

# 3. 记账（push 成功后才做）
gh issue edit <N> --add-label owner:a,status:wip --remove-label status:ready
```

**顺序不能倒。** 先打 label 再 push，失败时会留下一条「已认领但无分支」的脏记录，对方会误判这张卡有人在动。

被拒后的正确反应：`git switch develop/p1 && git branch -D card/C-XX`，回到第 1 步换卡。**不要**去追问对方，也不要试图覆盖对方分支。

## 4. 状态机

```
ready ──认领──> wip ──开 PR──> review ──approved+user-approved──> merged
                  ↑              │
                  └── rework <───┘ changes
                                 │
                            3 轮不过
                                 ↓
                              blocked（升级给人）
```

规则：

| 迁移 | 谁做 | 条件 |
|---|---|---|
| `ready → wip` | 认领者 | push 成功 |
| `wip → review` | 作者 | PR 已开且 CI 已挂上 |
| `review → rework` | 审核者 | 打 `review:changes` 并逐条写阻断项 |
| `rework → review` | 作者 | 修复后 push（同一 PR，不新开） |
| `review → merged` | 作者 | 三个条件同时满足（见 §6） |
| `any → blocked` | 任一方 | R3 仍不过，或依赖长期未清 |

## 5. 分支模型

| 分支 | 生命周期 | 规则 |
|---|---|---|
| `main` | 长期 | 只接受从 `develop/p1` 的整体合并（波末）。**永远可发布** |
| `develop/p1` | 长期 | 集成分支。PR 的 base |
| `card/{ID}` | 单卡 | 从 `develop/p1` 拉；合并后自动删除 |
| `wave{n}-start` / `wave{n}-done` | tag | 波末由志愿者打（谁收口谁打），回退粒度是波 |

**不设常驻的 `dev/a`、`dev/b`。** 仓库物理隔离后常驻分支没有意义，只会带来多余的 rebase。

rebase 约定：

```bash
# 每天开工第一次，以及提审前
git fetch origin
git rebase origin/develop/p1
```

## 6. 合并条件（三条同时满足）

| # | 条件 | 谁保证 |
|---|---|---|
| 1 | `review:approved` label 存在，且审核者做过反向验证 | 审核者 |
| 2 | `user-approved` label 存在 | 人 |
| 3 | CI 绿（lint + 该卡 checks） | Actions |

**合并权：作者自合**，用 `gh pr merge <N> --squash --delete-branch`。

审核者不代合。理由：代合会迫使审核者解决冲突，等于变相「代改代码」，破坏「谁的问题谁改」。也不设集成人——全局池模式下没有固定收口人，谁做完谁合，波末由任意一方（谁先空谁做）打 tag 并出波报。

## 7. 每卡 DoD（六项，缺一不可）

| # | 项 | 证据形式 |
|---|---|---|
| 1 | 覆盖的 case 全部 `通过` | `run_checks.py --apply` 回填，不手改 |
| 2 | `bash docs/cases/lint.sh` 绿 | 贴退出码 |
| 3 | 每条断言至少 1 次反向验证 | PR 里写「改坏第 N 条 → 失败 → 已改回」 |
| 4 | 迁移有 `down` 且验证可回滚 | 贴 `migrate down` 输出（不涉及则写「不涉及」） |
| 5 | 未手改判据 | `git diff origin/develop/p1...HEAD -- docs/cases/` 里无「预期结果」变更 |
| 6 | Issue 正文的进度段已更新 | 一行「已完成 / 未完成 / 下一步」 |

## 8. 门禁

| 层级 | 内容 | 谁执行 |
|---|---|---|
| 每卡 | 上面六项 | 作者自检 + 审核者复核 |
| 每波 | 全量 `run_checks --report` 出数；`audit_baseline.py` STALE=0；假绿 0；打 tag；出波报 | 先空出来的一方 |
| 终局 | 三种通过率同时给（名义 ≥92% / 可判 ≥96% / 可信度 100%）；性能 10 条单列 `待环境` | 双方 + 人 |

## 9. 异常处理

| 场景 | 规则 |
|---|---|
| 会话上下文丢失 | 从 Issue 正文 + 最近 PR 评论恢复；恢复后第一件事在 Issue 评论写「已恢复」 |
| 一方长时间不动 | 其 `status:wip` 的卡**不回收**，打 `owner-offline`；超一波未动交人裁决。**对方不得代做** |
| 单卡卡住 | 保持 `失败`，记入存量清单，不阻塞同波其他卡（沿用计划 §4.2） |
| 双方改动冲突 | 后合并方 rebase 解决；若同一文件的逻辑冲突无法自动判定，写 Issue 评论各自陈述事实，交人裁决。**禁止「谁后改谁赢」** |
| 危险命令误执行 | 见 `ENVIRONMENT.md` §6 防串仓 |

## 10. 降级方案（`gh` 不可用时）

若 GitHub 不可达或 token 无法配置：

| 层 | 降级为 |
|---|---|
| 任务池 | `collab/BOARD.md` 表格，靠 `git pull` 同步；认领仍是 push 分支抢占 |
| PR 与 review | 直接 `git fetch peer` 读对方分支，review 结论写 `collab/reviews/R-{ID}-r{n}.md` 并提交 |
| 门禁 | 本地跑 `lint.sh` + `run_checks.py`，结果贴文件 |

降级方案的代价：失去 label 的原子状态机与 PR 的 diff 视图，冲突率上升。**优先修好 `gh`，不要长期降级。**

## 11. 待你确认

见 `DECISIONS.md` §4。其中「共用账号」「集成分支名」「gh 认证」三项未定，未定期间按建议值执行。
