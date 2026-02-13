package main

import (
	"testing"
)

func TestHistory_FailedFewLastChecks(t *testing.T) {
	tests := []struct {
		name     string
		entries  []bool
		expected bool
	}{
		{
			name:     "History entries less than 3",
			entries:  []bool{true, false},
			expected: false,
		},
		{
			name:     "History entries equeal 3",
			entries:  []bool{true, false, true},
			expected: false,
		},
		{
			name:     "History last 3 entries(4) all false",
			entries:  []bool{true, false, false, false},
			expected: true,
		},
		{
			name:     "History last 3 entries(4) not all false",
			entries:  []bool{true, false, true, false},
			expected: false,
		},
		{
			name:     "History last 3 entries(10) not all false",
			entries:  []bool{false, true, true, false, true, false, true, false, true, false},
			expected: false,
		},
		{
			name:     "History last 3 entries(10) all false",
			entries:  []bool{false, true, true, false, true, false, true, false, false, false},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := &History{
				Entries: tt.entries,
				Size:    len(tt.entries),
			}
			result := history.FailedFewLastChecks()
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
