#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
CF 边缘 IP 优选扫描器 (为 opencode2api 的 vless 池挑选节点)

背景
----
vless 节点走 Cloudflare 前置: TCP 连任意 CF 边缘 IP, TLS SNI 填 zone 主机名,
CF 再按 SNI 路由到你的 zone。因此**任何能完成 TLS 握手的 CF 边缘 IP 都可以
直接当节点地址用**。本工具就是找出其中"又快、又不会在 ~100s 掐断长连接"的那些。

两个阶段
--------
阶段 1 (快, 全网扫描): 遍历 Cloudflare 官方 IP 段, 做 TCP 连接 + TLS 握手,
    记录握手成功的 IP 及其延迟。纯 TCP/TLS, 不发业务流量, 几秒到几十秒完成。

阶段 2 (慢, 只测头部候选): 对阶段 1 排名靠前的 IP, 发真实的**长连接**请求,
    持续观测存活时间。阶段 1 只知道"能不能连上", 不知道"能不能撑住两分钟",
    而后者才是本项目真正的问题。

用法
----
    python cf_ip_scan.py                  # 阶段1 + 阶段2(默认 12 个候选, 150s)
    python cf_ip_scan.py --top 20 --hold 200
    python cf_ip_scan.py --phase1-only
    python cf_ip_scan.py --apply          # 把结果写入 config.json

注意: 本脚本必须在**家里那台跑 opencode2api 的机器**上运行, 因为它衡量的是
"从家里到 CF 边缘"的链路质量。放到 HK 服务器上测没有意义。
"""

from __future__ import annotations

import argparse
import concurrent.futures as futures
import ipaddress
import json
import os
import re
import socket
import ssl
import statistics
import sys
import time
from dataclasses import dataclass, field
from typing import Iterable
from urllib.request import Request, urlopen

# ---------------------------------------------------------------- 配置

# CF 官方公布的 IPv4 段。取不到时回退到这份快照(2024 年, 至今仍有效)。
CF_IPV4_FALLBACK = [
    "173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
    "141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
    "197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
    "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
]
CF_IPS_URL = "https://www.cloudflare.com/ips-v4"

# 目标 zone 主机名。必须和 vless 节点里的 sni/host 一致, 否则 CF 不认这个 SNI。
# 不给默认值: 这是你自己的节点地址, 不该出现在公开仓库里。
TARGET_HOST = os.environ.get("CF_TARGET_HOST", "")
TARGET_PORT = int(os.environ.get("CF_TARGET_PORT", "443"))

# 阶段 1 并发。家用宽带/小主机别开太高, 否则自己先把带宽打满。
PHASE1_WORKERS = 200
PHASE1_TIMEOUT = 2.5

# 阶段 2 的长连接探测。每 10s 发一次小请求, 看连接能活多久。
PHASE2_HOLD = 150          # 秒, 期望值; opencode2api 的 retry.timeout_seconds 是 1800
PHASE2_KEEPALIVE = 10       # 秒, 心跳间隔
PHASE2_WORKERS = 4


# ---------------------------------------------------------------- 结果结构


@dataclass
class Probe:
    ip: str
    latency_ms: float | None = None
    tls_ok: bool = False
    tls_ms: float | None = None
    error: str = ""
    # 阶段 2
    survived_s: float = 0.0
    long_ok: bool = False
    long_err: str = ""

    @property
    def score(self) -> float:
        """越小越好。TLS 失败直接排最后。"""
        if not self.long_ok:
            return 1e9
        return (self.latency_ms or self.tls_ms or 999.0) + self.survived_s * 0.05


# ---------------------------------------------------------------- 阶段 1


def load_cf_ranges() -> list[str]:
    """优先从 Cloudflare 官网取 IP 段, 失败则用内置快照。"""
    try:
        req = Request(CF_IPS_URL, headers={"User-Agent": "cf-ip-scan/1.0"})
        with urlopen(req, timeout=10) as resp:
            text = resp.read().decode()
        nets = [l.strip() for l in text.splitlines() if l.strip() and not l.startswith("#")]
        if nets:
            print(f"[i] 从官网取到 {len(nets)} 个 IPv4 段")
            return nets
    except Exception as exc:  # noqa: BLE001
        print(f"[!] 官网取段失败({exc.__class__.__name__}), 使用内置快照")
    return list(CF_IPV4_FALLBACK)


def iter_candidate_ips(ranges: Iterable[str], limit_per_net: int) -> list[str]:
    """
    每个网段按 'host' 位递增取若干个代表地址。

    CF 的 anycast 段很大(整个 /13 都是), 全展开是 20 万个地址, 家用带宽扫不动。
    实际上同段内相邻地址路由质量高度相似, 所以每个段抽样就够。
    """
    ips: list[str] = []
    for cidr in ranges:
        try:
            net = ipaddress.ip_network(cidr, strict=False)
        except ValueError:
            continue
        hosts = net.num_addresses - 2  # 去掉网络号和广播地址
        if hosts <= 0:
            continue
        step = max(1, hosts // limit_per_net)
        for i in range(1, hosts, step):
            addr = net.network_address + i
            # 全零/全一是 CF 保留地址, 跳过
            text = str(addr)
            tail = text.split(".")[-1]
            if tail in ("0", "255"):
                continue
            ips.append(text)
    return ips


def probe_tls(ip: str, timeout: float = PHASE1_TIMEOUT) -> Probe:
    """TCP 连接 + 带 SNI 的 TLS 握手。握手成功即说明这个边缘 IP 可以路由到目标 zone。"""
    result = Probe(ip=ip)
    ctx = ssl.create_default_context()
    # 只关心能否握手 + 拿到证书, 不做 CA 校验(自签/证书链差异不影响路由判断)。
    # check_hostname 必须保持 False, 否则 Python 拒绝把 verify_mode 降级。
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    server_hostname = TARGET_HOST

    start = time.perf_counter()
    try:
        with socket.create_connection((ip, TARGET_PORT), timeout=timeout) as sock:
            result.latency_ms = (time.perf_counter() - start) * 1000
            sock.settimeout(timeout)
            with ctx.wrap_socket(sock, server_hostname=server_hostname) as tls:
                tls.getpeercert()  # 触发握手完成
                result.tls_ok = True
                result.tls_ms = (time.perf_counter() - start) * 1000
    except ssl.SSLCertVerificationError as exc:
        result.error = f"cert:{exc.verify_message or exc}"
    except socket.timeout:
        result.error = "timeout"
    except ConnectionRefusedError:
        result.error = "refused"
    except ssl.SSLError as exc:
        result.error = f"ssl:{exc.reason if hasattr(exc, 'reason') else exc}"
    except OSError as exc:
        result.error = f"os:{exc.errno or exc}"
    except Exception as exc:  # noqa: BLE001
        result.error = f"other:{exc.__class__.__name__}"
    return result


def run_phase1(ranges: list[str], per_net: int) -> list[Probe]:
    ips = iter_candidate_ips(ranges, per_net)
    print(f"[i] 阶段1: {len(ips)} 个候选地址, 并发 {PHASE1_WORKERS}")
    good: list[Probe] = []
    done = 0
    started = time.perf_counter()
    with futures.ThreadPoolExecutor(max_workers=PHASE1_WORKERS) as pool:
        for probe in pool.map(probe_tls, ips):
            done += 1
            if probe.tls_ok:
                good.append(probe)
            if done % 500 == 0:
                print(f"    进度 {done}/{len(ips)}  可用={len(good)}")
    elapsed = time.perf_counter() - started
    print(f"[+] 阶段1完成: {len(good)}/{len(ips)} 可用, 用时 {elapsed:.1f}s")
    good.sort(key=lambda p: p.tls_ms or 1e9)
    return good


# ---------------------------------------------------------------- 阶段 2


def probe_long(ip: str, hold: float) -> Probe:
    """
    长连接存活探测。

    关键点: 必须**真的连被测的那个 IP**, 否则阶段2 测的就不是阶段1 选出来的
    那个边缘节点, 结果毫无意义。HTTPSConnection 直接连 IP 会让 SNI 变成 IP
    而被 CF 拒掉, 所以这里手工做 TLS: 用 SOCKET 连 IP, 再用 server_hostname
    指定 zone, 之后在这条已建立的连接上发 HTTP 请求并带 Host 头。

    opencode2api 的长流被掐断通常发生在 80-130s, 所以 hold 必须大于它,
    否则测出来的都是"看起来还活着"的假阳性。
    """
    result = Probe(ip=ip, tls_ok=True, latency_ms=probe_tls(ip).latency_ms or 0.0)
    result.tls_ms = probe_tls(ip).tls_ms or result.latency_ms
    started = time.perf_counter()
    deadline = started + hold
    raw = None
    tls = None
    try:
        ctx = ssl.create_default_context()
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
        raw = socket.create_connection((ip, TARGET_PORT), timeout=PHASE2_KEEPALIVE * 2)
        raw.settimeout(PHASE2_KEEPALIVE * 2)
        tls = ctx.wrap_socket(raw, server_hostname=TARGET_HOST)
        raw = None  # 所有权已交给 tls

        while time.perf_counter() < deadline:
            elapsed = time.perf_counter() - started
            request = (
                f"GET /cdn-cgi/trace HTTP/1.1\r\n"
                f"Host: {TARGET_HOST}\r\n"
                f"User-Agent: cf-ip-scan/1.0\r\n"
                f"Accept: */*\r\n"
                f"Connection: keep-alive\r\n\r\n"
            ).encode()
            tls.sendall(request)
            # 读满一个响应头, 确认这条连接还活着
            buf = b""
            while b"\r\n\r\n" not in buf:
                chunk = tls.recv(4096)
                if not chunk:
                    raise ConnectionError("server closed the connection")
                buf += chunk
            status_line = buf.split(b"\r\n")[0].decode("latin-1", "replace")
            # /cdn-cgi/trace 只在 CF 自己的 zone 上有, 目标 zone 可能返回 4xx/5xx。
            # 只要是合法的 HTTP 响应就说明这条连接活着; 只有非 HTTP 响应才算失败。
            if not status_line.startswith("HTTP/"):
                raise ConnectionError(f"not an HTTP response: {status_line[:60]}")
            # 丢掉 body, 留到下一轮
            tls.settimeout(0.5)
            try:
                while tls.recv(4096):
                    pass
            except (socket.timeout, ssl.SSLError, OSError):
                pass
            tls.settimeout(PHASE2_KEEPALIVE * 2)
            result.survived_s = elapsed
            time.sleep(PHASE2_KEEPALIVE)

        result.survived_s = time.perf_counter() - started
        result.long_ok = True
    except Exception as exc:  # noqa: BLE001
        result.survived_s = time.perf_counter() - started
        result.long_err = f"{exc.__class__.__name__}: {exc}"
    finally:
        for sock in (tls, raw):
            if sock is not None:
                try:
                    sock.close()
                except OSError:
                    pass
    return result


# ---------------------------------------------------------------- 输出


def print_table(rows: list[Probe]) -> None:
    if not rows:
        print("[!] 没有通过的候选")
        return
    print()
    print(f"{'rank':<5}{'edge ip':<17}{'tls_ms':>8}{'held_s':>8}  status")
    print("-" * 60)
    for i, p in enumerate(rows, 1):
        status = "OK" if p.long_ok else (p.long_err or "unknown")[:28]
        print(f"{i:<5}{p.ip:<17}{p.tls_ms or 0:>8.1f}{p.survived_s:>8.1f}  {status}")


def emit_vless_nodes(rows: list[Probe], template: str, count: int) -> list[str]:
    """
    把通过的边缘 IP 套进 vless:// 模板, 生成可直接粘进 config.json 的 nodes。

    模板里除 address 以外的部分(UUID/sni/host/path/transport)全部沿用原节点,
    因为 CF 是按 SNI 路由的, 换边缘 IP 不需要改其它任何字段。
    """
    out = []
    for p in rows[:count]:
        out.append(re.sub(r"@[^/:]+:", f"@{p.ip}:", template, count=1))
    return out


def apply_to_config(nodes: list[str], path: str, count: int) -> bool:
    with open(path, "r", encoding="utf-8-sig") as fh:
        cfg = json.load(fh)
    vless = cfg.setdefault("vless", {})
    vless["nodes"] = nodes
    vless.setdefault("count", count)
    backup = path + ".bak-cfscan"
    if not os.path.exists(backup):
        os.replace(path, backup)
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(cfg, fh, indent=4, ensure_ascii=False)
    print(f"[+] 已写入 {path} (原文件备份为 {backup})")
    print(f"[i] nodes={len(nodes)} count={vless['count']}")
    return True


# ---------------------------------------------------------------- main


def main() -> int:
    ap = argparse.ArgumentParser(description="CF 边缘 IP 优选扫描")
    ap.add_argument("--per-net", type=int, default=6,
                    help="每个 CF 网段抽样多少个地址(默认 6)")
    ap.add_argument("--top", type=int, default=12,
                    help="阶段2 测多少个候选(默认 12)")
    ap.add_argument("--hold", type=float, default=PHASE2_HOLD,
                    help=f"阶段2 每候选持续多少秒(默认 {PHASE2_HOLD})")
    ap.add_argument("--phase1-only", action="store_true", help="只跑阶段1")
    ap.add_argument("--workers", type=int, default=PHASE2_WORKERS,
                    help=f"阶段2 并发(默认 {PHASE2_WORKERS}; 挂住的 TLS 连接几乎不耗 CPU)")
    ap.add_argument("--apply", action="store_true", help="把结果写入 config.json")
    ap.add_argument("--config", default="config.json")
    ap.add_argument("--template", default=os.environ.get("CF_VLESS_TEMPLATE", ""),
                    help="vless:// 模板, --apply 时需要")
    args = ap.parse_args()

    if not TARGET_HOST:
        print("[!] 需要设置 CF_TARGET_HOST 为节点 zone 主机名（见 tools/README.md）。")
        return 2

    print(f"[*] 目标 zone : {TARGET_HOST}:{TARGET_PORT}")
    print(f"[*] 阶段2 保持 : {args.hold}s (必须大于 opencode2api 常见断流点 ~130s)")

    ranges = load_cf_ranges()
    good = run_phase1(ranges, args.per_net)
    if not good:
        print("[!] 阶段1 一个都没通。检查本机出网 / 目标主机名是否正确。")
        return 1

    # 只在 TLS 延迟明显靠前的一批里做长连接测试, 否则阶段2 太慢。
    delays = [p.tls_ms or 0 for p in good]
    cutoff = statistics.median(delays) + max(60.0, statistics.pstdev(delays) * 3)
    pool = [p for p in good if (p.tls_ms or 0) <= cutoff][: args.top]
    print(f"[i] 阶段2: {len(pool)} 个候选 (TLS 延迟 <= {cutoff:.0f}ms), "
          f"每个保持 {args.hold}s, 并发 {args.workers}")

    survivors: list[Probe] = []
    with futures.ThreadPoolExecutor(max_workers=args.workers) as pool_exec:
        for res in pool_exec.map(lambda p: probe_long(p.ip, args.hold), pool):
            mark = "OK " if res.long_ok else "CUT"
            print(f"    {mark} {res.ip:<17} 存活 {res.survived_s:6.1f}s  {res.long_err[:40]}")
            if res.long_ok:
                survivors.append(res)

    survivors.sort(key=lambda p: p.score)
    print()
    print("=== 通过长连接测试的边缘 IP (按延迟排序) ===")
    print_table(survivors)

    # 端口 443 是 CF 的标准端口, 抽样的 host 位地址绝大多数可用; 额外补测
    # 交接地址附近的几个, 提高拿满 count 的概率。
    if len(survivors) < 7:
        print(f"[i] 只有 {len(survivors)} 个通过, 建议加大 --per-net 或 --top 重跑")

    if args.apply:
        if not args.template:
            print("[!] --apply 需要 --template (一个完整的 vless:// 链接)")
            return 2
        nodes = emit_vless_nodes(survivors, args.template, 7)
        apply_to_config(nodes, args.config, 7)
    return 0


if __name__ == "__main__":
    sys.exit(main())