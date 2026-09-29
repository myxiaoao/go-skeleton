# go-skeleton 项目约定（Claude Code 入口）

> 本项目的 AI 编码助手规则统一维护在 [`AGENTS.md`](./AGENTS.md)（唯一来源，Codex / Claude Code 等共用）。下面一行由 Claude Code 自动导入全文；**规则改动只改 AGENTS.md，不要把规则正文复制回本文件**——`make docs-verify` 会拦截缺导入行或与 AGENTS.md 同名的 `## ` 段。

@AGENTS.md

## Claude Code 专属补充

- 个人全局规则（`~/.claude/CLAUDE.md`）与本项目约定冲突时，以 AGENTS.md 为准（例如代码注释用简体中文）。
- `.claude/settings.json` 已启用 `modern-go-guidelines` 插件（JetBrains go-modern-guidelines），写 / 改 Go 代码时按它给出的 Go 1.27 写法。
