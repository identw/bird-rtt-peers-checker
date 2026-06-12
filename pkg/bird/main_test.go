package bird

import "testing"

func TestParseBgpProtocolOutput(t *testing.T) {
	output := []byte(`Name       Proto      Table      State  Since         Info
peer1      BGP        ---        up     2026-06-11    Established
  BGP state:          Established
  Neighbor address:  10.52.161.77
  Neighbor AS:       65001
  State:             UP
  Routes:            1523 imported, 42 exported, 1523 accepted
`)

	peer, err := parseBgpProtocolOutput(output)
	if err != nil {
		t.Fatalf("parseBgpProtocolOutput: %v", err)
	}
	if peer.IP != "10.52.161.77" {
		t.Fatalf("IP = %q, want 10.52.161.77", peer.IP)
	}
	if !peer.State {
		t.Fatal("expected BGP state UP")
	}
	if peer.PrefixesImported != 1523 {
		t.Fatalf("PrefixesImported = %d, want 1523", peer.PrefixesImported)
	}
	if peer.PrefixesExported != 42 {
		t.Fatalf("PrefixesExported = %d, want 42", peer.PrefixesExported)
	}
}

func TestParseBgpProtocolOutput_NoRoutes(t *testing.T) {
	output := []byte(`Neighbor address: 10.0.0.1
State: DOWN
`)
	peer, err := parseBgpProtocolOutput(output)
	if err != nil {
		t.Fatalf("parseBgpProtocolOutput: %v", err)
	}
	if peer.State {
		t.Fatal("expected BGP state down")
	}
	if peer.PrefixesImported != 0 || peer.PrefixesExported != 0 {
		t.Fatalf("expected zero prefixes, got imported=%d exported=%d", peer.PrefixesImported, peer.PrefixesExported)
	}
}
