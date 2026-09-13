# Forge 开发与维护指南

本文件指导 AI 编码代理和开发者维护 **Forge CLI 本身**。进入仓库后先阅读本文件，再按任务查看相关模块和测试。

## 项目定位与范围

Forge 是公司标准 Monorepo 的 Go 脚手架编排工具，同时面向人类开发者和 AI 编码代理。

- V1 命令：`create`、`doctor`、`describe`、`validate`、`version`。
- 官方生成器：Next.js 使用 `npx create-next-app@latest`；Spring Boot 使用 Spring Initializr；Python 使用 `uv init` 和 `uv lock`。
- Forge 负责目录规范、元数据、Docker、GitLab/GitHub CI、README 和代理指南。
- 不自行重写生态脚手架。新增命令、技术栈或部署能力时，先明确本次需求，不顺带实现未来功能。

## 开发、运行与测试

开发需要 Go 1.25+。以下命令在仓库根目录执行：

已安装 Task v3 时，可使用根目录 `Taskfile.yml`：`task` 列出命令，
`task run -- version --json` 运行 CLI，`task build` 构建到 `dist/`，
`task check` 依次运行全量测试和 vet，`task test:race` 执行竞态检测。
`task clean` 清理 `dist/`、`.task/`、`coverage.out` 和根目录的
`forge` / `forge.exe`，保留全局 Go 缓存与依赖缓存。
指定测试可用 `task test -- -v -count=1 ./internal/runner`；传入参数会替换
默认的 `./...`，全量测试加参数时需显式保留该包模式。

```bash
# 下载依赖；首次运行需要网络
go mod download

# 直接运行 CLI，无需安装
go run ./cmd/forge --help
go run ./cmd/forge version --json

# 完整测试、竞态检测、静态检查
go test ./...
go test -race ./...
go vet ./...

# 模块测试及指定测试；-count=1 禁用测试结果缓存
go test -v ./internal/runner
go test -v -count=1 ./internal/scaffold -run TestExistingTargetNeverTouched

# 本机二进制；Windows 使用 -o forge.exe
go build -o forge ./cmd/forge

# 构建六个平台的发布包及 dist/SHA256SUMS
go run ./tools/release --version v0.1.0 --output dist
```

测试以受控的 HTTP/进程边界替代真实生态下载，不要求安装 Node、Java、uv 或 Docker。HTTP 集成测试需要监听本地回环端口；若沙箱禁止监听，应在允许该操作的环境运行，不要删除或跳过测试来制造通过结果。

Forge 仓库本身不是生成的业务项目，没有 `forge.yaml`。不要用 `forge validate` 代替本仓库的 `go test`；该命令用于校验 Forge 创建的项目。

## 代码导航

| 路径 | 职责 |
| --- | --- |
| `cmd/forge/main.go` | 程序入口、信号取消、构建版本信息及退出码 |
| `internal/cli/` | Cobra 命令、Huh 交互、JSON/人类输出；业务逻辑放在服务层 |
| `internal/project/` | 选项与默认值、组件模型、严格解析和写入 `forge.yaml` |
| `internal/apperror/` | 稳定错误码、结构化错误和退出分类 |
| `internal/runner/` | 核心程序的统一进程执行、工作目录、环境、流与跨平台取消 |
| `internal/doctor/` | 按项目或选择项检查工具及版本兼容性 |
| `internal/generator/` | 官方生成器调用、Initializr 下载、安全 ZIP 解压、初始配置调整 |
| `internal/scaffold/` | 创建预检、名称预留、暂存生成、模板写入、验证与原子发布 |
| `internal/templates/` | `embed` + `text/template`；返回文件内容，不直接写磁盘 |
| `internal/validate/` | 离线、只读的项目结构和配置校验 |
| `tools/release/` | 开发用发布工具：交叉编译、归档、校验和 |
| `.github/workflows/` | Forge 自身的多系统测试与发布包构建 |
| `skills/forge/SKILL.md` | 指导代理使用 Forge CLI 的可复用技能 |

测试与对应实现放在同一包内。修改前先读相关测试，修复缺陷时添加能复现问题的回归测试；避免只断言实现文本或复制实现逻辑的测试。

## 修改时必须保持的契约

### CLI 与代理兼容性

- JSON 模式 stdout 只输出一个 JSON 文档，包括参数解析失败；子进程日志和诊断使用 stderr。
- `--json`、`--non-interactive` 和非终端输入均不触发交互。显式选项优先于交互默认值。
- 退出码：`0` 成功，`1` 运行/依赖/项目失败，`2` 无效 CLI 参数。扩展错误时复用结构化错误约定。
- `--output` 是已存在的父目录，目标为 `parent/NAME`。不要悄悄改变这一语义。
- 调整选项、默认值或 JSON 字段时，同步相关 CLI 测试、README 和操作技能。

### 文件安全与跨平台行为

- 不覆盖已有目标，包括空目录、符号链接和生成期间并发创建的同名目录。
- 在当前创建操作拥有的暂存目录中工作；校验成功后使用不覆盖目标的原子发布。不得用普通覆盖重命名或部分复制作为降级方案。
- 失败清理只处理本次操作拥有的资源；不得盲目删除已有项目或其他进程的预留文件。
- 核心文件操作使用 Go API，外部进程统一经过 `runner`。文件系统路径使用 `filepath`；元数据和模板文件名使用经过校验的可移植相对路径。
- Windows 的 `npm.cmd`/`npx.cmd` 引号与取消逻辑有专门测试，不要直接替换成 shell 字符串拼接。
- 目标平台为 darwin/windows/linux × amd64/arm64；最终发布二进制禁用 CGO。

### 元数据、生成器与校验

- `forge.yaml` 当前 schemaVersion 为 1；保持未知字段、重复键、多文档、非法路径及组件目录重叠的拒绝行为。
- 组件位置取自项目模型，避免在服务层到处硬编码 `frontend`/`backend`。
- V1 Java 配置为 21，生成前端的 Node 基线为 22，初始 Python 应用为 3.12。修改版本支持时同步 doctor、生成器、校验、模板和文档。
- Initializr 使用 `application/vnd.initializr.v2.3+json`。旧版媒体类型会把现代版本转换为不能解析的 `.RELEASE` 坐标，不要退回旧格式或自行拼接版本后缀。
- 修改 Next.js 配置时必须保留类型导入与配置字段，不能把“第一个花括号”当成配置对象。
- ZIP 解压继续拒绝路径逃逸、符号链接、Windows 保留名、重复路径和超限内容。
- `validate` 保持离线只读；结构校验通过不等于应用构建、容器启动或云端 CI 成功。

### 模板与 CI

- **本文件**用于维护 Forge。生成业务项目的代理指南来自 `internal/templates/assets/AGENTS.md.tmpl`；需要改变生成内容时编辑该模板和相应测试。
- 生成项目的 GitLab 文件位于 `.gitlab/*-ci.yml`，GitHub 文件位于 `.github/workflows/*-ci.yml`；与 Forge 自身的 `.github/workflows/` 区分。
- 保持组件级路径过滤：仅前端变化不构建后端，反之亦然；新增分支、首次提交和合并请求的规则不能错误回落为全量构建。
- 镜像构建可在合并请求和功能分支执行；注册表认证和推送只在允许的默认分支推送流程执行，使用不可变提交标签。
- 前端生产构建避免重复；后端镜像消费构建任务的 `target/*.jar`，不在 Docker 中再次执行 Maven 编译。
- 同步检查 Dockerfile 与 `.dockerignore`，避免排除后端 JAR。Python 镜像与锁定解释器一致，运行时直接使用已构建的虚拟环境。
- 模板变更同时检查组件选择、CI 提供方和 Docker 开关的组合；现有集成测试覆盖 42 种非空组合。

## 验证与交付习惯

按改动范围先运行相关包测试。跨模块或行为变更完成后运行全量测试和 vet；涉及进程、并发或取消时运行 race 测试；涉及平台或发布时验证相应目标编译。

纯文档修改核对内容、链接与实际命令即可，不必重复全部代码测试。官方生成器参数或框架配置变化时，在临时目录补真实生成与构建验证；模板结构测试不能替代这些检查。

报告实际执行的检查及未验证项。Windows 原生测试、Docker 镜像运行和云端 CI 需要对应环境；不要把交叉编译或历史测试记录描述为本次实跑。

保持相关文档同步：

- [README.md](README.md)：使用方式、默认值、工具要求和 JSON 契约。
- [docs/architecture.md](docs/architecture.md)：模块边界、安全模型和扩展点。
- [docs/verification.md](docs/verification.md)：带日期的实际验证记录和已知限制。
- [设计文档](docs/superpowers/specs/2026-09-06-forge-design.md)：V1 设计背景；后续明确批准的需求可更新该设计。
- [操作技能](skills/forge/SKILL.md)：代理使用 Forge 的流程。

下面的 `claude-mem-context` 区块由记忆工具维护；人工维护说明写在区块外。

<claude-mem-context>
# Memory Context

# [forge] recent context, 2026-09-09 9:51pm GMT+8

No previous sessions found.
</claude-mem-context>
