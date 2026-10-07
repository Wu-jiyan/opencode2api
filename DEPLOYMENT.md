# 部署教程

本文覆盖两种部署方式，以及首次配置、验证、常见故障。所有命令均在实际环境中验证过。

- [环境要求](#环境要求)
- [方式一：下载 Release 二进制](#方式一下载-release-二进制)
- [方式二：从源码编译](#方式二从源码编译)
- [首次配置](#首次配置)
- [验证部署是否成功](#验证部署是否成功)
- [Xray 与 vless 代理池](#xray-与-vless-代理池)
- [接入聚合面板](#接入聚合面板)
- [升级与回滚](#升级与回滚)
- [安全加固](#安全加固)
- [故障排查](#故障排查)

## 环境要求

| 项目          | 要求                                                |
| ------------- | --------------------------------------------------- |
| 网关二进制    | Linux / Windows / macOS，amd64 或 arm64，无外部依赖 |
| 使用 vless 时 | 能在本地运行 Xray，或允许自动下载                   |
| 配置文件目录  | 必须可写（密码迁移、配置保存、模型缓存、Xray 下载） |
| 内存          | 常驻约 30–60 MiB；每个 Xray 进程约 20–40 MiB        |

网关自身是纯静态Go 程序，除 Xray 外不依赖任何运行时。

## 方式一：下载 Release 二进制

从 [Releases](https://github.com/Wu-jiyan/opencode2api/releases) 下载对应平台的压缩包，每个包都附带 `.sha256` 校验文件。

### 压缩包里有什么

每个平台压缩包解压后是一个自包含目录：

```text
opencode2api_v1.0.0_linux_amd64/
  opencode2api          # 对应平台的可执行文件（Windows 为 opencode2api.exe）
  config.json           # 已填好占位符，直接改就能用
  README.md
  README.zh-CN.md
  DEPLOYMENT.md         # 本文档
```

`config.json` 由 `config.example.json` 复制而来，只含占位符，**不含任何真实凭据**，可以直接编辑后启动，无需再做 `cp`。

压缩包内**不含启动脚本**。批处理文件在不同代码页下容易出现乱码，因此请直接运行二进制：

```bash
./opencode2api -config config.json        # Linux / macOS
.\opencode2api.exe -config config.json    # Windows
```

### 三个必改项

解压后打开 `config.json`，至少改这三处，否则服务无法正常使用：

| 字段             | 当前占位值                   | 改成什么                                                          |
| ---------------- | ---------------------------- | ----------------------------------------------------------------- |
| `server_keys`    | `change-this-local-key`      | 你自己的本地 API Key，客户端调用时用                              |
| `zen_keys`       | `sk-your-zen-key`            | 你的 Zen Key；或改用 `go_keys`；或设 `anonymous: true` 并清空两者 |
| `webui.password` | `change-this-admin-password` | 至少 10 位的强密码                                                |

改完直接启动即可。启用 vless 但本地没有 Xray 时也不用管，服务会在后台自动下载。

### 配置修改的生效方式

启动后有两种改配置的方式：

- **直接编辑 `config.json`** —— 服务会检测文件变化，先校验新配置再切换；校验失败则保留原配置继续运行，并在事件日志中给出原因。所以写错字段不会导致服务中断。
- **通过 WebUI 修改** —— 打开 `http://<服务器地址>:8081` → 配置中心，实时生效。

WebUI 修改同样不需要重启，只有 `listen`、`webui.listen`、`webui.enabled` 三项例外，改这三项需要重启进程。

### Linux (amd64)

```bash
VERSION=v1.0.0
ARCH=linux_amd64
curl -LO "https://github.com/Wu-jiyan/opencode2api/releases/download/${VERSION}/opencode2api_${VERSION}_${ARCH}.tar.gz"
curl -LO "https://github.com/Wu-jiyan/opencode2api/releases/download/${VERSION}/opencode2api_${VERSION}_${ARCH}.tar.gz.sha256"

# 校验完整性，务必先做这一步
sha256sum -c "opencode2api_${VERSION}_${ARCH}.tar.gz.sha256"

tar -xzf "opencode2api_${VERSION}_${ARCH}.tar.gz"
cd "opencode2api_${VERSION}_${ARCH}"
vi config.json     # 改上面三个字段
./opencode2api -config config.json
```

### Linux (arm64)

把 `ARCH` 换成 `linux_arm64`，适用于树莓派 4/5、各类 ARM 云服务器（原生构建，无模拟开销）。

### macOS

```bash
VERSION=v1.0.0
ARCH=darwin_arm64      # Apple Silicon；Intel Mac 用 darwin_amd64
curl -LO "https://github.com/Wu-jiyan/opencode2api/releases/download/${VERSION}/opencode2api_${VERSION}_${ARCH}.tar.gz"
shasum -a 256 -c "opencode2api_${VERSION}_${ARCH}.tar.gz.sha256"   # macOS 用 shasum
tar -xzf "opencode2api_${VERSION}_${ARCH}.tar.gz"
cd "opencode2api_${VERSION}_${ARCH}"
vi config.json
./opencode2api -config config.json
```

### Windows

下载 `opencode2api_<版本>_windows_amd64.zip`（ARM 机器用 `windows_arm64`），校验后解压：

```powershell
Get-FileHash "opencode2api_v1.0.0_windows_amd64.zip" -Algorithm SHA256
# 与同名 .sha256 文件里的字符串比对

Expand-Archive opencode2api_v1.0.0_windows_amd64.zip
cd opencode2api_v1.0.0_windows_amd64
notepad config.json    # 改上面三个字段
.\opencode2api.exe -config config.json
```

### 可用平台

| 平台    | amd64 | arm64 |
| ------- | ----- | ----- |
| Linux   | ✅    | ✅    |
| Windows | ✅    | ✅    |
| macOS   | ✅    | ✅    |

六个平台均在对应的**原生 runner** 上构建（Linux ARM 用 `ubuntu-24.04-arm`，Windows ARM 用 `windows-11-arm`），不做交叉编译或指令集模拟。

### systemd 常驻（Linux 推荐）

```bash
sudo mkdir -p /opt/opencode2api
sudo cp opencode2api config.example.json /opt/opencode2api/
cd /opt/opencode2api && sudo cp config.example.json config.json
sudo chmod 755 opencode2api
```

创建 `/etc/systemd/system/opencode2api.service`：

```ini
[Unit]
Description=opencode2api gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=opencode2api
Group=opencode2api
WorkingDirectory=/opt/opencode2api
ExecStart=/opt/opencode2api/opencode2api -config /opt/opencode2api/config.json
Restart=always
RestartSec=5
# 允许 Xray 子进程
KillMode=mixed
# 每个 SSE 流都占用一个文件描述符，默认 1024 的软限制在并发下会耗尽
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

```bash
sudo useradd -r -s /usr/sbin/nologin opencode2api
sudo systemctl daemon-reload
sudo systemctl enable --now opencode2api
sudo systemctl status opencode2api
sudo journalctl -u opencode2api -f
```

`WorkingDirectory` 必须是配置所在目录 —— 相对路径的 `proxyfile`、`xray_path` 都以它为基准。

## 方式二：从源码编译

需要 **Go 1.24 或更高版本**。

```bash
git clone https://github.com/Wu-jiyan/opencode2api.git
cd opencode2api
cp config.example.json config.json
go build -o opencode2api ./cmd/opencode2api
vi config.json
./opencode2api -config config.json
```

国内网络建议先配置 Go 模块镜像，否则拉依赖会很慢：

```bash
go env -w GOPROXY=https://goproxy.cn,direct
go env -w GOSUMDB=sum.golang.org https://goproxy.cn/sumdb/sum.golang.org
```

Windows 下：

```powershell
Copy-Item config.example.json config.json
go build -o opencode2api.exe ./cmd/opencode2api
.\opencode2api.exe -config config.json
```

命令行参数：

| 参数          | 默认值        | 用途                  |
| ------------- | ------------- | --------------------- |
| `-config`     | `config.json` | 配置文件路径。        |
| `-listen`     | 未设置        | 覆盖 API 监听地址。   |
| `-web-listen` | 未设置        | 覆盖 WebUI 监听地址。 |

> **前端产物说明**：`webui/dist` 已随仓库提交并内嵌进二进制，所以编译网关**不需要 Node.js**。只有修改了 `webui/src/` 下的前端源码时，才需要先重新构建：
>
> ```bash
> npm ci --ignore-scripts
> npm run build
> ```

## 首次配置

最小可用配置（匿名模式，无需任何上游 Key）：

```json
{
  "listen": "127.0.0.1:8080",
  "server_keys": ["sk-换成你自己的随机字符串"],
  "zen_keys": [],
  "go_keys": [],
  "anonymous": true,
  "webui": {
    "enabled": true,
    "listen": "0.0.0.0:8081",
    "username": "admin",
    "password": "至少10位的强密码"
  }
}
```

匿名模式下，`/v1/models` 只会列出符合匿名条件的免费模型。

### 关键字段说明

| 字段                   | 说明                                                                          |
| ---------------------- | ----------------------------------------------------------------------------- |
| `listen`               | API 监听地址。**保持 `127.0.0.1`**，除非你确实需要从外部直连                  |
| `server_keys`          | 客户端调用本网关用的 Key，与上游 Key 完全独立                                 |
| `zen_keys` / `go_keys` | 上游 Key，两个 Tier 独立成池                                                  |
| `anonymous`            | `true` 时用 `public` 凭证访问符合条件的免费模型，无需上游 Key                 |
| `prefer`               | `go`（默认）或 `zen`，决定优先尝试哪个认证 Tier                               |
| `proxies`              | 代理列表，支持 `direct` / `http://` / `https://` / `socks5://` / `socks5h://` |
| `webui.password`       | 仅首次启动使用：服务会把它转成 Argon2id 哈希写入 `password_hash` 并删除明文   |

完整字段见 `config.example.json` 与 README 的配置参考章节。

## 验证部署是否成功

### 1. 健康检查（无需认证）

```bash
curl -s http://127.0.0.1:8080/healthz
```

正常响应类似：

```json
{
  "status": "ok",
  "ready": true,
  "version": "v1.0.0",
  "models": { "status": "ready", "total": 42, "exposed": 42, "zen": 30, "go": 12 },
  "keys": { "zen": 1, "go": 0, "total": 1, "anonymous": false },
  "proxies": { "total": 3, "healthy": 3, "unhealthy": 0 }
}
```

判定规则：

| 字段                     | 含义                                                   |
| ------------------------ | ------------------------------------------------------ |
| `ready: true` + HTTP 200 | 完全就绪                                               |
| `status: degraded`       | 可用但有隐患，`issues` 会列出具体原因                  |
| HTTP 503                 | 阻塞性故障：模型目录 `pending`/`empty`，或没有健康代理 |
| `models.status: stale`   | 使用了过期缓存但仍可服务，整体 `status` 为 `degraded`  |

常见 `issues` 取值：`model_catalog_pending`、`model_catalog_empty`、`model_catalog_stale`、`no_upstream_keys`、`no_healthy_proxies`。

`/healthz` 不触发任何网络请求，也不返回 Key 或代理地址，可以放心用于探活。

### 2. 获取模型列表

```bash
curl -s http://127.0.0.1:8080/v1/models \
  -H "Authorization: Bearer sk-你的本地KEY"
```

### 3. 发起一次真实推理

```bash
MODEL_ID=$(curl -s http://127.0.0.1:8080/v1/models \
  -H "Authorization: Bearer sk-你的本地KEY" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "使用模型：$MODEL_ID"

curl -s http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-你的本地KEY" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"$MODEL_ID\",\"messages\":[{\"role\":\"user\",\"content\":\"说一句话\"}]}"
```

### 4. 检查 WebUI

浏览器打开 `http://<服务器地址>:8081`，用配置的用户名密码登录，四个页面均应有数据：

- **概览** —— 请求量、成功率、P95 延迟、代理节点与 Key 池
- **路由诊断** —— 模型原生协议、路由顺序、匿名资格判断依据
- **配置中心** —— 在线修改配置
- **事件日志** —— 实时结构化日志

## Xray 与 vless 代理池

只有启用 `vless.enabled` 时才需要 Xray。

### 自动下载（默认开启）

```json
"vless": {
  "enabled": true,
  "subscription": "https://你的订阅地址",
  "auto_download_xray": true,
  "xray_version": ""
}
```

服务启动时若找不到 Xray 可执行文件，会**在后台自动下载**官方版本，依次尝试多个镜像源，第一个通过 SHA-256 校验的即被采用。下载的可执行文件落在配置目录下的 `bin/xray/`，重启后直接复用，不会重复下载。

| 字段                 | 说明                                                           |
| -------------------- | -------------------------------------------------------------- |
| `auto_download_xray` | 缺 Xray 时是否自动下载，默认 `true`。设为 `false` 则完全不联网 |
| `xray_version`       | 固定版本，如 `"v26.3.27"`。留空表示自动取最新 Release          |
| `xray_path`          | 指定已有 Xray 的路径。相对路径基于配置目录解析                 |

安全设计：

- 下载的压缩包会与官方发布的 `.dgst` 文件核对 **SHA-256**，校验不通过绝不落盘
- 校验和文件来自**同一个镜像**，镜像无法在不同时篡改二进制的情况下伪造校验和
- 先写入 `.download` 临时文件再原子改名，中断不会留下半个可执行文件
- 只解压 `xray` / `xray.exe` 单个文件，跳过 `geoip.dat` / `geosite.dat`（约 30 MB，且本网关生成的配置不引用它们）
- 全程在后台 goroutine 中进行，**不会阻塞网关启动**；下载期间 vless 池显示为空，日志可见进度

支持的平台：Linux / Windows / macOS 的 amd64、arm64、386、arm(v7)。

镜像源按实测持续吞吐量排序，而非 ping 延迟（能快速响应 HEAD 的主机未必能快速传输载荷）：

1. `gh-proxy.com`（约 0.5 MiB/s，实测最快）
2. `ghfast.top`（约 0.09 MiB/s）
3. `ghproxy.net`（约 0.01 MiB/s，很慢）
4. 官方 `github.com` 直连（国内常超时，兜底）

下载过程可在事件日志中查看（WebUI → 事件日志，筛选 `xray`），或直接看标准输出：

看到 `xray installed` 表示成功；`automatic xray download failed` 表示所有镜像都失败，此时可手动下载后用 `xray_path` 指定：

```bash
mkdir -p bin/xray && cd bin/xray
curl -LO https://github.com/XTLS/Xray-core/releases/latest/download/Xray-linux-64.zip
unzip Xray-linux-64.zip xray && chmod +x xray && rm Xray-linux-64.zip
```

### 节点来源：订阅或固定节点

两者至少填一项，可以只填其一，也可以同时使用（固定节点优先占用前面的槽位）：

```json
"vless": {
  "enabled": true,
  "subscription": "https://你的订阅地址",
  "nodes": [],
  "preferred_domains": [],
  "endpoints": []
}
```

只配固定节点时服务不会发起任何订阅请求，订阅失效也不影响池子工作：

```json
"vless": {
  "enabled": true,
  "subscription": "",
  "nodes": ["vless://uuid@ct.example.com:443?security=tls&sni=edn2.example.com&type=ws&host=edn2.example.com&path=%2F%3Fed%3D2560"],
  "preferred_domains": ["cf.example.com", "ct.example.com", "cu.example.com"],
  "endpoints": ["edn2.example.com", "edn3.example.com", "edn4.example.com"],
  "count": 7,
  "port_base": 24100
}
```

| 字段                | 说明                                                           |
| ------------------- | -------------------------------------------------------------- |
| `nodes`             | 固定 `vless://` 链接，每行一条                                 |
| `preferred_domains` | 入口域名，替换固定节点的入口地址，每个值生成一个候选           |
| `endpoints`         | 服务入口，按候选序号轮流分配；只有 Host 与 SNI 会变，UUID 不变 |

`preferred_domains` 与 `endpoints` 的区别：前者决定"从哪个边缘地址进场"（出口 IP由此变化），后者决定"落到哪个服务入口"（各入口通常各有请求额度）。地址是字面 IP 且不在 `preferred_domains` 中的节点会原样使用，不会被改写。

端口按候选数规划即可 —— 用固定节点时 `count` 建议设成 `preferred_domains` 的长度，多出来的槽位没有候选可用。固定节点候选会按实测延迟排序，快的占用靠前槽位，连不上的排最后而不是被丢弃，以免池子缩到小于 `count`。

### 端口规划

vless 池会占用 `port_base` 到 `port_base + count - 1` 的一段连续端口。默认值：

```json
"vless": { "port_base": 24000, "count": 24 }
```

即占用 24000–24023。修改时需确保这段范围未被宿主机其他服务占用，且 `port_base + count <= 65535`。

### 轮换与连接复用

- 每 `refresh_seconds`（默认 300 秒）按 `rotate_batch`（默认 3）重建一批节点，实现滚动更新，池不会整体中断
- 新节点先在临时端口验证可连，通过后才替换旧进程
- 若使用 vless 并希望**每次请求都换出口 IP**，设置：

```json
"performance": { "connection_rotation_seconds": 300 }
```

代价是每条新连接多约 0.6–1.3 秒（Cloudflare trace 类目标更高）。设为 `0` 关闭轮换。

## 接入聚合面板

本网关定位是"作为上游渠道被 new-api 等聚合面板调用"，不重复实现面板已有的统计功能。

聚合面板侧配置：

- **接口地址**：`http://<网关地址>:8080`
- **密钥**：填 `server_keys` 中的一个
- **模型**：先调用 `/v1/models` 获取，或直接手动填写模型 ID

面板从每次响应的 `usage` 字段读取 Token 用量，网关原样透传上游响应，不做任何统计改写。

## 升级与回滚

下载新版本的压缩包并替换二进制即可：

```bash
cd /opt/opencode2api
sudo cp opencode2api opencode2api.bak      # 备份当前版本
sudo systemctl stop opencode2api
sudo cp 新下载的opencode2api .
sudo chmod 755 opencode2api
sudo systemctl start opencode2api
```

回滚只需把 `opencode2api.bak` 拷回去。

配置兼容性：网关只接受已知字段，新增字段在旧版本中会被拒绝，因此**降级前要确认新配置不含旧版本不认识的字段**。建议升级前备份：

```bash
cp config.json config.json.$(date +%Y%m%d)
```

## 安全加固

**管理端暴露到公网前必须做这些：**

1. **加 HTTPS 反向代理**。管理端使用 HttpOnly/SameSite Cookie，写操作有 CSRF 校验，但明文 HTTP 下 Cookie 仍可能被截获。

2. **限制访问来源**。管理端只有一个共享账号，登录限速记录在内存中，重启即清零，挡不住分布式爆破。

```nginx
server {
    listen 443 ssl;
    server_name gw.example.com;

    ssl_certificate     /etc/letsencrypt/live/gw.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/gw.example.com/privkey.pem;

    # 只允许内网 / 固定 IP 访问管理端
    location / {
        allow 10.0.0.0/8;
        allow 192.168.0.0/16;
        deny all;
        proxy_pass http://127.0.0.1:8081;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

3. **API 端口不要直接暴露公网**。保持 `listen: 127.0.0.1:8080`，只让面板在同一内网访问。

4. **配置文件权限**。里面含上游 Key 与代理凭据：

```bash
chmod 600 config.json
chmod 700 bin/xray
```

5. **关闭请求体日志**。`logging.dump_request_bodies` 会把对话内容输出到日志，仅在排查问题时临时开启。

6. **密码不会留在配置里**。首次启动后 `webui.password` 被替换为 `webui.password_hash`（Argon2id），含明文的备份文件也会被删除。

## 故障排查

### 启动即退出

```bash
opencode2api -config config.json
```

配置有语法错误或未知字段时会在启动阶段直接报错并指出字段名。配置支持 `//` 和 `/* ... */` 注释。

### API 返回 401

用的是 `server_keys` 里的 Key，不是上游 `zen_keys` / `go_keys`。两者互不相通。

### 健康检查一直返回 503

按顺序排查：

1. 日志里搜 `catalog refresh` 看目录刷新是否成功
2. 出站网络是否可达（`curl -I https://opencode.ai/zen`）
3. 若上游网络需要代理，`proxies` 是否配置且该代理可用
4. 模型原生协议不被支持时，用 `models.protocols` 手动指定

### 模型列表为空

| 原因                    | 处理                                                                                 |
| ----------------------- | ------------------------------------------------------------------------------------ |
| 没配任何 Key 且未开匿名 | 填 `zen_keys` / `go_keys`，或设 `anonymous: true`                                    |
| 模型原生协议不受支持    | 用 `models.protocols` 覆盖为 `chat` / `responses` / `anthropic`                      |
| 只配了匿名通道          | 此时 `/v1/models` 只列符合条件的免费模型，确认模型名含 `free` 或在 models.dev 上免费 |

### 请求返回 502 / 504

查看日志中的 `request_failed` 事件，重点关注：

- `retry.timeout_seconds`（默认 300 秒）是否过短
- `performance.attempt_timeout_seconds` 是否需要设置，让慢节点不拖垮整体预算
- 代理是否可用（概览页可点"测试延迟"实测各节点首字节耗时）
- 上游 Key 是否有效

### HTTP 200 但生成失败

流式响应已经发出头之后才失败，HTTP 状态码不会体现。查看事件日志中的 SSE `error` 事件与请求 `outcome`：

- `stream_error` —— 流中断或出错
- `client_canceled` —— 客户端主动断开

### vless 池为空

按顺序检查：

1. `vless.enabled` 是否为 `true`，`subscription` 或 `nodes` 至少有一项且有效
2. 订阅是否能访问：日志搜 `vless_refresh_failed` 相关事件
3. 订阅格式是否为 v2ray（base64 或 `vless://` 列表）或 Clash / mihomo（YAML）；固定节点必须是 `vless://` 链接
4. `port_base` 段是否被占用
5. Xray 是否就绪：日志搜 `xray installed` 或 `xray_download_failed`

只配固定节点时不需要订阅，日志会出现 `vless pool refreshed ... fixed=N`；若 N 为 0 说明链接无法解析。

### 代理节点状态异常

概览页点"测试延迟"会实测每个节点经代理访问 Cloudflare 的首字节耗时，且**不会改动健康状态**。失败的节点会在 15 分钟后自动复查。

### 重启后监控数据消失

监控指标（请求量、成功率、延迟分位）只存在进程内存中，重启清空。这是设计如此 —— 聚合面板从每次响应的 `usage` 字段读取用量，网关不做持久化统计。需要长期日志请在外部收集 stdout 的 JSON 输出。
