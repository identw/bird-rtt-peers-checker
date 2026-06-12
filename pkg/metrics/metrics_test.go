package metrics

import "testing"

func TestHostAliveFromChecks(t *testing.T) {
	tests := []struct {
		name            string
		icmpAlive       bool
		tcpActualAlive  bool
		tcpCheckEnabled bool
		tcpCheckEnforce bool
		want            bool
	}{
		{name: "icmp only", icmpAlive: true, want: true},
		{name: "icmp fail", icmpAlive: false, want: false},
		{
			name:            "tcp enforced both ok",
			icmpAlive:       true,
			tcpActualAlive:  true,
			tcpCheckEnabled: true,
			tcpCheckEnforce: true,
			want:            true,
		},
		{
			name:            "tcp enforced tcp fail",
			icmpAlive:       true,
			tcpActualAlive:  false,
			tcpCheckEnabled: true,
			tcpCheckEnforce: true,
			want:            false,
		},
		{
			name:            "tcp enabled not enforced ignores tcp",
			icmpAlive:       true,
			tcpActualAlive:  false,
			tcpCheckEnabled: true,
			tcpCheckEnforce: false,
			want:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hostAliveFromChecks(
				tt.icmpAlive,
				tt.tcpActualAlive,
				tt.tcpCheckEnabled,
				tt.tcpCheckEnforce,
			)
			if got != tt.want {
				t.Fatalf("hostAliveFromChecks() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBoolToFloat(t *testing.T) {
	if boolToFloat(true) != 1 {
		t.Fatal("expected 1 for true")
	}
	if boolToFloat(false) != 0 {
		t.Fatal("expected 0 for false")
	}
}
