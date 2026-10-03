# opencode2api

[English](README.md) | [简体中文](README.zh-CN.md)

用 Go 编写的 **OpenCode Zen / Zen Go API 网关**。对外提供 OpenAI Chat Completions、Responses 和 Anthropic Messages 三种推理接口，按上游原生协议自动转换请求与响应，并统一管理上游 Key 与代理池。

管理端（WebUI）已内嵌进可执行文件，运行时**不需要 Node.js，也不需要数据库**。

> **部署教程**：完整的四种部署方式、首次配置、验证步骤、故障排查与安全加固见 **[DEPLOYMENT.md](DEPLOYMENT.md)**。

## 特性

- 三种推理协议之间的普通 JSON 与 SSE 流式双向转换。
- 文本、图片、reasoning、函数工具、工具调用与工具结果；目标协议可表达时支持文件内容。
- 另提供 OpenCode 决策模型端点 `/v1/systemone`，载荷原样透传。
- 独立的 Zen / Go Key 池，可配置优先级、重试次数与会话亲和性。
- 可选的匿名通道：使用 OpenCode `public` 凭证访问符合条件的免费模型。
- 直连、HTTP、HTTPS、SOCKS5、SOCKS5H 代理，以及从文件加载代理列表。
- vless 订阅代理池：兼容 v2ray 与 Clash / mihomo 订阅，每个节点一个独立 Xray 进程与出口 IP，滚动轮换。**Xray 缺失时自动下载并校验**，无需手动准备。
- 动态模型发现、原生协议识别与模型能力缓存。
- 独立管理端口，提供概览、路由诊断、配置编辑与实时事件日志。
- 配置热重载：先校验并构建新网关，再切换新请求，出错时保留原有实例。

## 快速开始

从 [GitHub Releases](https://github.com/wu-jiyan/opencode2api/releases) 下载二进制文件，或使用 **Go 1.24 及以上版本**编译：

```bash
git clone https://github.com/wu-jiyan/opencode2api.git
cd opencode2api
cp config.example.json config.json
go build -o opencode2api ./cmd/opencode2api
```

启动前编辑 `config.json`：

1. 把 `server_keys` 改成你自己的本地 API Key。
2. 填入 Zen 或 Go Key；也可以设置 `anonymous: true` 并清空两个上游 Key 数组。
3. 替换 `webui.password`。示例配置已启用 WebUI，用户名为 `admin`。

```bash
./opencode2api -config config.json
```

Windows 下使用 `Copy-Item config.example.json config.json` 复制配置，编译为 `go build -o opencode2api.exe ./cmd/opencode2api`，然后运行 `.\opencode2api.exe -config config.json`。

示例配置的 API 监听 `127.0.0.1:8080`，WebUI 监听 `0.0.0.0:8081`。本机访问 `http://localhost:8081`。**通过网络访问管理端时，请务必限制访问范围并使用 HTTPS 反向代理** —— 管理端只有一个共享账号，且限速是按内存记录而非持久化的。

命令行参数：

| 参数          | 默认值        | 用途                  |
| ------------- | ------------- | --------------------- |
| `-config`     | `config.json` | 配置文件路径。        |
| `-listen`     | 未设置        | 覆盖 API 监听地址。   |
| `-web-listen` | 未设置        | 覆盖 WebUI 监听地址。 |

配置所在目录需要可写，用于密码迁移、配置保存和模型缓存。

## 下载二进制部署

从 [Releases](https://github.com/Wu-jiyan/opencode2api/releases) 下载对应平台的压缩包。Linux 与 macOS 用 `.tar.gz`，Windows 用 `.zip`，每个包都附带 `.sha256` 校验文件，务必先校验再使用：

```bash
VERSION=v1.0.0
ARCH=linux_amd64        # 可选：linux_arm64、darwin_arm64、darwin_amd64
curl -LO "https://github.com/Wu-jiyan/opencode2api/releases/download/${VERSION}/opencode2api_${VERSION}_${ARCH}.tar.gz"
curl -LO "https://github.com/Wu-jiyan/opencode2api/releases/download/${VERSION}/opencode2api_${VERSION}_${ARCH}.tar.gz.sha256"
sha256sum -c "opencode2api_${VERSION}_${ARCH}.tar.gz.sha256"
tar -xzf "opencode2api_${VERSION}_${ARCH}.tar.gz"
cd "opencode2api_${VERSION}_${ARCH}"
vi config.json
./opencode2api -config config.json
```

macOS 用 `shasum -a 256 -c` 替代 `sha256sum -c`；Windows 下载 `.zip` 后用 `Get-FileHash -Algorithm SHA256` 比对，再用 `Expand-Archive` 解压。

压缩包内含对应平台的二进制、已填好占位符的 `config.json` 和三份文档，**不含启动脚本**（批处理文件在不同代码页下容易乱码），请直接运行二进制。启用 vless 但本地没有 Xray 时，服务会在后台自动下载。

更完整的说明——包括 systemd 常驻、各平台命令、配置生效方式——见 [DEPLOYMENT.md](DEPLOYMENT.md)。

## API 使用

`server_keys` 用于客户端访问本网关，与 `zen_keys`、`go_keys` 相互独立，永远不会被当作上游凭证发送。

请求头使用 `Authorization: Bearer YOUR_LOCAL_API_KEY` 或 `x-api-key: YOUR_LOCAL_API_KEY`。`/healthz` 无需认证。

| 方法 | 路径                   | 用途                             |
| ---- | ---------------------- | -------------------------------- |
| GET  | `/v1/models`           | 当前配置下可路由的模型。         |
| POST | `/v1/chat/completions` | Chat Completions。               |
| POST | `/v1/responses`        | Responses。                      |
| POST | `/v1/messages`         | Anthropic Messages。             |
| POST | `/v1/systemone`        | 决策模型，仅透传，不做形状转换。 |
| GET  | `/healthz`             | 就绪状态与资源汇总。             |

先获取可用模型：

```bash
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer YOUR_LOCAL_API_KEY"
```

把下面示例中的 `MODEL_ID` 换成返回列表中的某个 ID。

**Chat Completions**

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer YOUR_LOCAL_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"MODEL_ID","messages":[{"role":"user","content":"Hello"}]}'
```

**Responses**

```bash
curl http://localhost:8080/v1/responses \
  -H "Authorization: Bearer YOUR_LOCAL_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"MODEL_ID","input":"Hello"}'
```

**Anthropic Messages**

```bash
curl http://localhost:8080/v1/messages \
  -H "x-api-key: YOUR_LOCAL_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"MODEL_ID","max_tokens":512,"messages":[{"role":"user","content":"Hello"}]}'
```

需要流式响应时在请求体中加入 `"stream": true`，并使用 `curl -N`。响应头 `x-request-id` 可用于关联日志。API 请求体上限为 32 MiB。

### 兼容边界

当客户端协议与上游原生协议相同时，供应商特有字段会被保留。跨协议请求会经过统一的中间结构转换，部分参数没有等价表达 —— 例如 Chat 的 JSON 输出约束与 `seed` 不会转发到 Anthropic Messages。不支持的内容块或工具类型会被拒绝。

网关只实现上表中的接口，不提供 embeddings、文件上传、图像生成，也不提供 Responses 的查询与取消接口。网关不保存会话历史，客户端需要自行提交历史，或使用实际上游支持的相关能力。

## 路由与模型

### 模型发现

服务按 `models.refresh_seconds` 刷新 Zen / Go 的 `/v1/models`，以及 OpenCode 的[能力目录](https://models.opencode.ai/api.json)，并分别为两个 Tier 记录原生协议与模型限制。协议无法自动判定时，回退到 OpenCode 的 Zen / Go 文档。

`models.protocols` 可覆盖自动发现结果：

```json
{
  "models": {
    "refresh_seconds": 300,
    "protocols": {
      "custom-model": "chat"
    }
  }
}
```

协议值仅允许 `chat`、`responses`、`anthropic`、`systemone`。原生协议不受支持的模型会从模型列表中隐藏，除非配置了覆盖值。

`models.strip_free_suffix` 用于隐藏对外模型名末尾的 `-free`：`/v1/models` 返回 `deepseek-v4-flash` 而不是 `deepseek-v4-flash-free`。两种写法都仍可调用 —— 精确匹配目录条目优先，匹配不到才回退到 `名字 + "-free"` —— 因此该开关的取值不会破坏任何已有客户端。上游始终收到真实模型 ID；若去掉后缀后会与目录中已有模型重名，则保留原样以免遮蔽。该开关只影响对外暴露的名字，响应体中回显的 `model` 仍是上游真实 ID。

成本与弃用信息来自 [models.dev](https://models.dev/api.json)，每 24 小时刷新一次，元数据请求超时 30 秒。刷新失败时保留已有数据。

### 匿名通道与回退

启用 `anonymous: true` 后，满足以下任一条件的模型可以进入 Zen 匿名通道：

- 模型 ID 包含 `free`（不区分大小写）。
- models.dev 中输入、输出成本均为零，且模型未弃用。

这仅是网关的路由判断，上游仍可能拒绝或限流。匿名请求在上游认证头中使用 `public` 凭证。

路由顺序：

1. 符合条件的模型先尝试匿名通道，每个可用代理最多一次。
2. 按 `prefer` 顺序尝试认证通道，仅包含已配置 Key 且能提供该模型的 Tier。
3. 每个认证 Tier 各自使用 `retry.max_attempts` 预算。

匿名阶段不受 `retry.max_attempts` 截断，但所有阶段共享请求总超时。网络错误、认证失败、限流与服务端错误会触发 Key 轮换；其他 4xx 会结束当前 Tier，但仍可继续尝试其他可用 Tier。

每个 Tier 使用自己的原生协议编码请求。流开始后不再切换节点重新生成。识别到过期的 Responses reasoning 引用时可执行一次修复重试；指定 Key 的诊断不会执行该重试。

仅配置匿名通道时，`/v1/models` 只展示符合匿名条件的模型。

### 会话与代理

Key 初始化时均衡分配到各代理。真实流量会触发代理检查、Key 重新绑定与失败冷却。已被标记异常的代理每 15 分钟通过 Cloudflare trace 复查一次。

服务用稳定的会话哈希选择首选 Key 或匿名代理。建议通过 `x-session-id` 区分独立会话，同时支持 `x-opencode-session`、`x-session-affinity`、`conversation-id`、`conversation_id` 与 `metadata.session_id`。

没有显式会话 ID 时，使用第一条用户消息生成会话标识，因此开场内容相同的会话可能共享亲和性。调整 Key 或代理池成员后，原有会话选中的节点可能变化。

失败冷却按指数增长，上限为 `performance.failure_cooldown_seconds` 的八倍；上游 `Retry-After` 更长时以更长者为准。所有认证 Key 都处于冷却时，路由仍可能尝试最早结束冷却的 Key；仍在冷却的匿名节点会被跳过。

## 配置参考

完整起始配置见 [config.example.json](config.example.json)。配置支持 `//` 与 `/* ... */` 注释，未知字段和无效取值会被拒绝。

### Key、监听地址与路由

| 字段                        | 默认值或要求                                                                  |
| --------------------------- | ----------------------------------------------------------------------------- |
| `listen`                    | `127.0.0.1:8080`。                                                            |
| `server_keys`               | 至少一个本地 Key。                                                            |
| `zen_keys`、`go_keys`       | 未启用匿名模式时，至少需要一个上游 Key。                                      |
| `anonymous`                 | `false`。                                                                     |
| `prefer`                    | `go`；可选 `go`、`zen`。                                                      |
| `upstream.zen`              | `https://opencode.ai/zen`。                                                   |
| `upstream.go`               | `https://opencode.ai/zen/go`。                                                |
| `proxies`                   | 两个代理来源都为空时，使用 `["direct"]`。                                     |
| `proxyfile`                 | 可选；相对路径基于配置文件所在目录解析。                                      |
| `models.refresh_seconds`    | `300`；最小为 1。                                                             |
| `models.protocols`          | `{}`；按模型 ID 覆盖原生协议。                                                |
| `models.strip_free_suffix`  | `false`；对外隐藏模型名末尾的 `-free`。                                       |
| `reasoning.effort`          | 空（关闭）；可选 `minimal`、`low`、`medium`、`high`、`xhigh`、`max`、`none`。 |
| `reasoning.effort_by_model` | `{}`；按模型 ID 覆盖 `reasoning.effort`。                                     |

### 强制思考强度

`reasoning.effort` 指定发往上游的思考强度，用于客户端请求自身没有提出强度的情况；`reasoning.effort_by_model` 按模型 ID 覆盖它。它对三种协议、两个转换方向都生效，对跨协议请求尤其重要：Anthropic 的 `thinking.budget_tokens` 只能被 Chat 与 Responses 近似表达，而强制指定的强度能到达单靠预算表达不出的档位。

```json
{
  "reasoning": {
    "effort": "high",
    "effort_by_model": { "claude-opus-5": "max" }
  }
}
```

客户端显式指定的强度始终优先，所以它只替换无人指定的强度：完全未设置，或由 `thinking.budget_tokens` 推断出来的强度。设为 `"none"` 可为未请求思考的请求移除思考配置。

两个方向的强度都会如实保留：显式的 `output_config.effort` 不会再被 `thinking` 块遮蔽；`budget_tokens` 保持其档位（`8192` 为 `high`，`32000` 为 `xhigh`，两者可区分）；发往 Responses 时强度以 `reasoning.effort` 形式传递。

WebUI 的配置中心也提供默认强度与按模型覆盖。管理接口 `GET /api/config` 返回 `reasoning`，`PUT /api/config` 接受同名对象；省略该字段会保留现有值，发送空对象则会清除它。

代理支持 `direct`、`http://`、`https://`、`socks5://` 与 `socks5h://`，URL 可包含认证信息。配置内代理先加载，再追加 `proxyfile` 内容，并按首次出现的顺序去重。

代理文件每行一个地址，允许空行与注释：

```text
# 首选代理
http://user:password@127.0.0.1:7890
socks5://127.0.0.1:1080  # 备用代理
direct
```

支持 `#`、`;`、`//` 三种注释标记，需位于行首或空白字符之后。

### vless 代理池

`vless` 段把一份上游订阅转换成一组本地 SOCKS5 监听器：每个监听器由独立的 Xray 子进程提供服务，因此各自拥有独立出口 IP。启用后，这些本地监听器会追加到静态代理之后，成为代理池的一部分参与轮换。

订阅同时兼容 **v2ray（base64 或明文 `vless://` 列表）** 与 **Clash / mihomo（YAML）** 两种格式。

| 字段                            | 默认值      | 含义                                                                                  |
| ------------------------------- | ----------- | ------------------------------------------------------------------------------------- |
| `vless.enabled`                 | `false`     | 打开 vless 代理池。                                                                   |
| `vless.subscription`            | 空          | 订阅地址，需为 http/https。与 `nodes` 至少填一项。                                    |
| `vless.nodes`                   | 空          | 固定 `vless://` 分享链接列表，可留空。与订阅可同时使用，固定节点优先占位。            |
| `vless.preferred_domains`       | 空          | 入口域名（Cloudflare 优选域名或自选 IP），见下节。                                    |
| `vless.endpoints`               | 空          | 服务入口，按顺序轮流分配到各候选，见下节。                                            |
| `vless.count`                   | `24`        | 同时保持的本地监听器数量。                                                            |
| `vless.refresh_seconds`         | `300`       | 滚动重建的基础间隔。                                                                  |
| `vless.rotate_batch`            | `3`         | 每轮重建的监听器数量；越小则池整体越"热"，越大则整池轮换越快。                        |
| `vless.xray_path`               | 空          | Xray 可执行文件。相对路径基于配置目录解析；留空则查找 `bin/xray/xray[.exe]` 或 PATH。 |
| `vless.auto_download_xray`      | `true`      | 找不到 Xray 时自动下载官方版本并校验。                                                |
| `vless.xray_version`            | 空          | 固定自动下载的版本号，如 `v26.3.27`；留空表示最新 Release。                           |
| `vless.port_base`               | `24000`     | 监听端口起点，第 i 个监听器使用 `port_base + i`。                                     |
| `vless.host`                    | `127.0.0.1` | 本地监听地址。                                                                        |
| `vless.subscription_proxy`      | 空          | 可选；拉取订阅本身所走的代理。                                                        |
| `vless.startup_timeout_seconds` | `15`        | 单个 Xray 实例启动后等待其开始监听的超时。                                            |

轮换策略：`refresh_seconds` 到达时按 `rotate_batch` 重建一批监听器，实现滚动更新 —— 任何时刻池中都不会整体中断。每个新节点会先在临时端口上验证可连接，通过后才替换旧进程，因此"单节点重建后出口 IP 变化"不会以一次失败的连接为代价。

#### 固定节点、入口域名与服务入口

除订阅外，还可以直接配置固定的 `vless://` 链接，适合 Cloudflare 前置的节点 —— 订阅地址失效时服务照常工作，也适合只想锁定某几条线路的场景。

`preferred_domains` 会把固定节点的**入口地址**替换成列表里的每一个值，每条生成一个候选，UUID、SNI、Host、路径与传输方式全部保持不变。因此一条链接就能撑起多个独立出口。地址是字面 IP、且不在列表中的节点会原样使用，不会被改写。

```json
"vless": {
  "enabled": true,
  "subscription": "",
  "nodes": ["vless://uuid@ct.example.com:443?security=tls&sni=edn2.example.com&type=ws&host=edn2.example.com&path=%2F"],
  "preferred_domains": ["cf.example.com", "ct.example.com", "cu.example.com"],
  "endpoints": ["edn2.example.com", "edn3.example.com", "edn4.example.com"],
  "count": 7
}
```

`endpoints` 是同一服务的多个入口，按候选序号**轮流分配**而非随机 —— 每个入口通常各有独立的请求额度，均摊能把额度同时用完，而不是让随机偏斜提前耗掉其中一个。只有 Host 与 SNI 会变，UUID 标识的是用户而非入口，因此不受影响；留空则沿用链接自带的 host。

固定节点候选会按实测延迟排序，快的优先占用前面的槽位。排序在后台完成，不阻塞启动；连不上的候选排在最后而不是被丢弃，以免池子缩到小于配置的 `count`。

### Xray 自动下载

开启 `vless.enabled` 后，若找不到 Xray 可执行文件，服务会在**后台自动下载**官方版本（不阻塞启动），落到配置目录下的 `bin/xray/`，重启后直接复用。流程：

1. 按平台与架构确定官方 Release 资产名（如 `Xray-linux-64.zip`）。
2. `xray_version` 为空时先查询最新 Release 标签。
3. 依次尝试多个镜像源，下载压缩包与其 `.dgst` 校验文件。
4. **核对 SHA-256**，校验不通过则换下一个镜像，绝不落盘。
5. 先写 `.download` 临时文件再原子改名，只解压 `xray` / `xray.exe`（跳过约 30 MB 的 geo 数据）。

镜像源按**实测持续吞吐量**排序，而非 ping 延迟 —— 能快速响应 HEAD 的主机未必能快速传输载荷：

| 顺序 | 镜像                   | 国内实测吞吐 |
| ---- | ---------------------- | ------------ |
| 1    | `gh-proxy.com`         | ~0.5 MiB/s   |
| 2    | `ghfast.top`           | ~0.09 MiB/s  |
| 3    | `ghproxy.net`          | ~0.01 MiB/s  |
| 4    | 官方 `github.com` 直连 | 常超时，兜底 |

进度可在事件日志中查看：成功为 `xray installed`，全部失败为 `automatic xray download failed`。若自动下载失败，可手动放置二进制并用 `vless.xray_path` 指定，或将 `auto_download_xray` 设为 `false` 完全禁止联网。

支持 Linux / Windows / macOS 的 amd64、arm64、386、arm(v7)。

### 超时与连接池

| 字段                                      | 默认值 | 含义                                             |
| ----------------------------------------- | ------ | ------------------------------------------------ |
| `retry.max_attempts`                      | `3`    | 每个认证 Tier 的尝试次数，包含首次请求。         |
| `retry.timeout_seconds`                   | `300`  | 推理请求总超时，包含流式响应读取。               |
| `performance.attempt_timeout_seconds`     | `0`    | 每次尝试等待响应头的超时；0 表示使用请求总超时。 |
| `performance.connect_timeout_seconds`     | `5`    | 建立连接的超时。                                 |
| `performance.failure_cooldown_seconds`    | `15`   | 失败冷却基数。                                   |
| `performance.max_idle_conns`              | `2048` | 每个代理 Transport 的空闲连接上限。              |
| `performance.max_idle_conns_per_host`     | `256`  | 每个 Transport 对单个主机的空闲连接上限。        |
| `performance.max_conns_per_host`          | `0`    | 单个主机的连接上限；0 表示不限制。               |
| `performance.idle_conn_timeout_seconds`   | `120`  | 空闲连接保留时间。                               |
| `performance.connection_rotation_seconds` | `0`    | 空闲连接回收周期；0 表示不轮换。                 |

单次响应头超时不会超过请求总超时。希望慢节点仍能留出回退时间时，可以把它设为小于总超时的值。请求上下文过期后会停止后续尝试，不会惩罚尚未实际使用的 Key 或代理。

vless 入口会给每条新建连接分配不同的出口 IP，因此当 `performance.connection_rotation_seconds > 0` 时，网关会回收空闲连接，让后续请求重新拨号以轮换出口 IP。回收按监听器错峰进行（每周期只处理一个），避免整池同时支付握手开销；紧挨着到达的请求仍复用热连接。

代价参考：经隧道新建连接比复用连接多约 0.6～1.3 秒（Cloudflare trace 类目标更高）。因此该值应设得远大于单次请求间隔，例如与 `vless.refresh_seconds` 对齐的 `300`；设为 `0` 可完全关闭轮换。

### 日志与管理端

| 字段                          | 默认值或要求                                    |
| ----------------------------- | ----------------------------------------------- |
| `logging.level`               | `info`；可选 `debug`、`info`、`warn`、`error`。 |
| `logging.ring_size`           | `2000`；范围 100–50,000。                       |
| `logging.dump_request_bodies` | `false`；配合 `debug` 级别输出上游请求体。      |
| `webui.enabled`               | 省略时为 `false`；示例配置设置为 `true`。       |
| `webui.listen`                | `0.0.0.0:8081`。                                |
| `webui.username`              | 启用 WebUI 时必填；示例为 `admin`。             |
| `webui.password`              | 首次初始化密码，最小长度 10。                   |
| `webui.password_hash`         | 自动生成的 Argon2id 哈希，用于替代明文密码。    |
| `webui.session_ttl_minutes`   | `720`；范围 5–10,080。                          |

启动时会把初始密码哈希化并从配置中删除，含初始明文密码的备份也会被删除。配置与备份仍包含运行所需的上游 Key 和代理凭据，请限制文件访问权限。

请求体日志默认关闭，每份准备好的上游请求体最多输出 64 KiB。已配置的敏感值会脱敏，但对话内容本身仍可能包含敏感信息。

## WebUI 与诊断

管理端口提供 WebUI 与 `/api/*` 接口。认证使用单一管理员账号、服务端 Session、HttpOnly / SameSite Cookie、写操作 CSRF 校验与登录限速。

本网关面向"作为上游渠道被聚合面板（如 new-api）调用"的部署方式：请求级用量、Token 统计、请求明细与渠道测试由聚合面板负责，管理端只保留本网关独有的能力 —— 上游 Key 与代理池管理、协议路由诊断，以及进程内事件日志。控制台包含四个页面：**概览**、**路由诊断**、**配置中心**、**事件日志**。

| 管理接口                                         | 用途                                               |
| ------------------------------------------------ | -------------------------------------------------- |
| `POST /api/auth/login`                           | 登录，获取 Session Cookie 与 CSRF Token。          |
| `GET /api/auth/session`、`POST /api/auth/logout` | 查看或结束当前 Session。                           |
| `GET /api/config`、`PUT /api/config`             | 读取脱敏配置或应用修改。                           |
| `POST /api/config/reload`                        | 从磁盘重载配置。                                   |
| `GET /api/monitor`                               | 进程内请求量、成功率、延迟分位与资源快照。         |
| `GET /api/debug/models`                          | 模型路由、匿名资格与 pricing metadata 诊断。       |
| `POST /api/nodes/probe`                          | 探测各代理节点的首字节延迟（只读，不改健康状态）。 |
| `POST /api/models/refresh`                       | 立即从上游重拉模型目录与 models.dev 价格元数据。   |
| `GET /api/logs`、`GET /api/logs/stream`          | 最近日志或 SSE 实时订阅。                          |

### 保存与重载

服务会先校验候选配置并构建新的 Gateway，然后保存配置并切换实例。校验或保存失败时，当前 Gateway 继续处理请求；已经开始的请求继续使用原有实例。

Key、代理、上游地址、重试、模型、日志与路由偏好立即对新请求生效。`listen`、`webui.listen`、`webui.enabled` 的修改需要重启进程。保存后的 JSON 会被规范化，不保留原有注释。

## 监控与持久化

请求结果以完整推理过程为准。即使 HTTP 200 已经发出，SSE 错误事件或异常断流仍会计为失败并标记 `stream_error`；客户端取消标记为 `client_canceled`。

进程内监控只聚合请求量、成功率与延迟分位（最近 60 分钟滚动窗口加生命周期累计）。Token 用量与请求/尝试明细不由本进程统计：聚合面板直接读取响应中的 `usage` 字段即可，本网关只负责如实透传上游响应。

| 数据               | 保存位置或保留规则                          |
| ------------------ | ------------------------------------------- |
| Session 与监控指标 | 进程内存；重启清空。                        |
| 实时日志环形缓冲   | 进程内存，大小由 `logging.ring_size` 指定。 |
| 结构化日志         | stdout JSON；需长期保留时由外部系统收集。   |
| 当前配置与上一版本 | `config.json`、`config.json.bak`。          |
| 模型目录缓存       | `config.json.models.catalog.json`。         |
| 成本与弃用信息缓存 | `config.json.models.dev.json`。             |

缓存文件名基于实际配置路径生成。lifetime 仅表示当前进程运行期间的累计，各实例之间不共享 Session 或监控状态。

### 健康检查

`/healthz` 不增加监控计数，也不触发网络请求；返回资源数量，不包含 Key 或代理地址。

- HTTP 503：模型目录尚未就绪、当前配置下无可路由模型，或没有健康代理。
- HTTP 200：服务就绪。可用目录缓存过期时仍返回 200，同时模型状态为 `stale`，整体状态为 `degraded`。

过期阈值为 `models.refresh_seconds` 的两倍，且不低于 60 秒。就绪检查只覆盖目录与资源，不会实际验证某个上游 Key 能否完成下一次推理。

## 开发

Go 源码按职责拆分为独立包。程序入口负责组装服务，具体实现放在 `internal/` 中。

```text
cmd/
  opencode2api/main.go    命令行参数、启动与优雅退出
internal/
  admin/                 管理 API、登录会话与诊断
  buildinfo/             健康检查与管理接口共享的版本信息
  config/                配置解析、持久化、密码与脱敏
  gateway/               HTTP 路由、重试、资源池、刷新与运行时
  httpx/                 通用 HTTP 响应、响应体处理与请求头
  identity/              请求标识与会话亲和性
  jsonutil/              JSON 取值与解码辅助函数
  models/                模型目录、能力、价格与缓存
  protocol/              请求和响应转换、SSE 解析与输出
  telemetry/             请求跟踪、指标、日志与异常恢复
  vless/                 订阅解析与 Xray 进程池
webui/
  embed.go               将前端构建产物（dist）内嵌进可执行文件
  index.html             管理端入口（Vite 源文件）
  src/                   Vue 3 源码：入口、API 客户端、视图与组件
  dist/                  Vite 构建产物，随仓库提交并被 embed
```

Vite 配置位于仓库根目录的 `vite.config.js`。

建议阅读顺序：

1. [程序入口](cmd/opencode2api/main.go) → [运行时管理](internal/gateway/runtime.go) → [HTTP 处理](internal/gateway/gateway.go)。
2. [上游请求](internal/gateway/upstream.go) 与 [模型路由](internal/models/catalog.go) 说明请求如何选择并到达上游。
3. [请求转换](internal/protocol/request.go)、[响应转换](internal/protocol/response.go) 与 [流式传输](internal/protocol/stream.go) 说明协议处理过程。
4. [管理路由](internal/admin/server.go) 与 [WebUI 源码](webui/src/App.vue) 说明配置编辑与诊断功能。

执行 Go 格式化、静态分析与构建：

```bash
gofmt -w cmd internal webui
go vet ./...
go build -o opencode2api ./cmd/opencode2api
```

开发时可直接运行 `go run ./cmd/opencode2api -config config.json`。发布构建仍通过 `-ldflags "-X main.version=vX.Y.Z"` 注入版本号。

Node.js 仅用于构建管理端与格式化。`webui/dist` 已随仓库提交，因此构建网关二进制本身不依赖 Node.js；改动前端源码后需要重新构建：

```bash
npm ci --ignore-scripts
npm run build     # 重新生成 webui/dist
npm run format
npm run format:check
```

项目通过 `.editorconfig`、`.gitattributes`、Go 格式化与固定版本的 Prettier 统一格式。CI 覆盖 Linux / Windows 上 Go 1.24 与稳定版 Go 的 `go vet` 和构建，以及格式检查、管理端构建和 shell 脚本语法检查。发布压缩包包含中英文两份 README。

## 常见问题

| 现象                          | 排查方向                                           |
| ----------------------------- | -------------------------------------------------- |
| API 返回 401                  | 使用配置中的本地 `server_keys`。                   |
| 健康检查一直为 `starting`     | 查看目录刷新日志与网络连通性，必要时补充协议覆盖。 |
| 模型列表为空                  | 检查配置的 Tier、匿名资格与原生协议是否受支持。    |
| 请求返回 502 / 504            | 查看上游尝试、凭据、代理以及请求总超时和单次超时。 |
| HTTP 200 但生成失败           | 查看 SSE 错误事件与请求结果，不能只看 HTTP 状态。  |
| 修改 `config.json` 后未生效   | 查看事件日志中的校验错误；旧配置会继续运行。       |
| 局域网访问不到 WebUI          | `webui.listen` 需监听 `0.0.0.0` 而非 `127.0.0.1`。 |
| 重启后监控消失                | 监控仅保存在内存，需要外部收集 stdout 日志。       |

## 致谢

感谢 [LINUX DO](https://linux.do) 社区的支持。
