# 实验室服务器命令行部署

默认实验室方式：网页 http://服务器内网IP:8088，Codex/Claude MCP http://127.0.0.1:8088/mcp，
Skill 来自 GitHub proofbridge-skill-v1.1.0。该内网方案无需域名或证书；VPN/防火墙限制访问范围。
文中的 10.0.0.10 是示例，请替换为目标服务器的实际内网/VPN IP。
[设计与边界](features/reproducible-8088-deployment.md)说明配置与兼容关系。

## 先选客户端位置

| 场景 | 浏览器 | Codex/Claude 的 MCP |
| --- | --- | --- |
| CLI 运行在服务器，包括 SSH 远程终端 | 内网 IP:8088，外网用户先连 VPN | 直接访问服务器 127.0.0.1:8088 |
| CLI 运行在个人电脑 | VPN 后访问内网 IP:8088 | VPN 后再建立 SSH 本地转发，客户端连接本机 127.0.0.1:8088 |
| 有受信任 HTTPS 地址 | HTTPS 域名/IP | 可直接使用 HTTPS MCP，不需要回环覆盖 |

VPN 提供到实验室网络的路由，不会把个人电脑的回环指向服务器。网页/结果地址与 MCP 地址
分别由 AUTOMATION_PUBLIC_URL、AUTOMATION_MCP_URL 配置；不依据访问页面的 Host 自动改写。
SSH 转发参考 [OpenSSH -L](https://man.openbsd.org/ssh#L)。

## 1. 固定源码与构建

准备 Linux、与 go.mod 匹配的 Go、Node 22+、npm、Caddy 和 systemd。

```sh
git clone --branch main https://github.com/Atingaii/ProofBridge.git
cd ProofBridge
git rev-parse HEAD
# 记录完整 commit，其他机器复现时 git checkout <该 commit>。
cd frontend
npm ci
cd ..
make frontend-8088
make build
PB_SOURCE_DIR="$PWD"
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

在初始化配置后安装服务。PB_SOURCE_DIR 是第 1 步保存的源码绝对路径：

```sh
sudo install -m 644 "$PB_SOURCE_DIR/deploy/systemd-hub-8088.service" /etc/systemd/system/proofbridge-hub.service
sudo systemctl daemon-reload
sudo systemctl enable --now proofbridge-hub
sudo systemctl status proofbridge-hub --no-pager
curl -fsS http://127.0.0.1:18089/health
```
Hub 仅监听回环 18089，数据与 JWT 独立于源服务器，不复制其账号、历史或模型登录。

## 3. HTTP 8088 代理

先检查目标机是否已有网站或 8088 服务；本节用于新的独立 Hub，已有 Caddy 网站应在现有配置中合并站点。
Caddy 与 Hub 分别读取各自的环境文件；不把代理变量仅写入 Hub 的 replica.env。

```sh
sudo install -d /etc/caddy /etc/systemd/system/caddy.service.d
# 已有配置先备份，不覆盖其他站点。
sudo cp -a /etc/caddy/Caddyfile /etc/caddy/Caddyfile.before-proofbridge
sudo install -m 644 "$PB_SOURCE_DIR/deploy/Caddyfile" /etc/caddy/Caddyfile
sudo install -m 644 "$PB_SOURCE_DIR/configs/caddy-lab.env.example" /etc/caddy/proofbridge.env
sudo install -m 644 "$PB_SOURCE_DIR/deploy/systemd-caddy-lab.conf" /etc/systemd/system/caddy.service.d/proofbridge.conf
sudo env PROOFBRIDGE_SITE_ADDRESS=http://:8088 PROOFBRIDGE_UPSTREAM=127.0.0.1:18089 caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
sudo systemctl daemon-reload
sudo systemctl enable --now caddy
sudo systemctl restart caddy
curl -fsS http://127.0.0.1:8088/health
```

如 Caddy 首装没有默认 Caddyfile，可跳过备份命令。代理环境文件默认：

```ini
PROOFBRIDGE_SITE_ADDRESS=http://:8088
PROOFBRIDGE_UPSTREAM=127.0.0.1:18089
```

明确 HTTP :8088 使一个入口同时接收 LAN IP 与服务器回环访问；不添加 /mcp Host 或 JSON 改写。
更改端口需同步 Caddy 站点、网页/MCP 基址；更改上游还需同步 Hub 的 APP_PORT。
环境变量替换参考 [Caddy 官方说明](https://caddyserver.com/docs/caddyfile/concepts#environment-variables)。

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
ssh -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 \
  -L 127.0.0.1:8088:127.0.0.1:8088 user@10.0.0.10
```

若学校/实验室要求 VPN，先连 VPN，再执行转发；保留正常 SSH 主机密钥校验。
ExitOnForwardFailure 使端口绑定失败时立即退出，保活有助于发现断开的 VPN。
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

## 升级与回滚命令

以下命令针对维护窗口中的目标 Hub，不应用到其他服务器的服务：

```sh
cd "$PB_SOURCE_DIR"
git pull --ff-only origin main
npm --prefix frontend ci
make frontend-8088
make build
BACKUP_DIR="/opt/proofbridge/backups/$(date -u +%Y%m%dT%H%M%SZ)"
sudo install -d -m 700 "$BACKUP_DIR"
# 等待任务结束，再停止 Hub；停止后复制 SQLite，避免不完整数据库快照。
sudo systemctl stop proofbridge-hub
sudo cp -a /opt/proofbridge/configs "$BACKUP_DIR/configs"
sudo cp -a /opt/proofbridge/data "$BACKUP_DIR/data"
sudo cp -a /opt/proofbridge/bin/codex-bridge "$BACKUP_DIR/codex-bridge"
sudo install -m 755 bin/codex-bridge /opt/proofbridge/bin/codex-bridge
sudo systemctl start proofbridge-hub
curl -fsS http://127.0.0.1:8088/health
```

保留 BACKUP_DIR 的实际值。启动或健康失败时，恢复旧二进制后重新启动；不要自动重置管理员密码
或更换 JWT。若旧二进制与新数据库不兼容，停止 Hub 后经确认恢复对应数据备份，避免丢弃新任务记录。
Caddy、下载平台文件、连接的 Bridge 与 CLI 均不需要随每次 Hub 更新一起重启或升级。

## 可选 HTTPS 部署

如需其他电脑直接通过网络访问 MCP，可另选 HTTPS：Caddy 站点改为实际 HTTPS 地址，
HUB_COOKIE_SECURE=true，AUTOMATION_PUBLIC_URL=同一地址，清空 AUTOMATION_MCP_URL
或设置为另一个可达 HTTPS 基址。私有 CA 须安装到客户端信任库。
默认文档采用内网 HTTP 网页与回环 MCP，不要求此可选方案。
