#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
验证: edgetunnel(CF Pages) 的出口 IP 是否随 CF 边缘 IP 变化。

做法: 对每个候选 CF 边缘 IP, 起一个独立 xray 实例(vless 出站指向该 IP,
SNI/Host 固定为 zone 主机名), 然后通过它的 socks5 端口请求一个 IP 回显服务。
如果不同边缘 IP 看到不同的出口 IP, 就证明轮换边缘 IP = 轮换出口 IP。
"""
import json
import os
import re
import socket
import subprocess
import sys
import tempfile
import time

XRAY = os.environ.get("XRAY_PATH", r"E:\opencode2api\bin\xray\xray.exe")
# Credentials and endpoint come from the environment so nothing sensitive can be
# committed by accident. See tools/README.md for the required variables.
UUID = os.environ.get("VLESS_UUID", "")
SNI = os.environ.get("VLESS_SNI", "")
HOST_HDR = SNI
PATH = os.environ.get("VLESS_PATH", "/")
WORK = os.environ.get("TMP_EGRESS_DIR", os.path.join(tempfile.gettempdir(), "egress-probe"))
TRACE_URLS = [
    "https://cloudflare.com/cdn-cgi/trace",
    "https://api.ipify.org",
]


def wait_port(port, timeout=15):
    end = time.time() + timeout
    while time.time() < end:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.5):
                return True
        except OSError:
            time.sleep(0.2)
    return False


def make_config(edge_ip, port):
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
            "streamSettings": {
                "network": "ws", "security": "tls",
                "tlsSettings": {"serverName": SNI, "fingerprint": "random"},
                "wsSettings": {"path": PATH, "headers": {"Host": HOST_HDR}},
            },
        }],
    }


def probe_exit_ip(edge_ip, port, label):
    cfg_path = os.path.join(WORK, f"x-{port}.json")
    with open(cfg_path, "w", encoding="utf-8") as fh:
        json.dump(make_config(edge_ip, port), fh)

    proc = subprocess.Popen(
        [XRAY, "run", "-config", cfg_path],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    try:
        if not wait_port(port):
            return edge_ip, "xray-not-listening", ""
        # 每个 curl 都是一条全新连接 -> 全新出口 IP
        for url in TRACE_URLS:
            out = subprocess.run(
                ["curl.exe", "-sS", "--socks5-hostname", f"127.0.0.1:{port}",
                 "--max-time", "25", url],
                capture_output=True, text=True, timeout=40,
            )
            text = (out.stdout or "").strip()
            if not text:
                continue
            ip = None
            if "cdn-cgi/trace" in url:
                m = re.search(r"^ip=(\S+)", text, re.M)
                ip = m.group(1) if m else None
                loc = re.search(r"^loc=(\S+)", text, re.M)
                colo = re.search(r"^colo=(\S+)", text, re.M)
                if ip:
                    return edge_ip, ip, f"{label} colo={colo.group(1) if colo else '?'} loc={loc.group(1) if loc else '?'}"
            else:
                if re.match(r"^\d+\.\d+\.\d+\.\d+$", text):
                    return edge_ip, text, label
        return edge_ip, "no-response", label
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=8)
        except subprocess.TimeoutExpired:
            proc.kill()
        try:
            os.remove(cfg_path)
        except OSError:
            pass


def main():
    if not UUID or not SNI:
        print("[!] 需要先设置 VLESS_UUID 与 VLESS_SNI 环境变量（见 tools/README.md）。")
        print("    这些是节点凭据，故意不写进仓库。")
        return 2
    os.makedirs(WORK, exist_ok=True)
    edge_ips = sys.argv[1:] or [
        "108.162.192.1",
        "173.245.58.169",
        "190.93.245.85",
        "104.16.0.1",
    ]
    print(f"{'edge ip':<18}{'exit ip':<18}note")
    print("-" * 62)
    exits = {}
    for i, ip in enumerate(edge_ips):
        edge, exit_ip, note = probe_exit_ip(ip, 24900 + i, f"probe{i}")
        exits.setdefault(exit_ip, []).append(edge)
        print(f"{edge:<18}{exit_ip:<18}{note}")
        time.sleep(1)
    print()
    real = {k: v for k, v in exits.items() if k not in ("no-response", "xray-not-listening")}
    print(f"不同出口 IP 数量: {len(real)} / {len(edge_ips)}")
    if len(real) > 1:
        print("=> 结论: 出口 IP 随边缘 IP 变化, 轮换边缘 IP == 轮换出口 IP")
    elif len(real) == 1:
        print("=> 结论: 出口 IP 固定(与你的说法不符, 请复核)")
    try:
        os.rmdir(WORK)
    except OSError:
        pass


if __name__ == "__main__":
    main()