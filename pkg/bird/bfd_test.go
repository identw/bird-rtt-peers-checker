package bird

import "testing"

func TestParseBfdSessionsOutput(t *testing.T) {
	output := []byte(`BIRD 2.0.6 ready.
bfd1:
IP address                Interface  State      Since       Interval  Timeout
10.5.40.2                 bond0      Up         20:14:51.479 0.300    0.000
10.5.40.3                 bond0      Down       20:14:51.479 0.300    0.000
10.5.40.4                 bond0      Init       20:14:51.479 1.000    3.000
`)

	sessions := parseBfdSessionsOutput(output)
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	byIP := BfdSessionsByIP(sessions)
	cases := map[string]bool{
		"10.5.40.2": true,
		"10.5.40.3": false,
		"10.5.40.4": false,
	}
	for ip, want := range cases {
		got, ok := byIP[ip]
		if !ok {
			t.Fatalf("missing session for %s", ip)
		}
		if got.State != want {
			t.Fatalf("session %s: expected state %v, got %v", ip, want, got.State)
		}
	}
}

func TestParseBfdSessionsOutput_RealWorld(t *testing.T) {
	output := []byte(`bird> show bfd sessions 
bfd1:
IP address                Interface  State      Since         Interval  Timeout
10.52.161.77              oc_91-1-501-4 Up         05:05:33.540    1.000    5.000
10.52.161.65              oc_91-1-201-1 Up         2026-06-11      1.000    5.000
10.52.161.89              oc_91-1-300-1 Up         2026-06-11      1.000    5.000
10.52.161.85              oc_91-1-150-1 Up         2026-06-11      1.000    5.000
10.52.161.69              oc_91-1-501-2 Up         22:53:34.401    1.000    5.000
`)

	sessions := parseBfdSessionsOutput(output)
	if len(sessions) != 5 {
		t.Fatalf("expected 5 sessions, got %d: %+v", len(sessions), sessions)
	}

	byIP := BfdSessionsByIP(sessions)
	for _, ip := range []string{
		"10.52.161.77", "10.52.161.65", "10.52.161.89", "10.52.161.85", "10.52.161.69",
	} {
		s, ok := byIP[ip]
		if !ok {
			t.Fatalf("missing session for %s", ip)
		}
		if !s.State {
			t.Fatalf("session %s expected Up, got down", ip)
		}
		if s.Interval != 1.000 || s.Timeout != 5.000 {
			t.Fatalf("session %s interval/timeout = %v/%v, want 1.000/5.000", ip, s.Interval, s.Timeout)
		}
	}
}

func TestParseBfdSessionsOutput_NoBfd(t *testing.T) {
	output := []byte(`BIRD 2.0.6 ready.
There is no BFD protocol running
`)
	sessions := parseBfdSessionsOutput(output)
	if len(sessions) != 0 {
		t.Fatalf("expected no sessions, got %d", len(sessions))
	}
}
