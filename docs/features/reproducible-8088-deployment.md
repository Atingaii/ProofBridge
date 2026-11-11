# 可复现的 8088 部署

## 目标与边界

将源机动态页面、简短接入提示词、HTTP UUID 修复和配套 Hub 固化为可下载的源码版本。
保留单二进制、Hub SQLite 与反向 Bridge；不迁移源机数据库、Token、模型登录、SMTP 凭据或私人工作目录。
不切换源机正在运行的服务。其他服务器复现功能与呈现，账户、项目和模型可用性由目标机单独配置。

## 数据与协议

不增加 API、WebSocket 帧或数据库迁移。使用现有 AUTOMATION_PUBLIC_URL 生成 MCP 配置和结果链接；
Bridge 下载仍由请求外部 Host 和转发协议生成，Skill 固定 GitHub proofbridge-skill-v1.1.0。
新部署使用 Caddy 直接代理嵌入 UI 的 Hub，取消旧机器上针对 JSON 的地址替换和外部静态目录。
现有会话保持 sid/runID 和消息幂等标识。

## 实施步骤

1. 收录源机 frontend 源码补丁、UUID 回归，使用 e3d6efd 基线（已含 Skill GitHub 分发后端）。
2. 增加 frontend-8088 构建目标，固定 VITE_LEADING 与 VITE_OFFICIAL_CODEX_MODELS。
3. 参数化 Caddy 站点和上游，增加独立 Hub 配置、环境和 systemd 模板。
4. 提供部署步骤、AI 提示词、检查清单及回滚方法；关联主文档。
5. 重建嵌入资源，执行前端、Go、文档门禁，提交并推送专用分支。

## 退出门禁

前端检查及关键浏览器回归通过；Go 全量测试、编译与文档检查通过。
在独立临时数据目录验证代理健康、登录、MCP 地址、下载命令和 Skill 来源。
真实模型往返需目标 Bridge 和凭据，不能由 HTTP 检查代替。

## Reviewer Q&A

- 为什么不是运行二进制的 e375f577？e3d6efd 在其后加入已由源机代理补齐的 GitHub Skill 分发，避免复制响应重写层。
- 是否字节相同？不是；源码和构建配置可追溯，构建时间与生成资源 hash 会不同。
- 只有 HTTP IP 怎么办？普通 LAN HTTP 可浏览；远程 MCP 仍要求 HTTPS，采用内网证书并信任 CA 或提供受信域名。
- 是否保证供应商推理成功？不保证；源机已有 Claude/DeepSeek HTTP500 记录，目标机必须独立验证。
