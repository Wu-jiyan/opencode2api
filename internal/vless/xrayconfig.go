package vless

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// instanceConfig renders a minimal Xray document with one SOCKS5 inbound and
// one vless outbound. Only the protocols implied by the node are emitted, so
// a plain ws+tls node never carries REALITY settings.
func instanceConfig(node Node, listenHost string, listenPort int) ([]byte, error) {
	inbound := map[string]any{
		"tag":      "socks-in",
		"listen":   listenHost,
		"port":     listenPort,
		"protocol": "socks",
		"settings": map[string]any{"auth": "noauth", "udp": true},
		"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
	}

	stream, err := streamSettings(node)
	if err != nil {
		return nil, err
	}
	outbound := map[string]any{
		"tag":      "vless-out",
		"protocol": "vless",
		"settings": map[string]any{
			"vnext": []map[string]any{{
				"address": node.Address,
				"port":    node.Port,
				"users": []map[string]any{{
					"id":         node.UUID,
					"encryption": firstNonEmpty(node.Encryption, "none"),
					"flow":       node.Flow,
				}},
			}},
		},
		"streamSettings": stream,
	}

	document := map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  []any{inbound},
		"outbounds": []any{outbound},
	}
	return json.Marshal(document)
}

func streamSettings(node Node) (map[string]any, error) {
	network := firstNonEmpty(node.Network, "tcp")
	stream := map[string]any{"network": network}
	switch network {
	case "ws":
		stream["wsSettings"] = map[string]any{
			"path":    firstNonEmpty(node.Path, "/"),
			"headers": headersOrEmpty(node.Host),
		}
	case "grpc":
		stream["grpcSettings"] = map[string]any{
			"serviceName": node.Service,
			"multiMode":   false,
		}
	case "h2", "http":
		stream["network"] = "h2"
		stream["httpSettings"] = map[string]any{
			"path":    firstNonEmpty(node.Path, "/"),
			"host":    hostList(node.Host),
			"headers": headersOrEmpty(node.Host),
		}
	case "httpupgrade":
		stream["httpupgradeSettings"] = map[string]any{
			"path":    firstNonEmpty(node.Path, "/"),
			"headers": headersOrEmpty(node.Host),
		}
	case "xhttp":
		stream["xhttpSettings"] = map[string]any{
			"path": firstNonEmpty(node.Path, "/"),
			"host": node.Host,
		}
	case "tcp", "raw":
		stream["network"] = "tcp"
		stream["tcpSettings"] = map[string]any{"header": map[string]any{"type": "none"}}
	default:
		return nil, fmt.Errorf("unsupported vless transport %q", network)
	}

	security := firstNonEmpty(node.Security, "none")
	switch security {
	case "tls":
		tls := map[string]any{"serverName": firstNonEmpty(node.SNI, node.Host, node.Address)}
		if len(node.ALPN) > 0 {
			tls["alpn"] = node.ALPN
		}
		if node.ClientFP != "" {
			tls["fingerprint"] = node.ClientFP
		}
		stream["security"] = "tls"
		stream["tlsSettings"] = tls
	case "reality":
		if node.PublicKey == "" {
			return nil, fmt.Errorf("reality node has no public key")
		}
		reality := map[string]any{
			"serverName":  firstNonEmpty(node.SNI, node.Host, node.Address),
			"publicKey":   node.PublicKey,
			"fingerprint": firstNonEmpty(node.ClientFP, "chrome"),
		}
		if node.ShortID != "" {
			reality["shortId"] = node.ShortID
		}
		if node.SpiderX != "" {
			reality["spiderX"] = node.SpiderX
		}
		stream["security"] = "reality"
		stream["realitySettings"] = reality
	case "none", "":
		stream["security"] = "none"
	default:
		return nil, fmt.Errorf("unsupported vless security %q", security)
	}
	return stream, nil
}

func headersOrEmpty(host string) map[string]string {
	if host == "" {
		return map[string]string{}
	}
	return map[string]string{"Host": host}
}

func hostList(host string) []string {
	if host == "" {
		return nil
	}
	return []string{host}
}

func listenAddress(host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + strconv.Itoa(port)
	}
	return host + ":" + strconv.Itoa(port)
}
