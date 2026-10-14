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

| 字段                   | 说明                                                                                            |
| ---------------------- | ----------------------------------------------------------------------------------------------- |
| `listen`               | API 监听地址。**保持 `127.0.0.1`**，除非你确实需要从外部直连                                    |
| `server_keys`          | 客户端调用本网关用的 Key，与上游 Key 完全独立                                                   |
| `zen_keys` / `go_keys` | 上游 Key，两个 Tier 独立成池                                                                    |
| `anonymous`            | `true` 时用 `public` 凭证访问符合条件的免费模型，无需上游 Key                                   |
| `prefer`               | `go`（默认）或 `zen`，决定优先尝试哪个认证 Tier                                                 |
| `proxies`              | 代理列表，支持 `direct` / `http://` / `https://` / `socks5://` / `socks5h://` / `ipv6://<地址>` |
| `webui.password`       | 仅首次启动使用：服务会把它转成 Argon2id 哈希写入 `password_hash` 并删除明文                     |

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

## IPv6 源地址池部署

适用于机房给了你一段 on-link IPv6 前缀的机器（例如 `/56`）。相比 vless 池，它没有订阅额度、没有 Xray 进程、没有隧道握手，出口延迟最低、并发上限最高。

> 下文示例统一使用 IPv6 文档保留前缀 `2001:db8:1234:5600::/56`。**请替换成你自己机器上实际拿到的前缀**，直接照抄示例是不会通的。

### 前置条件

1. **前缀确实 on-link 到本机** —— `ip -6 route show` 中能看到类似：

```
2001:db8:1234:5600::/56 dev eth0 proto kernel metric 256 pref medium
```

2. **确认挂上地址后出口随之变化**（这是整个方案的前提）：

```bash
ip -6 addr add 2001:db8:1234:5600::1002/56 dev eth0
sleep 3   # 等 DAD 完成
curl -6 -s https://api64.ipify.org --interface 2001:db8:1234:5600::1002
# 期望输出：2001:db8:1234:5600::1002
```

两个必须注意的点：地址刚加入时处于 `tentative` 状态，此时 bind 会报 `errno 99 Cannot assign requested address`，`sleep` 后才会转正；地址必须**真正挂到网卡上**，直接 bind 一个前缀内未挂载的地址同样会 `errno 99`（`/56` 路由存在 ≠ 可任意 bind）。

3. **确认上游接受该源地址**，且目标 API 的 IPv6 可达。

### 配置

```json
"ipv6_pool": {
  "enabled": true,
  "prefix": "2001:db8:1234:5600::/56",
  "interface": "eth0",
  "count": 64,
  "offset": 4096,
  "spread": "subnet",
  "manage_addresses": true,
  "ready_timeout_seconds": 30
}
```

- 需要 **root**（或具备 `CAP_NET_ADMIN`）才能执行 `ip -6 addr add`。
- **容器部署**需 `network_mode: host`；默认 bridge 下前缀挂在宿主 netns，容器内 bind 必然失败。也可改用 macvlan 把前缀分给容器，并设 `manage_addresses: false`。
- **`spread` 必须保持 `subnet`。** 上游按 `/64` 前缀计量，这个字段决定池子看起来是 1 个客户端还是 256 个。设成 `address` 会把所有地址挤进同一个 `/64`，加再多地址也只算一份额度。（该计量结论在 2026-10-11 之前的门禁下实测；`403 FreeTierError` 与它无关，见「403 与客户端契约」。）
- `count` 建议 **不超过前缀里的 `/64` 数量**（`/56` 即 256）。每个地址一个 `http.Transport`，fd 与内存随 `count` 线性增长；超过块数后会往每个 `/64` 里放第二个地址，收益递减。

### 内核调优（大量并发时）

```bash
# 每个源地址拥有独立端口空间，N 个地址即 N 倍端口容量
sysctl -w net.ipv4.ip_local_port_range="1024 65535"
# 连接跟踪上限，按目标并发上调
sysctl -w net.netfilter.nf_conntrack_max=1048576
sysctl -w net.ipv4.tcp_max_tw_buckets=262144
```

持久化写入 `/etc/sysctl.d/99-opencode2api.conf`。另外别忘进程 fd 上限（systemd 用 `LimitNOFILE=`，容器用 `--ulimit nofile=`）。

### 验证出口确实在轮换

分两层验证，缺一层都可能得出错误结论。

**第一层：源地址绑定是否生效**（OS 层，与网关无关）。池内每个地址都应当以自己的 IP 出网：

```bash
for n in 4096 4159; do
  addr=$(printf '%s::%x' "$PREFIX_BASE" "$n")
  printf '%s -> ' "$addr"
  curl -6 -s https://cloudflare.com/cdn-cgi/trace --interface "$addr" | grep '^ip='
done
```

**第二层：网关是否真的把流量铺开。** 这里有个关键前提：**出口选择带会话亲和性**（上游对 prompt cache 命中给奖励），同一会话固定走同一出口，只有不同会话才分散。所以：

- 压测必须给每个请求带上不同的 `x-session-id`，否则全部请求被判定为同一会话，只会用到 1 个出口；
- 或直接看并发时的连接数，它应当接近 `count` 而不是 1：

```bash
ss -tnp state established | grep opencode2api \
  | grep -oE '\[[0-9a-f:]+\]' | sort -u | wc -l
```

并发压测示例（40 并发、120 请求、每请求独立会话）：

```bash
seq 1 120 | xargs -P 40 -I{} curl -s -o /dev/null -w '%{http_code}\n' --max-time 180 \
  -H "Authorization: Bearer $SERVER_KEY" -H 'Content-Type: application/json' \
  -H 'x-session-id: lt-{}' \
  -d '{"model":"big-pickle","messages":[{"role":"user","content":"ok"}],"max_tokens":4}' \
  http://127.0.0.1:8080/v1/chat/completions | sort | uniq -c
```

`tools/check_egress_ip.py`（见 `tools/README.md`）面向 vless 场景，IPv6 池请用上面的方式。

### 常见故障

| 现象                                   | 原因                                               | 处理                                                          |
| -------------------------------------- | -------------------------------------------------- | ------------------------------------------------------------- |
| 启动失败：`produced no usable address` | 网卡名写错、前缀未 on-link、或无权限挂地址         | 核对 `ip -6 route show` 与 `interface`，确认以 root 运行      |
| 日志 `could not assign an address`     | 前缀长度与实际不符（如把 `/64` 写成 `/56`）        | 用 `ip -6 addr show dev eth0` 看真实长度                      |
| 日志 `never became usable`             | DAD 未在预算内完成                                 | 调大 `ready_timeout_seconds`，或减少 `count`                  |
| 找不到 `ip` 命令                       | 精简镜像缺 iproute2                                | 安装 `iproute2`，或设 `manage_addresses: false` 自行挂地址    |
| 出口 IP 始终不变                       | **会话亲和性**：同一会话固定走同一出口（预期行为） | 压测时给每个请求带不同的 `x-session-id`                       |
| vless 出口 IP 不变                     | `connection_rotation_seconds` 为 0，连接被复用     | 参考「轮换与连接复用」开启轮换（IPv6 池无效：源地址是钉死的） |
| 大批量请求返回 429                     | 上游按 **`/64` 前缀**计量，且额度每日重置          | 确认 `spread: subnet`，见下节「429 与每日额度」               |
| 所有免费模型一律 403 `FreeTierError`   | **客户端契约不满足，与出口 IP 无关**               | 见下节「403 与客户端契约」；升级到最新版本                    |
| 403 的耗时长达十几秒到几分钟           | 旧版本把 403 当可重试，逐个扫完整条出口池          | 升级到 `v1.3.2` 及以上（403 现在立即返回）                    |

### 403 与客户端契约

**先分清 403 和 429**：`429` 是额度问题（按 `/64`、每日重置），`403 FreeTierError` 是**请求身份被拒**，与出口 IP、源地址数量、`/64` 分布**全都无关**——加地址、换网段、重启池子都不会有任何改变。

**同一段文案对应多个不同根因**，必须逐项消融而不能猜。已实测确认四项，外加一条反向要求：

| 条件               | 说明                                                                                                                                                                                                                                                     |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 路由               | 推理必须发到 `/inference/v1/*`。`/zen/v1/*` 已被弃用：对推理一律 403，**但目录 GET `/zen/v1/models` 仍返回 200**——这正是它能骗过排查的原因。                                                                                                             |
| `stream: true`     | Chat 请求必须流式；网关自动强制，并把流式结果替非流式客户端折叠回 JSON                                                                                                                                                                                   |
| 工具定义           | 必须声明核心工具，且**定义本身要有真内容**：描述不能为空、`parameters.properties` 不能为空。**任意一个核心工具的定义不像官方那份，整个请求就被拒**（实测把 11 个官方工具里的 `bash` 换成极简定义即复现）。网关因此一律用内嵌的官方定义声明这五个核心工具 |
| session 的 counter | `x-opencode-session` 与 `x-opencode-session-id` 都要发，前 12 位 hex 为 `~(毫秒时间戳*0x1000 + counter)` 的低 48 位，counter 必须足够小（实测 1、5 通过，255 被拒）                                                                                      |
| ⛔ 不要发          | **`x-session-affinity`**：真客户端只在非 opencode 分支发它；在推理路由上单独带上它就会 200 → 403                                                                                                                                                         |

两次收紧：

- **2026-10-13**：上游开始把那 12 位解码回 counter，并要求它足够小。从 sha256 摘要切出这 12 位的实现只有很低的比例恰好解出小 counter，于是整体 403。
- **2026-10-14**：上游把匿名推理路由从 `/zen` 改名为 `/inference`，`x-session-affinity` 开始致命，并开始按**工具定义的内容**判定。**这一层与 session 编码无关**：把真客户端抓到的请求逐字节回放，只改路由就能 403 → 200。

**判断依据**：403 时网关日志里每一次匿名尝试都是 `status=403`，同时 `systemctl` 显示池子健康、`ss` 显示出口确实分散——三者同时成立就说明问题不在网络层。反向判据：`429` 才是额度，`403` 是契约。

**排查手法（这次就是这么定位的）**：不要只盯着请求头猜。**对照真客户端**最快——让 `opencode run -m opencode/<免费模型> ...` 走同一出口成功一次，再把它抓到的请求逐字节回放，逐步淘汰到只剩一个变量。另外注意 `/zen/v1/models` 这类 GET 仍正常，别据此认为整条 `/zen` 路由还活着。

**403 不能重试**：匿名层只有一个身份（共享 `public` 凭证 + 一套客户端指纹），换出口不会改变判定。`v1.3.2` 起网关遇到 `403 FreeTierError` 立即停止扫描出口池，并记为 `anonymous_identity_rejected`。旧版本会把一次 403 放大成整池尝试——客户端延迟涨到 12–184 秒。看到这种「403 却要好几分钟才返回」的形态，直接升级即可。

**处理**：升级到最新版本。自检（应为 `200` 而不是 `403`）：

```bash
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $SERVER_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"big-pickle","messages":[{"role":"user","content":"ok"}],"max_tokens":4}' \
  http://127.0.0.1:<API端口>/v1/chat/completions
```

> 若升级后仍是清一色 `403`，且日志里 `anonymous_identity_rejected` 每次只出现一次（即网关没有在放大它），说明上游又加了一道还没被识别的维度。此时用单变量法逐个改：路由、`x-session-affinity`、session 前 12 位、工具集、`User-Agent`，一次只动一个。

### 429 与每日额度

用匿名通道（`anonymous: true`）时，上游的 429 不是短时速率限制，而是**按 `/64` 前缀计量、每日重置的额度耗尽**。以下判据在 2026-10-11 之前的门禁下实测，2026-10-13 门禁收紧后**未复测**；用之前请先用上一节排除 403。实测判据：

- 把 256 个地址**全挤在同一个 `/64`** 里、以 480 并发冲击，444/480 收到 429，`Retry-After` 精确等于"距下一个 UTC 零点"的秒数（实测相差 1–2 秒，且 90 秒墙钟内递减 90 秒）；
- 随后把出口换到一个**从未使用过的 `/64`**，同一模型**立刻恢复 200**。

结论：**限流既不按单个地址，也不是全局，而是按 `/64` 分桶。** 所以 `spread: subnet` 是这个功能成立的前提——一个 `/56` 含 256 个 `/64`，就是 256 份独立额度。

自检方式：

```bash
# 1) 当前出口落在哪些 /64（应当分散，而不是全在同一个）
ss -tnp state established | grep opencode2api \
  | grep -oE '\[[0-9a-f:]+\]' | tr -d '[]' \
  | python3 -c "
import sys, ipaddress
blocks = {ipaddress.IPv6Network((int(ipaddress.IPv6Address(l.strip()))>>64<<64, 64)) for l in sys.stdin if l.strip()}
print('使用中的 /64 数量:', len(blocks))
for b in sorted(blocks)[:5]: print(' ', b)"

# 2) 是否被限流、额度何时重置
curl -s -D - -o /dev/null -H "Authorization: Bearer $SERVER_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"big-pickle","messages":[{"role":"user","content":"ok"}],"max_tokens":4}' \
  http://127.0.0.1:<API端口>/v1/chat/completions | grep -i retry-after
```

注意两点：

1. 429 不会把出口标记为不可用（只有连接超时和拒绝才会），所以池子状态看起来一切正常，这是预期行为，不要去调健康检查。
2. 单份额度有限（实测一个 `/64` 约 1200–1500 次请求后触发）。要更高总量，就提高可用的 `/64` 数量，或叠加真实的 `zen_keys` / `go_keys`。

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
2. 出站网络是否可达（`curl -s -o /dev/null -w '%{http_code}\n' https://opencode.ai/inference/v1/models`，应为 `200`）
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
