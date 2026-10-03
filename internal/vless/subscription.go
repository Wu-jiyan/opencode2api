package vless

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Node is one upstream vless endpoint normalized from a subscription.
type Node struct {
	Address    string
	Port       int
	UUID       string
	Encryption string
	Flow       string
	Network    string
	Security   string
	SNI        string
	Host       string
	Path       string
	Service    string
	ALPN       []string
	PublicKey  string
	ShortID    string
	SpiderX    string
	// ClientFP is the uTLS fingerprint carried by the share link (fp=).
	ClientFP string
	// Fixed marks a node the operator configured by hand rather than one a
	// subscription returned, so diagnostics can tell a stable route from a
	// rotating candidate.
	Fixed bool
}

// DisplayName is a stable label used for logs and instance names.
func (n Node) DisplayName() string {
	if n.Host != "" {
		return n.Host
	}
	return net.JoinHostPort(n.Address, strconv.Itoa(n.Port))
}

// Endpoint identifies the physical server, which is what deduplication and
// rotation ordering care about.
func (n Node) Endpoint() string {
	return net.JoinHostPort(n.Address, strconv.Itoa(n.Port))
}

// Fingerprint returns a stable identity for the node's connection parameters.
func (n Node) Fingerprint() string {
	return strings.Join([]string{
		n.Endpoint(), n.UUID, n.Network, n.Security, n.SNI, n.Host, n.Path,
		n.Service, n.Flow, n.PublicKey, n.ShortID, strings.Join(n.ALPN, ","),
	}, "|")
}

// ParseSubscription decodes a subscription body into vless nodes. It accepts
// a base64-encoded v2ray list, a plain newline-separated share-link list, and
// a Clash/mihomo YAML document.
func ParseSubscription(body []byte) ([]Node, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, fmt.Errorf("subscription is empty")
	}
	if nodes, ok := parseV2ray(trimmed); ok {
		return nodes, nil
	}
	if nodes, ok := parseClash([]byte(trimmed)); ok {
		return nodes, nil
	}
	return nil, fmt.Errorf("subscription is neither a v2ray payload nor an inline Clash manifest")
}

func parseV2ray(body string) ([]Node, bool) {
	text := body
	if !strings.Contains(text, "://") {
		decoded, ok := decodeBase64(text)
		if !ok {
			return nil, false
		}
		text = decoded
	}
	var nodes []Node
	sawLink := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(strings.ToLower(line), "vless://") {
			continue
		}
		sawLink = true
		node, err := ParseURI(line)
		if err != nil {
			continue
		}
		nodes = append(nodes, node)
	}
	if !sawLink {
		return nil, false
	}
	return nodes, true
}

func decodeBase64(value string) (string, bool) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		}
		return r
	}, value)
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if decoded, err := encoding.DecodeString(cleaned); err == nil {
			return string(decoded), true
		}
	}
	return "", false
}

// ParseURI parses a single vless:// share link.
func ParseURI(raw string) (Node, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(raw), "vless://") {
		return Node{}, fmt.Errorf("not a vless link")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return Node{}, fmt.Errorf("parse vless link: %w", err)
	}
	uuid := parsed.User.Username()
	if uuid == "" {
		return Node{}, fmt.Errorf("vless link has no UUID")
	}
	host := parsed.Hostname()
	if host == "" {
		return Node{}, fmt.Errorf("vless link has no address")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return Node{}, fmt.Errorf("vless link has an invalid port")
	}
	query := parsed.Query()
	node := Node{
		Address:    host,
		Port:       port,
		UUID:       uuid,
		Encryption: firstNonEmpty(query.Get("encryption"), "none"),
		Flow:       query.Get("flow"),
		Network:    firstNonEmpty(query.Get("type"), "tcp"),
		Security:   firstNonEmpty(query.Get("security"), "none"),
		SNI:        query.Get("sni"),
		Host:       query.Get("host"),
		Path:       query.Get("path"),
		Service:    firstNonEmpty(query.Get("serviceName"), query.Get("servicename")),
		ClientFP:   query.Get("fp"),
		PublicKey:  query.Get("pbk"),
		ShortID:    query.Get("sid"),
		SpiderX:    query.Get("spx"),
	}
	if alpn := query.Get("alpn"); alpn != "" {
		for _, item := range strings.Split(alpn, ",") {
			if item = strings.TrimSpace(item); item != "" {
				node.ALPN = append(node.ALPN, item)
			}
		}
	}
	return node, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ParseFixedNodes expands the operator's hand-written share links into pool
// candidates.
//
// A Cloudflare-fronted node reaches the same origin through whichever edge
// address the link names, and the edge is what decides the outbound IP. So a
// node whose address is covered by preferredDomains is cloned once per domain,
// keeping UUID, SNI, host, path and transport intact — only the address differs.
// Every clone is a genuinely different outbound route, which is how a single
// fixed link can fill several listeners.
//
// endpoints names alternative origins for the same service. They are handed out
// round-robin rather than at random: each one carries its own request budget, so
// an even spread keeps the pool from draining one endpoint's allowance early
// while another sits unused. Only the host and SNI move — the UUID stays valid
// because it identifies the user, not the entry point.
//
// Nodes whose address is a literal IP, or that match no preferred domain, are
// taken as-is: a fixed link is a deliberate choice and must not be rewritten
// into something the operator did not ask for.
func ParseFixedNodes(links, preferredDomains, endpoints []string) ([]Node, error) {
	domains := make([]string, 0, len(preferredDomains))
	for _, domain := range preferredDomains {
		if value := strings.TrimSpace(domain); value != "" {
			domains = append(domains, value)
		}
	}
	origins := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if value := strings.TrimSpace(endpoint); value != "" {
			origins = append(origins, value)
		}
	}
	var out []Node
	for _, link := range links {
		node, err := ParseURI(link)
		if err != nil {
			return nil, fmt.Errorf("vless.nodes: %w", err)
		}
		node.Fixed = true
		matches := false
		for _, domain := range domains {
			if strings.EqualFold(node.Address, domain) {
				matches = true
				break
			}
		}
		if !matches || len(domains) == 0 {
			out = append(out, applyEndpoint(node, origins, len(out)))
			continue
		}
		for _, domain := range domains {
			clone := node
			clone.Address = domain
			out = append(out, applyEndpoint(clone, origins, len(out)))
		}
	}
	return out, nil
}

// applyEndpoint points a candidate at one of the alternative origins, cycling
// through them so consecutive candidates land on different ones. The original
// host is used as the SNI fallback so a node keeps working if an endpoint is
// later removed from the list.
func applyEndpoint(node Node, endpoints []string, position int) Node {
	if len(endpoints) == 0 {
		return node
	}
	endpoint := endpoints[position%len(endpoints)]
	node.Host = endpoint
	if node.SNI != "" {
		node.SNI = endpoint
	}
	return node
}

// applyPreferredOrder reorders fixed candidates to match a measured ranking.
// Candidates missing from the order keep their relative position at the end, so
// a ranking taken before a configuration change cannot drop a node.
func applyPreferredOrder(nodes []Node, order []string) []Node {
	if len(order) == 0 || len(nodes) < 2 {
		return nodes
	}
	rank := make(map[string]int, len(order))
	for i, address := range order {
		rank[strings.ToLower(address)] = i
	}
	out := append([]Node(nil), nodes...)
	sort.SliceStable(out, func(i, j int) bool {
		left, leftOK := rank[strings.ToLower(out[i].Address)]
		right, rightOK := rank[strings.ToLower(out[j].Address)]
		if leftOK != rightOK {
			return leftOK
		}
		return left < right
	})
	return out
}

type clashDocument struct {
	Proxies []clashProxy `yaml:"proxies"`
}

type clashProxy struct {
	Name              string            `yaml:"name"`
	Type              string            `yaml:"type"`
	Server            string            `yaml:"server"`
	Port              int               `yaml:"port"`
	UUID              string            `yaml:"uuid"`
	Network           string            `yaml:"network"`
	TLS               bool              `yaml:"tls"`
	SNI               string            `yaml:"sni"`
	ServerName        string            `yaml:"servername"`
	SkipCertVerify    bool              `yaml:"skip-cert-verify"`
	Flow              string            `yaml:"flow"`
	ClientFingerprint string            `yaml:"client-fingerprint"`
	ALPN              []string          `yaml:"alpn"`
	Encryption        string            `yaml:"encryption"`
	WSHeaders         map[string]string `yaml:"ws-headers"`
	WSHeadersOpt      map[string]string `yaml:"ws-opts-headers"`
	WSOpts            *clashWSOpts      `yaml:"ws-opts"`
	RealityOpts       *clashReality     `yaml:"reality-opts"`
	GrpcOpts          *clashGrpcOpts    `yaml:"grpc-opts"`
}

type clashWSOpts struct {
	Path    string            `yaml:"path"`
	Headers map[string]string `yaml:"headers"`
}

type clashReality struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id"`
}

type clashGrpcOpts struct {
	ServiceName string `yaml:"grpc-service-name"`
}

func parseClash(body []byte) ([]Node, bool) {
	var document clashDocument
	if err := yaml.Unmarshal(body, &document); err != nil || len(document.Proxies) == 0 {
		return nil, false
	}
	var nodes []Node
	for _, proxy := range document.Proxies {
		if !strings.EqualFold(strings.TrimSpace(proxy.Type), "vless") {
			continue
		}
		if proxy.Server == "" || proxy.Port < 1 || proxy.Port > 65535 || proxy.UUID == "" {
			continue
		}
		node := Node{
			Address: proxy.Server,
			Port:    proxy.Port,
			UUID:    proxy.UUID,
			Network: firstNonEmpty(proxy.Network, "tcp"),
		}
		if proxy.Encryption != "" {
			node.Encryption = proxy.Encryption
		} else {
			node.Encryption = "none"
		}
		if proxy.Flow != "" {
			node.Flow = proxy.Flow
		}
		node.SNI = firstNonEmpty(proxy.SNI, proxy.ServerName)
		node.ClientFP = proxy.ClientFingerprint
		node.ALPN = append(node.ALPN, proxy.ALPN...)

		security := "none"
		if proxy.RealityOpts != nil && proxy.RealityOpts.PublicKey != "" {
			security = "reality"
			node.PublicKey = proxy.RealityOpts.PublicKey
			node.ShortID = proxy.RealityOpts.ShortID
		} else if proxy.TLS {
			security = "tls"
		}
		node.Security = security

		headers := proxy.WSHeaders
		if len(proxy.WSOptsHeaders()) > 0 {
			headers = proxy.WSOptsHeaders()
		}
		if proxy.WSOpts != nil {
			node.Path = proxy.WSOpts.Path
			if len(headers) == 0 {
				headers = proxy.WSOpts.Headers
			}
		}
		for key, value := range headers {
			if strings.EqualFold(key, "host") {
				node.Host = value
			}
		}
		if proxy.GrpcOpts != nil {
			node.Service = proxy.GrpcOpts.ServiceName
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return nil, false
	}
	return nodes, true
}

func (p clashProxy) WSOptsHeaders() map[string]string {
	if len(p.WSHeadersOpt) > 0 {
		return p.WSHeadersOpt
	}
	return nil
}
