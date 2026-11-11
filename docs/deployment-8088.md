# 8088 内网 IP 网页与回环 MCP 部署

默认复现方式：网页 http://服务器内网IP:8088，Codex/Claude MCP http://127.0.0.1:8088/mcp，
Skill 来自 GitHub proofbridge-skill-v1.1.0。无需域名或证书。
文中的 10.0.0.10 是示例，请替换为目标服务器的实际内网/VPN IP。
[AI 提示词](deploy-8088-ai-prompt.md)可直接复制到目标机；
[设计与边界](features/reproducible-8088-deployment.md)说明配置与兼容关系。

## 1. 固定源码与构建

准备 Linux、与 go.mod 匹配的 Go、Node 20+、npm、Caddy 和 systemd。

```sh
git clone --branch proofbridge-8088-20261111 https://github.com/Atingaii/ProofBridge.git
cd ProofBridge
git rev-parse HEAD
# 记录完整 commit，其他机器复现时 git checkout <该 commit>。
cd frontend
npm ci
cd ..
make frontend-8088
CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/codex-bridge .
```

frontend-8088 固定动态编排和官方 Codex 模型选择开关；重新普通构建会取消开关。
HTTP UUID 修复及简短 MCP 提示词在源码中，Go 二进制包含重建后的页面。

## 2. 安装与私有配置

```sh
sudo mkdir -p /opt/proofbridge/bin /opt/proofbridge/configs /opt/proofbridge/data
sudo install -m 755 bin/codex-bridge /opt/proofbridge/bin/codex-bridge
sudo install -m 600 configs/replica.yaml.example /opt/proofbridge/configs/replica.yaml
sudo install -m 600 configs/replica.env.example /opt/proofbridge/configs/replica.env
```

编辑私有 replica.env：生成随机 JWT_SECRET（openssl rand -hex 32）和新管理员密码，
保留数据库路径，并将示例内网 IP 换成目标服务器实际地址。核心值：

```ini
APP_ENV=replica
APP_HOST=127.0.0.1
APP_PORT=18089
HUB_COOKIE_SECURE=false
AUTOMATION_PUBLIC_URL=http://10.0.0.10:8088
AUTOMATION_MCP_URL=http://127.0.0.1:8088
```

AUTOMATION_PUBLIC_URL 用于网页结果/详情与邮件链接；AUTOMATION_MCP_URL 用于两客户端配置，
填写基址，不附加 /mcp。两者分离，无需复制原机的 JSON 地址重写代理。
HTTP 页面使用 HUB_COOKIE_SECURE=false；不将明文 HTTP 内网方案直接暴露到公网。
在安装目录初始化新账户：

```sh
cd /opt/proofbridge
sudo env CODEX_BRIDGE_ENV_FILE=/opt/proofbridge/configs/replica.env ./bin/codex-bridge user
```

从源码目录安装 deploy/systemd-hub-8088.service 为 /etc/systemd/system/proofbridge-hub.service，
执行 sudo systemctl daemon-reload 和 sudo systemctl enable --now proofbridge-hub。
Hub 仅监听回环 18089，数据与 JWT 独立于源服务器，不复制其账号、历史或模型登录。

## 3. HTTP 8088 代理

将源码 deploy/Caddyfile 安装到 /etc/caddy/Caddyfile。
执行 sudo systemctl edit caddy 添加：

```ini
[Service]
Environment=PROOFBRIDGE_SITE_ADDRESS=http://:8088
Environment=PROOFBRIDGE_UPSTREAM=127.0.0.1:18089
```

明确使用 http://:8088，使同一个入口既支持内网 IP，也支持服务器本机 127.0.0.1。
保持 Host 和转发头，不添加针对 /mcp 的 Host 改写。
验证后重启 Caddy（仅目标新服务器）：

```sh
sudo env PROOFBRIDGE_SITE_ADDRESS=http://:8088 PROOFBRIDGE_UPSTREAM=127.0.0.1:18089 caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
sudo systemctl daemon-reload
sudo systemctl enable --now caddy
sudo systemctl restart caddy
```

目标服务器防火墙仅向需要访问的内网/VPN 放行 TCP 8088；无需开放 18089。
浏览器打开 http://10.0.0.10:8088；服务器本机检查 http://127.0.0.1:8088/health。
Caddy 变量属于代理进程，不会自动从 replica.env 加载。

## 4. MCP 与 Skill

在新站个人中心复制配置，自动生成如下结构，Token 来自新站当前账户：

```toml
[mcp_servers.proofbridge]
url = "http://127.0.0.1:8088/mcp"
http_headers = { Authorization = "Bearer <新站自己的Token>" }
```

Codex/Claude 运行在这台服务器上时，127.0.0.1 可直接使用。
若客户端运行在其他电脑，先在那台电脑建立本地转发：

```sh
ssh -N -L 127.0.0.1:8088:127.0.0.1:8088 user@10.0.0.10
```

保持转发连接，再使用上面的 MCP 配置。若客户端 8088 已占用，可用本地 18088 转发到
服务器 8088，并仅将该客户端 MCP URL 改成 http://127.0.0.1:18088/mcp。
没有同机运行或本地转发时，其他电脑上的 127.0.0.1 不会自动连接服务器。

Skill 安装命令仍由新站生成，两层下载均来自固定 GitHub tag，不随内网 IP 变化。
执行安装提示词，只调用 proofbridge_get_context 与 proofbridge_list_endpoints 做只读检查。
不要把源站个人 Token 写进文档或复用到目标站。

## 5. Bridge、模型与下载

在新站创建端点注册令牌，使用新站生成的安装命令接入真实 Bridge。
选择明确的项目目录与已有 CLI 登录；Hub 账户不提供模型凭据。
源机当前架构 Hub 可直接作为同平台下载；其他平台从相同 commit 构建，置于
/opt/proofbridge/bin/downloads/codex-bridge-<linux|darwin>-<amd64|arm64>。
仅构建需要的平台；显式需要全部四平台时运行 make bridge-downloads。
HUB_BRIDGE_DOWNLOAD_URL 留空，下载和安装脚本应使用目标站 IP，不指向源机。
SMTP 独立按[环境变量](dev-workflow.md#environment-variables)配置，默认不发送邮件。

## 6. 验收、原服务保护与回滚

- 独立目标部署不修改源机现有 8088 的代理、Hub、Bridge、配置与数据库。已占用 8088 时先识别进程，不直接覆盖。
- 健康、登录、页面资源、动态编排和官方模型选择正常；原生问答依赖支持 v3 的 Bridge。
- 从内网 IP 打开个人中心，MCP 仍为 http://127.0.0.1:8088/mcp；Skill 为 GitHub 固定 tag。
- 实际从新站下载目标平台文件并核对；MCP 调用两项只读工具；结果链接回到内网 IP 网页。
- 真实任务使用目标 Bridge 与可用模型验收，同一 run 的追加消息与问答保持连续；未验证项如实列出。
- 升级前备份数据库、JWT、配置和二进制，候选端口验收后再切换；回滚恢复旧二进制与配置，保留数据和 JWT。

## 可选 HTTPS 部署

如需其他电脑直接通过网络访问 MCP，可另选 HTTPS：Caddy 站点改为实际 HTTPS 地址，
HUB_COOKIE_SECURE=true，AUTOMATION_PUBLIC_URL=同一地址，清空 AUTOMATION_MCP_URL
或设置为另一个可达 HTTPS 基址。私有 CA 须安装到客户端信任库。
默认文档采用内网 HTTP 网页与回环 MCP，不要求此可选方案。
