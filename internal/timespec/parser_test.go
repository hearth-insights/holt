package timespec

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		want    int64 // For absolute times
		wantErr bool
	}{
		{
			name:    "RFC3339",
			spec:    "2025-10-29T13:00:00Z",
			want:    1761742800000,
			wantErr: false,
		},
		{
			name:    "Duration",
			spec:    "1h",
			wantErr: false, // Can't check exact value easily
		},
		{
			name:    "Invalid",
			spec:    "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.want != 0 && got != tt.want {
				t.Errorf("Parse() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseRange(t *testing.T) {
	tests := []struct {
		name      string
		since     string
		until     string
		wantErr   bool
		checkVals func(since, until int64) bool
	}{
		{
			name:    "Absolute Range",
			since:   "2025-10-29T13:00:00Z",
			until:   "2025-10-29T14:00:00Z",
			wantErr: false,
			checkVals: func(since, until int64) bool {
				return since == 1761742800000 && until == 1761746400000
			},
		},
		{
			name:    "Mixed Range",
			since:   "2h",
			until:   "2099-01-01T00:00:00Z", // Far future
			wantErr: false,
			checkVals: func(since, until int64) bool {
				// 2099-01-01T00:00:00Z is 4070908800000 ms
				return since > 0 && until == 4070908800000
			},
		},
		{
			name:    "Invalid Range (since > until)",
			since:   "2025-10-29T15:00:00Z",
			until:   "2025-10-29T14:00:00Z",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			since, until, err := ParseRange(tt.since, tt.until)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRange() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.checkVals != nil {
				if !tt.checkVals(since, until) {
					t.Errorf("ParseRange() values check failed: since=%v, until=%v", since, until)
				}
			}
		})
	}
}
