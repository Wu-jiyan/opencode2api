package vless

import "testing"

// Documentation values only: the domains come from the reserved example.com
// range and the UUID from the reserved 550e8400- range, so no real endpoint or
// credential can end up in the repository.
const (
	cfLink = "vless://550e8400-e29b-41d4-a716-446655440000@ct.example.com:443" +
		"?encryption=none&security=tls&sni=origin.example.com&fp=random&type=ws" +
		"&host=origin.example.com&path=%2F%3Fed%3D2560#Example"

	cfUUID   = "550e8400-e29b-41d4-a716-446655440000"
	cfOrigin = "origin.example.com"
)

func TestParseFixedNodesExpandsAcrossPreferredDomains(t *testing.T) {
	nodes, err := ParseFixedNodes([]string{cfLink}, []string{"cf.example.com", "ct.example.com"}, nil)
	if err != nil {
		t.Fatalf("ParseFixedNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(nodes))
	}
	for i, want := range []string{"cf.example.com", "ct.example.com"} {
		if nodes[i].Address != want {
			t.Errorf("node %d address = %q, want %q", i, nodes[i].Address, want)
		}
		// Only the edge address may change; everything else identifies the
		// origin and has to survive the substitution.
		if nodes[i].UUID != cfUUID {
			t.Errorf("node %d lost its UUID", i)
		}
		if nodes[i].SNI != cfOrigin || nodes[i].Host != cfOrigin {
			t.Errorf("node %d lost its SNI/Host", i)
		}
		if nodes[i].Path != "/?ed=2560" {
			t.Errorf("node %d path = %q, want %q", i, nodes[i].Path, "/?ed=2560")
		}
		if nodes[i].Port != 443 || nodes[i].Network != "ws" || nodes[i].Security != "tls" {
			t.Errorf("node %d lost its transport", i)
		}
		if !nodes[i].Fixed {
			t.Errorf("node %d is not marked fixed", i)
		}
	}
}

// A literal IP address is a deliberate choice. Rewriting it into a preferred
// domain would hand the operator a route they never asked for.
func TestParseFixedNodesKeepsUnrelatedAddress(t *testing.T) {
	const link = "vless://uuid@203.0.113.9:443?security=tls&type=tcp"
	nodes, err := ParseFixedNodes([]string{link}, []string{"cf.example.com"}, nil)
	if err != nil {
		t.Fatalf("ParseFixedNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
	if nodes[0].Address != "203.0.113.9" {
		t.Errorf("address = %q, want the configured 203.0.113.9", nodes[0].Address)
	}
}

func TestParseFixedNodesWithoutDomains(t *testing.T) {
	nodes, err := ParseFixedNodes([]string{cfLink}, nil, nil)
	if err != nil {
		t.Fatalf("ParseFixedNodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Address != "ct.example.com" {
		t.Fatalf("got %+v, want the single configured node", nodes)
	}
}

func TestParseFixedNodesRejectsBadLink(t *testing.T) {
	if _, err := ParseFixedNodes([]string{"https://example.com"}, nil, nil); err == nil {
		t.Fatal("expected an error for a non-vless link")
	}
}

// Each endpoint carries its own request budget, so consecutive candidates must
// land on different origins instead of draining the first one immediately.
func TestParseFixedNodesSpreadsEndpoints(t *testing.T) {
	endpoints := []string{"a.example.com", "b.example.com", "c.example.com"}
	nodes, err := ParseFixedNodes([]string{cfLink}, []string{
		"cf1.example.com", "ct.example.com", "cm.example.com",
		"cu.example.com", "as.example.com", "eu.example.com",
	}, endpoints)
	if err != nil {
		t.Fatalf("ParseFixedNodes: %v", err)
	}
	if len(nodes) != 6 {
		t.Fatalf("got %d nodes, want 6", len(nodes))
	}
	counts := map[string]int{}
	for i, node := range nodes {
		want := endpoints[i%len(endpoints)]
		if node.Host != want {
			t.Errorf("node %d host = %q, want %q", i, node.Host, want)
		}
		if node.SNI != want {
			t.Errorf("node %d SNI = %q, want %q", i, node.SNI, want)
		}
		// The UUID identifies the user, not the entry point, so switching
		// origins must not disturb it.
		if node.UUID != cfUUID {
			t.Errorf("node %d lost its UUID", i)
		}
		counts[node.Host]++
	}
	for _, endpoint := range endpoints {
		if counts[endpoint] != 2 {
			t.Errorf("endpoint %q served %d candidates, want an even 2", endpoint, counts[endpoint])
		}
	}
}

func TestParseFixedNodesWithoutEndpointsKeepsHost(t *testing.T) {
	nodes, err := ParseFixedNodes([]string{cfLink}, []string{"cf.example.com"}, nil)
	if err != nil {
		t.Fatalf("ParseFixedNodes: %v", err)
	}
	if nodes[0].Host != cfOrigin || nodes[0].SNI != cfOrigin {
		t.Fatalf("host/SNI = %q/%q, want the link's own %s", nodes[0].Host, nodes[0].SNI, cfOrigin)
	}
}

func TestApplyPreferredOrderKeepsUnknownAtEnd(t *testing.T) {
	nodes, err := ParseFixedNodes([]string{cfLink}, []string{"cf.example.com", "ct.example.com", "cu.example.com"}, nil)
	if err != nil {
		t.Fatalf("ParseFixedNodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(nodes))
	}
	// A ranking computed before "cu" was added must not drop it.
	ordered := applyPreferredOrder(nodes, []string{"ct.example.com", "cf.example.com"})
	got := []string{ordered[0].Address, ordered[1].Address, ordered[2].Address}
	want := []string{"ct.example.com", "cf.example.com", "cu.example.com"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
