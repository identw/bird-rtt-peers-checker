package metrics

import "testing"

func TestVPNFromPeer(t *testing.T) {
	tests := []struct {
		peer string
		want string
	}{
		{peer: "vpn501-1_oc", want: "oc"},
		{peer: "oc_91-1-501-4", want: "oc"},
		{peer: "peer", want: "unknown"},
		{peer: "foo_bar_baz", want: "baz"},
		{peer: "myvpn_91-1-501-4", want: "myvpn"},
	}
	for _, tt := range tests {
		if got := VPNFromPeer(tt.peer); got != tt.want {
			t.Fatalf("VPNFromPeer(%q) = %q, want %q", tt.peer, got, tt.want)
		}
	}
}
