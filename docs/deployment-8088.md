# 8088 动态版本的独立部署

此版本把源机动态前端和配套后端纳入同一份源码，使用 Caddy 直接代理 Hub。
源机原有 IP、回环 MCP 响应替换和静态资源绝对路径无需搬迁。
[AI 部署提示词](deploy-8088-ai-prompt.md)可直接复制到目标机。
设计与兼容边界见[功能说明](features/reproducible-8088-deployment.md)。

## 固定源码与构建

```sh
git clone --branch proofbridge-8088-20261111 https://github.com/Atingaii/ProofBridge.git
cd ProofBridge
git rev-parse HEAD
# 保存以上完整 commit；后续机器使用 git checkout <完整 commit>。
cd frontend
npm ci
cd ..
make frontend-8088
CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/codex-bridge .
```

Go 版本以 go.mod 为准，Node 20+。构建目标固定 VITE_LEADING=true 和
VITE_OFFICIAL_CODEX_MODELS=true；重新执行普通前端构建会取消这两个开关。
源机 UUID 与简短接入提示词修复已在源码中，构建输出包含于 Go 二进制。

## Hub 服务与私有配置

安装到 /opt/proofbridge，保留 bin/、configs/、data/ 三个目录。
将 bin/codex-bridge、configs/replica.yaml.example、configs/replica.env.example
复制到对应目录。后两者分别复制为 replica.yaml 和 replica.env。
将环境文件权限设为 600，填写随机 JWT_SECRET（openssl rand -hex 32）、新管理员密码，
AUTOMATION_PUBLIC_URL 使用真实客户端可达的 HTTPS URL，例如 https://bridge.example.com:8088。
在 /opt/proofbridge 下初始化账户：

```sh
CODEX_BRIDGE_ENV_FILE=/opt/proofbridge/configs/replica.env ./bin/codex-bridge user
```

安装 deploy/systemd-hub-8088.service 为 /etc/systemd/system/proofbridge-hub.service，
执行 systemctl daemon-reload 和 systemctl enable --now proofbridge-hub。
默认上游只监听 127.0.0.1:18089；工作目录改变时同步修改配置、路径和 unit。
SMTP 按[环境变量与邮件设置](dev-workflow.md#environment-variables)独立配置；默认没有发送能力。

## Caddy 入口

安装 Caddy 后，将 deploy/Caddyfile 安装到 /etc/caddy/Caddyfile。
使用 systemctl edit caddy 添加环境（这两个值不会从 replica.env 自动加载）：

```ini
[Service]
Environment=PROOFBRIDGE_SITE_ADDRESS=https://bridge.example.com:8088
Environment=PROOFBRIDGE_UPSTREAM=127.0.0.1:18089
```

先用同样的环境运行 caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile，
再执行 systemctl daemon-reload 和 systemctl restart caddy。
DNS 指向目标机；自动签发公网证书时按 Caddy 要求开放挑战端口或配置已有证书。
若使用 443，去掉两个 URL 中的 :8088；若需要 HTTP 8088 兼容入口，可另设
 http://bridge.example.com:8088 站点重定向到正式 HTTPS 站点。
内网地址可在站点块使用 tls internal，并将 Caddy 根证书安装到所有客户端信任库。
未设置变量时默认 localhost 和上游 8088，沿用常规 Hub 布局；独立部署务必设置上面的环境变量。
localhost 会使用本地证书；不能把它作为其他机器的 MCP URL。
仅做 LAN HTTP 页面测试时，可显式使用 http://目标地址:8088 并关闭安全 Cookie，
但远程 MCP 仍需 HTTPS。无需放宽 Hub 的 TLS 校验，也不要复制旧代理的 JSON 重写。

Caddy 保留外部 Host 并设置转发头；Hub 返回本站下载地址，AUTOMATION_PUBLIC_URL
负责 MCP 与结果地址。Skill 安装脚本固定 GitHub tag，不随站点变化。
参考 [Caddy 环境替换](https://caddyserver.com/docs/caddyfile/concepts#environment-variables)
与[反向代理](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)。

## Bridge 下载与接入

当前架构的 Hub 本身可用于同平台下载。其他平台从与此部署相同的 commit 构建后，
放在 /opt/proofbridge/bin/downloads/codex-bridge-<linux|darwin>-<amd64|arm64>。
仅按需要构建目标平台，例如 CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build
 -ldflags "-s -w" -o bin/downloads/codex-bridge-linux-arm64 .；全部四平台可显式运行 make bridge-downloads。
不要设置指向旧服务器的 HUB_BRIDGE_DOWNLOAD_URL。
在新站创建账户与端点注册令牌后，使用新站生成的端点安装命令。
CLI 登录和项目权限保留在执行端；Hub 登录不会提供模型凭据。

## 验收与回滚

- 运行 npm test、关键浏览器回归、go test ./...、make doc-lint；健康地址为 /health。
- 登录新站，确认没有固定轮次选择器，动态编排与官方 Codex 选择可见；原生问答还依赖支持 v3 的 Bridge。
- 在新站个人中心复制 Codex/Claude 两套提示词：MCP URL 必须为新站 /mcp，Skill URL 必须为 GitHub 固定 tag，Token 属于新站当前账户。
- 检查 /install.sh 与实际目标平台下载能成功返回；不以页面上存在下载按钮代替下载验收。
- 使用真实 MCP 客户端调用 proofbridge_get_context、proofbridge_list_endpoints；再用支持的真实模型验收同一 run 的消息与问答。
- 结果与详情链接必须回到新站；不回显 Token。供应商 500、模型权限缺失或端点离线分别记录，不能称为模型通过。
- 升级前备份 SQLite（停 Hub 后复制或使用 SQLite backup）、JWT、配置及旧二进制。验证候选服务后切流量。
  回滚恢复旧二进制与对应配置后重启；保留数据和 JWT，除非恢复数据库已明确获准。

源码版本不包含源机历史、模型登录或邮件凭据，也不承诺逐字节相同的前端 hash。
