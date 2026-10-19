# tools

诊断脚本。用来定位 vless/CF 隧道问题，**不是**运行时依赖——opencode2api 本身不需要它们。

## 为什么这些脚本要读环境变量

节点凭据（VLESS UUID、zone 主机名）等于你代理的钥匙。它们**故意不写进仓库**，
所以每个脚本在缺少变量时会直接退出并提示，而不是用一个写死的默认值去跑。

## 前置

- Python 3.8+
- `curl.exe`（Windows 自带，或 Linux/macOS 的 curl）
- 一个 Xray 可执行文件（默认找 `bin/xray/xray.exe`，可用 `XRAY_PATH` 覆盖）

## 环境变量

| 变量                  | 必需               | 说明                                        |
| --------------------- | ------------------ | ------------------------------------------- |
| `VLESS_UUID`          | 是                 | 节点 UUID                                   |
| `VLESS_SNI`           | 是                 | zone 主机名，须与节点里的 `sni`/`host` 一致 |
| `VLESS_PATH`          | 否                 | 传输路径，默认 `/`                          |
| `VLESS_NETWORK`       | 否                 | `ws`（默认）或 `xhttp`                      |
| `VLESS_MODE`          | 否                 | xhttp 传输模式，如 `stream-one`             |
| `VLESS_EXTRA`         | 否                 | xhttp 的 `extra` JSON，如 padding 伪装参数  |
| `XRAY_PATH`           | 否                 | Xray 可执行文件路径                         |
| `CF_TARGET_HOST`      | 仅 `cf_ip_scan.py` | 要扫描的 zone 主机名                        |
| `SSE_PROBE_PORT_BASE` | 否                 | 探测用本地 SOCKS 端口起始值                 |

### PowerShell 示例

```powershell
$env:VLESS_UUID = "<你的 UUID>"
$env:VLESS_SNI  = "<你的 zone 主机名>"
$env:VLESS_PATH = "/"
python tools\cf_ip_scan.py
```

### bash 示例

```bash
export VLESS_UUID="<你的 UUID>"
export VLESS_SNI="<你的 zone 主机名>"
python3 tools/cf_ip_scan.py
```

## 脚本

### `cf_ip_scan.py` — Cloudflare 边缘 IP 扫描

一个 Cloudflare 前置的节点可以连**任意** CF 边缘 IP，只要 TLS SNI 填 zone 主机名，
CF 会按 SNI 路由过去。这个脚本分两阶段找可用 IP：

1. 遍历 CF 官方 IP 段，做 TCP 连接 + TLS 握手，记录延迟
2. 对延迟靠前的候选发真实长连接请求，记录能存活多久

```bash
# 全量扫描
python tools/cf_ip_scan.py --per-net 10 --top 24 --hold 150 --workers 12

# 只扫第一阶段（快，几秒）
python tools/cf_ip_scan.py --phase1-only

# 把通过的地址写进 config.json 的 vless.edge_ips（推荐）
python tools/cf_ip_scan.py --apply

# 旧行为：把边缘 IP 套进模板、改写成 vless.nodes
python tools/cf_ip_scan.py --apply-nodes --template "<完整的 vless:// 链接>"
```

> **注意**：换 CF 边缘 IP 改变的是**链路质量**和**隧道条数**，不改变节点身份，也**不会**增加"出口 IP 桶"——出口地址由 Cloudflare 决定，与本机选的入口 IP 不一一对应（实测 2 个 worker × 3 个入口只对应 3 个出口 IP）。经验上它对延迟和并发有用，但**不能修复长流被截断**——那通常是隧道层的问题。

### `sse_survive_test.py` — 长流存活判定器

**这个更有用。** 换隧道方案时先跑它，别凭猜。

它对每个候选 IP 起一个独立 Xray，发一个真实的长 SSE 请求，记录能活多久：

- 有 IP 跑完 → 边缘 IP 有差别，优选有效
- 全部在相近时间截断 → 限制在隧道/Worker 层，**优选 IP 无效**

```bash
python tools/sse_survive_test.py                      # 用内置候选
python tools/sse_survive_test.py 104.16.0.1 1.1.1.1  # 指定 IP
```

串行跑，别一次并发太多——免费额度会先被打爆，你会看到一堆 429 而不是真实结果。

### `check_egress_ip.py` — 出口 IP 观测

edgetunnel 这类部署在 CF Pages/Workers 上的节点，出口是 **Cloudflare 自己的网络**，不是你的机器。这个脚本起一个指向指定边缘 IP 的 Xray，然后查出口 IP。

```bash
python tools/check_egress_ip.py 108.162.192.1 104.16.0.1
```

**实测结论（2026-10-19，2 个 worker × 3 个入口）**：出口 IP 由 Cloudflare 分配，与入口 IP **不一一对应** —— 6 个入口只观察到 3 个出口（`104.28.164.140` / `104.28.152.191` / `104.28.164.148`，同一 colo），且短时间内对同一 listener 保持稳定。所以：

- 多入口 = **多条隧道、更高并发**，不是"更多出口 IP 桶"；
- 出口 IP 会变，但由 CF 决定，不能用"换入口"精确控制；
- 想观测它变不变，可以用这个脚本在不同时间点各跑一次。

## 排障思路

遇到 `upstream SSE stream failed: unexpected EOF` 时的顺序：

1. **先绕过网关复现**——用 `sse_survive_test.py`。如果它也断，问题在隧道，不在网关
2. **看断流时间是否整齐**。整齐（如都 100 秒）像固定超时；零散（79s/139s 各不相同）像链路问题
3. **对比 WS 和 XHTTP**。Xray 已把 WS 标为 deprecated，长连接场景优先用 XHTTP
4. 如果 XHTTP 报 TLS 握手失败，检查节点链接里的 `mode`/`extra` 参数有没有被中间层丢掉
   （Worker 开了 padding 伪装时，客户端必须带上，否则握手直接失败）
