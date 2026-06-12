package main

import "testing"

func TestHistory_FailedFewLastChecks(t *testing.T) {
	tests := []struct {
		name          string
		failThreshold int
		entries       []bool
		expected      bool
	}{
		{
			name:          "fewer failures than threshold",
			failThreshold: 3,
			entries:       []bool{true, false, false},
			expected:      false,
		},
		{
			name:          "exactly at threshold",
			failThreshold: 3,
			entries:       []bool{true, false, false, false},
			expected:      true,
		},
		{
			name:          "failures mixed with successes",
			failThreshold: 3,
			entries:       []bool{false, true, true, false, true, false, true, false, true, false},
			expected:      false,
		},
		{
			name:          "last failures reach threshold",
			failThreshold: 3,
			entries:       []bool{false, true, true, false, true, false, true, false, false, false},
			expected:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := &History{FailThreshold: tt.failThreshold}
			for _, alive := range tt.entries {
				history.Record(alive)
			}
			result := history.FailedFewLastChecks()
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestHealthPeer_lastTcpActualAlive(t *testing.T) {
	hp := &HealthPeer{}
	if !hp.lastTcpActualAlive() {
		t.Fatal("expected true when no tcp data yet")
	}

	hp.TcpActualHasData = true
	hp.TcpActualAlive = false
	if hp.lastTcpActualAlive() {
		t.Fatal("expected false when last tcp check failed")
	}
}
