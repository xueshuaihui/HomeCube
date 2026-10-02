# HomeCube 契约目录

本目录包含 HomeCube 系统的权威契约定义，作为跨服务通信和前后端协作的唯一真源。

## 结构

```
contracts/
├── events/          # 事件目录（7个域）
│   ├── homeos.yaml  # HomeOS 底座事件（P1-M1）
│   ├── finance.yaml # 财务面事件（P1-M1）
│   ├── purchase.yaml # 采购面事件（P2，空壳）
│   ├── diet.yaml    # 饮食面事件（P3，空壳）
│   ├── trip.yaml    # 出行面事件（P6，空壳）
│   ├── kin.yaml     # 家人面事件（P4，空壳）
│   └── growth.yaml  # 成长面事件（P5，空壳）
└── openapi/         # OpenAPI 契约（2个服务）
    ├── homeos.yaml  # HomeOS API（P1-M1）
    └── finance.yaml # Finance API（P1-M1）
```

## 事件目录规范

### business_id 取值约定（PRD 10.4）

同一业务对象可合法重复发生的事件，`business_id` 必须带周期或版本后缀：

- `finance.budget.exceeded` = `{budget_id}:{period}`（同一预算逐月/逐季超支）
- `{code}.due.registered` = `{source_id}:{due_at}`（改期即重发）
- `homeos.reminder.fired` = `{reminder_id}:{fire_date}`（周期提醒每次触发）
- `homeos.family.module.updated` = `{family_id}:{code}:{version}`（面反复启停）

一次性语义的事件用对象 id 即可：
- `homeos.todo.completed`
- `finance.transaction.created`
- `finance.bill.paid`
- `{code}.audit.recorded`

### 启用期说明

- **P1-M1**: 2026-Q3，HomeOS 底座 + 财务面单人闭环
- **P1-M2**: 财务面深度域与整面验收
- **P2-P6**: 各面出生期，联动随该面出生期末交付

## OpenAPI 契约规范

### 路由前缀

所有接口遵循 `/api/{code}/*` 前缀规则（registry 域表一致）：
- `/api/homeos/*` - HomeOS 底座服务
- `/api/finance/*` - 财务面服务

### 鉴权要求

除以下接口外，所有接口均需携带 JWT token：
- `GET /healthz` - 健康检查
- `GET /.well-known/jwks.json` - JWKS 公钥分发
- `POST /auth/login` - 登录
- `POST /auth/refresh` - Token 刷新

## CI 门禁

契约变更需通过 CI 第 5 道门禁（22.5）：
- 向后兼容 diff 检查
- 发布未登记事件拦截
- 引用不存在的服务拦截
- 破坏性变更未升 version 拦截

## 参考文档

- PRD: `docs/prd-homecube.md` §10.1-10.4（事件规范）、§3.7（接口清单）、§4.8（财务接口）
- 技术方案: `docs/p1-tech-plan.md` §3.1-3.2（JetStream 流规划 + 事件目录）、§10.2（门禁 5）
