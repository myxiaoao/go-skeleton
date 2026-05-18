<!-- PR 描述模板。如果是 draft 可以暂时跳过部分小节。 -->

## 变更概述

<!-- 1-3 句话说明改了什么、为什么。链接相关 issue（如有）。 -->

Closes #

## 变更类型

<!-- 勾选适用项 -->

- [ ] feat: 新功能
- [ ] fix: bug 修复
- [ ] refactor: 重构（不改变外部行为）
- [ ] docs: 文档
- [ ] test: 仅测试
- [ ] chore / build / ci: 工程改动
- [ ] breaking: 破坏性变更（需在 CHANGELOG 标注）

## 影响范围

<!-- 影响哪些层 / 哪些进程 -->

- [ ] `cmd/api`
- [ ] `cmd/worker`
- [ ] `cmd/migrate`
- [ ] `api/openapi.yaml` 契约
- [ ] DB schema（涉及迁移）
- [ ] 部署配置（systemd / Dockerfile / docker-compose）
- [ ] CI / 构建链

## 自检清单

<!-- 提交 PR 前请确认 -->

- [ ] `make verify` 全绿（fmt + vet + test + lint + oapi-verify + docs-verify）
- [ ] 改了 `api/openapi.yaml` 已跑 `make oapi` 并提交生成产物
- [ ] 新增 / 修改配置项已同步到 `.env.example` 并补注释
- [ ] 新增错误码已加到 `pkg/errcode/common.go` 并补 `messageFor` 文案
- [ ] 涉及破坏性变更已写入 `CHANGELOG.md` 的 `### Breaking` 段
- [ ] 测试覆盖了正反两面的关键路径
- [ ] 未引入 testify / gomock / sqlmock / Wire / Dig 等被禁用依赖

## 验证步骤

<!-- 列出 reviewer / CI 可以复跑的验证步骤 -->

```sh
make verify
# 必要时附加:
# go test ./internal/<pkg>/... -run TestXxx -v
# make test-race
```

## 备注

<!-- 已知限制、后续 TODO、迁移注意事项等 -->
