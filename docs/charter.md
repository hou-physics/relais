# Relais 项目章程

- **唯一构建/测试命令**：`./scripts/check.sh`（gofmt + vet + build + test，全绿才许 commit）。
- **目录语义**：`internal/store`=事实源与查询；`internal/server`=HTTP/SSE/网页；`internal/cli`=agent 客户端；`internal/msg`=落盘格式；`internal/api`=server↔cli 唯一接口语言；`e2e/`=锚点回归；`deploy/`=部署物。
- **命名**：术语表 = spec §2（频道/信封/摘要/正文/双钥匙/串台），全项目同名；Go 包名小写单词。
- **环境**：Go ≥1.22，`CGO_ENABLED=0`；依赖白名单见计划 Global Constraints。
- **事实源矩阵**：设计=spec（`docs/superpowers/specs/`）；决策=`docs/decisions.md`（只增）；工程约定=本章程；消息数据=服务器 SQLite（本地文件是快照）。
- **IGNORE**：`dist/`（可再生构建产物，再生命令 `deploy/deploy.sh build`）、`*.db*`（本地测试数据）。
