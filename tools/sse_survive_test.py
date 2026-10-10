#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
决定性实验: 真实 SSE 长流在**不同 CF 边缘 IP** 上能存活多久。

背景
----
cf_ip_scan.py 的阶段2 证明: 24 个边缘 IP 都能稳定保持 150 秒连接。
但真实 opencode.ai 的 SSE 长响应却在 80-130 秒被掐断。
两者矛盾, 说明掐断点不在 CF 边缘, 而在隧道更深处。

这个脚本直接回答那个唯一要紧的问题:
    **换 CF 边缘 IP 到底能不能救回长流?**

判定
----
  - 部分 IP 能跑过 200s  -> 边缘 IP 有差别, 优选 IP 方案有效, 用它挑 IP
  - 全部 IP 都在 ~100s 断 -> 是 Workers/edgetunnel 或 VLESS 后端的限制,
                          换边缘 IP 没用, 要换隧道方案

用法
----
    python tools/sse_survive_test.py                 # 用扫描结果里的候选
    python tools/sse_survive_test.py 1.1.1.1 2.2.2.2 # 指定 IP
"""
import contextlib
import json
import os
import re
import socket
import subprocess
import sys
import tempfile
import time
from concurrent.futures import ThreadPoolExecutor

XRAY = os.environ.get("XRAY_PATH", r"E:\opencode2api\bin\xray\xray.exe")
# Node credentials come from the environment so nothing sensitive lands in the
# repository. See tools/README.md for the required variables.
UUID = os.environ.get("VLESS_UUID", "")
SNI = os.environ.get("VLESS_SNI", "")
HOST_HDR = SNI
WS_PATH = os.environ.get("VLESS_PATH", "/")
NETWORK = os.environ.get("VLESS_NETWORK", "ws")
MODE = os.environ.get("VLESS_MODE", "")
EXTRA = os.environ.get("VLESS_EXTRA", "")
PORT_BASE = int(os.environ.get("SSE_PROBE_PORT_BASE", "24960"))

# 真实 SSE 负载: 长输出 + 高 reasoning, 尽量把流拖长
PAYLOAD = json.dumps({
    "model": "space-bunny-free",
    "stream": True,
    "reasoning_effort": "high",
    "max_tokens": 32000,
    "messages": [{"role": "user", "content": (
        "Write an extremely long exhaustive technical treatise of at least "
        "20000 words on distributed systems consensus. Cover Paxos, Raft, Zab "
        "and EPaxos exhaustively with worked examples, proofs, failure "
        "scenarios and comparative analysis. Keep writing, do not stop early, "
        "do not summarize.")}],
})

DEFAULT_IPS = [
    "108.162.192.1", "173.245.58.169", "190.93.245.85",
    "104.16.0.1", "104.17.153.153", "190.93.247.254",
]


def xray_config(edge_ip, port):
    return {
        "log": {"loglevel": "error"},
        "inbounds": [{
            "tag": "socks-in", "listen": "127.0.0.1", "port": port,
            "protocol": "socks",
            "settings": {"auth": "noauth", "udp": False},
            "sniffing": {"enabled": True, "destOverride": ["http", "tls"]},
        }],
        "outbounds": [{
            "tag": "vless-out", "protocol": "vless",
            "settings": {"vnext": [{
                "address": edge_ip, "port": 443,
                "users": [{"id": UUID, "encryption": "none"}],
            }]},
            "streamSettings": transport_settings(),
        }],
    }


def transport_settings():
    """Render the stream settings for VLESS_NETWORK (ws or xhttp).

    xhttp needs the transfer mode and the padding extras the Worker was
    configured with; without them an edgetunnel Worker drops the request
    during the handshake, which looks exactly like a dead proxy.
    """
    stream = {
        "network": NETWORK, "security": "tls",
        "tlsSettings": {"serverName": SNI, "fingerprint": "random"},
    }
    if NETWORK == "xhttp":
        settings = {"path": WS_PATH}
        if MODE:
            settings["mode"] = MODE
        if EXTRA:
            with contextlib.suppress(json.JSONDecodeError):
                settings["extra"] = json.loads(EXTRA)
        stream["xhttpSettings"] = settings
    else:
        stream["wsSettings"] = {"path": WS_PATH, "headers": {"Host": HOST_HDR}}
    return stream


def wait_port(port, timeout=15):
    end = time.time() + timeout
    while time.time() < end:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.5):
                return True
        except OSError:
            time.sleep(0.2)
    return False


def test_one(edge_ip, port, max_time=260):
    """起一个 xray 指向该边缘 IP, 跑真实 SSE, 返回 (存活秒数, 是否完整, 说明)。"""
    cfg_path = os.path.join(tempfile.gettempdir(), f"sse-{port}.json")
    body_path = os.path.join(tempfile.gettempdir(), f"sse-{port}-body.json")
    out_path = os.path.join(tempfile.gettempdir(), f"sse-{port}-out.txt")
    with open(cfg_path, "w", encoding="utf-8") as fh:
        json.dump(xray_config(edge_ip, port), fh)
    with open(body_path, "w", encoding="utf-8") as fh:
        fh.write(PAYLOAD)

    proc = subprocess.Popen(
        [XRAY, "run", "-config", cfg_path],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    started = time.perf_counter()
    try:
        if not wait_port(port):
            return edge_ip, 0.0, False, "xray 未监听"
        res = subprocess.run(
            ["curl.exe", "-sS", "--socks5-hostname", f"127.0.0.1:{port}",
             "-N", "--max-time", str(max_time),
             "-H", "Authorization: Bearer public",
             "-H", "x-opencode-client: cli",
             "-H", "Content-Type: application/json",
             "--data-binary", f"@{body_path}",
             "-o", out_path,
             "-w", "%{http_code}",           # 只有完整结束才会真正写出
             "https://opencode.ai/zen/v1/chat/completions"],
            capture_output=True, text=True, timeout=max_time + 30,
        )
        secs = time.perf_counter() - started
        code = (res.stdout or "").strip()
        complete = False
        size = 0
        if os.path.exists(out_path):
            size = os.path.getsize(out_path)
            with open(out_path, "r", encoding="utf-8", errors="replace") as fh:
                complete = "[DONE]" in fh.read()
        if code == "200" and complete:
            return edge_ip, secs, True, f"完整 (HTTP 200, {size//1024} KB)"
        if code == "200":
            return edge_ip, secs, False, f"截断 ({size//1024} KB, 无 [DONE])"
        err = (res.stderr or "").strip().splitlines()
        return edge_ip, secs, False, (err[-1][:60] if err else f"HTTP {code or '000'}")
    except subprocess.TimeoutExpired:
        return edge_ip, time.perf_counter() - started, False, "curl 超时"
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=8)
        except subprocess.TimeoutExpired:
            proc.kill()
        for p in (cfg_path, body_path, out_path):
            try:
                os.remove(p)
            except OSError:
                pass


def main():
    if not UUID or not SNI:
        print("[!] 需要先设置 VLESS_UUID 与 VLESS_SNI 环境变量（见 tools/README.md）。")
        print("    这些是节点凭据，故意不写进仓库。")
        return 2
    ips = sys.argv[1:] or DEFAULT_IPS
    print(f"[*] 真实 SSE 长流测试, 每个最多 260s, 并发 {len(ips)}")
    print(f"[*] 目标: https://opencode.ai/zen/v1/chat/completions via {SNI}\n")
    print(f"{'edge ip':<19}{'survived':>10}  {'status':<28}")
    print("-" * 60)
    results = []
    with ThreadPoolExecutor(max_workers=len(ips)) as ex:
        futs = [ex.submit(test_one, ip, PORT_BASE + i) for i, ip in enumerate(ips)]
        for fut in futs:
            results.append(fut.result())
    for edge, secs, ok, note in sorted(results, key=lambda r: -r[1]):
        print(f"{edge:<19}{secs:>9.1f}s  {note:<28}")

    winners = [r for r in results if r[2]]
    print()
    if winners:
        print(f"=> {len(winners)}/{len(results)} 跑完整流。边缘 IP 有差别, "
              f"优选 IP 方案有效。")
        print("   可用: " + ", ".join(r[0] for r in winners))
    elif any(r[1] > 150 for r in results):
        print("=> 全部被截断, 但存活时间分散 -> 仍是边缘/路径差异, "
              "值得用更长 hold 继续筛。")
    else:
        print("=> 全部在相近时间被截断 -> 边缘 IP 无关, 限制在 "
              "Workers/edgetunnel 或 VLESS 后端。优选 IP 无效。")
    return 0


if __name__ == "__main__":
    sys.exit(main())