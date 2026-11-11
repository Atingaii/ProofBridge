# GitHub 分发 ProofBridge Skill

## Goals

实验室 Hub 使用私有 CA 时，个人中心的安装脚本与脚本内二次下载不应访问 Hub。
两层均从官方 `Atingaii/ProofBridge` GitHub 仓库下载，正常验证 TLS；生成的命令
固定使用同一个版本 `proofbridge-skill-v1.1.0`，不依赖 main 是否已有功能。
此专用版本标签发布后不得移动；更新安装资源应发布新的 Skill 标签。

## Non-goals

不修改 MCP URL、Token、VPN 或客户端 TLS 策略，不安装 CA，不使用 `-k` 或
`--insecure`。本改动不解决 MCP 本身访问私有 CA Hub 的信任问题，不重新测试证明、
模型路由或外发邮件，不改变 Bridge、调度、数据库或登录配置。

## Data and protocol impact

保持 `GET /api/automation/access` 的响应结构与两个 `skillInstallCommands` 键。
命令由 Hub 统一生成：安装器 URL 和文件源均为同一 GitHub 版本。
保留 `/skills/*` 作为兼容的嵌入资源路由，但新安装命令不依赖它。
在仓库加入 credential-free `skill/checksums.txt`，测试要求与嵌入 Skill 文件逐字节
计算的 manifest 一致。个人 MCP Token 只在 MCP 配置，不得传给 GitHub。

## Behavior and failure recovery

- 安装器默认 GitHub 下载；保留旧 `--hub` 参数兼容，它不决定资源下载地址。
- 测试可显式指定 fixture 资源源，限 HTTPS 或 loopback HTTP；生产生成命令
  不携带 Hub 地址或 fixture override。错误不可自动回退 Hub 或跳过证书检查。
- 保留两客户端/用户与项目目录、CLAUDE_CONFIG_DIR、受管标记、拒绝符号链接及
  非受管目录。完整下载与校验完成前不替换已安装文件；临时资源退出清理。
- 第一层 curl 失败不得继续执行安装或返回伪成功；生成命令先下载临时脚本，成功
  后执行，保留有限超时、TLS 验证和退出清理。
- GitHub 不可达时报告外网访问问题；MCP curl 60 时提示信任 CA 或配置受信任证书，
  不在 AI 提示词中授予绕过校验的权限。安装、加载和 MCP 检查分别报告。

## Implementation steps

1. 更新安装器默认资源源和固定版本，加入静态 manifest 与安装器测试。
2. Hub 使用统一 GitHub 命令生成函数；更新中英文 AI 接入提示词和回归。
3. 更新部署/操作/README/代码地图与变更契约，构建 embedded UI。
4. 完成默认离线测试、局部 TLS 回归、Go 全量与构建；独立审查后提交。
5. 将当前分支和专用 Skill 标签一起推送，再以真实 GitHub TLS 分别安装 Codex 和
   Claude（隔离 HOME），比对文件校验和并清理临时目录。标签不可用时不得报告发布成功。

## Exit gates

- 两客户端/两语言的接入配置保留原 Hub 地址，安装命令仅访问官方 GitHub。
- 私有 CA Hub 不可访问时，Skill 仍可从独立可信源完整安装；MCP信任问题单独保留。
- 安装器默认路径不请求 Hub；不携带 MCP 凭据；无 insecure 开关；校验失败保留旧安装。
- manifest 同步、原用户配置保护、双客户端目录及失败清理回归通过。
- 前端检查/构建、Go测试/构建、doc-lint和实际 GitHub 双客户端下载验证通过。

## Reviewer Q&A

**为什么不继续给两层加 -k？** 它取消传输身份验证，并且无法修复 MCP 的证书信任。

**为什么使用专用标签？** 部署版本与资源保持一致，避免 main/开发分支变动混用文件。
标签在本次资源提交后创建，和功能分支原子推送；不会触发只匹配 v* 的二进制发布。

**GitHub下载成功能否报告接入成功？** 不能。仍需客户端加载 Skill、MCP认证与两项
只读查询各自的证据；真实模型探针既有失败记录不改判。

## Local checks (2026-11-07)

安装器与命令回归、完整 assets 包、前端测试与构建、提示词类型检查、双客户端/双语
页面复制及手机宽度夹具均通过。独立审查、Go vet、Go构建及文档检查通过。
Go全量测试在 macOS 使用 `TMPDIR=/private/tmp` 的规范短临时路径通过；默认的长
临时路径触发现有Bridge测试的Unix socket长度限制和路径规范化差异，未修改断言。
页面夹具不是原生模型验收；真实GitHub下载须在版本标签发布后另验。
