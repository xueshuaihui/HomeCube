# External Dependency Selection

This document tracks all external service dependencies for HomeCube, their candidate vendors, current P1 status, and commercial launch deadlines.

## Dependency Selection Table

| 依赖类型 | 候选供应商 | P1 状态 | 商用前定版时间 | 备注 |
|---|---|---|---|---|
| OCR | 百度 AI / 腾讯云 OCR / 阿里云 OCR | 桩实现 | P2 开始前 | 需评估准确率与成本 |
| ASR | 百度语音 / 腾讯云 ASR / 阿里云 ASR | 桩实现 | P2 开始前 | 需评估识别率与延迟 |
| 地图 | 高德地图 / 百度地图 / 腾讯地图 | 未实现 | P3 出行面出生前 | 饮食面无需地图 |
| 对象存储 | 本地文件系统 / 阿里云 OSS / 腾讯云 COS | 本地文件系统 | P2 分包下发前 | 决定 CDN 方案 |
| 短信 | 阿里云短信 / 腾讯云短信 | 固定测试验证码 | P2 商用前 | 登录验证码通道 |
| 推送 | 极光推送 / 个推 / 友盟 | 桩实现 | P2 商用前 | 站内信 + 推送双通道 |

## Notes

- **P1 Status**: All adapters are implemented as stubs during P1 phase to enable development without external dependencies.
- **Commercial Deadline**: Vendors must be selected and credentials configured before the specified deadline.
- **Evaluation Criteria**: Each vendor should be evaluated on accuracy, cost, latency, and reliability before final selection.

## References

- PRD 19.3: P1 dependency selection requirements
- docs/p1-tech-plan.md §十一 S19: P1-M2 closeout requirements
