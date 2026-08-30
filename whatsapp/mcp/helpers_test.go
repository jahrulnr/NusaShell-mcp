package main

import "testing"

// TestNormalizePhone pins the contract for the E.164 normalization helper
// used by handlePairWithCode. The default country code is 62 (Indonesia).
//
// The function must:
//   - accept friend input ('+62 812-3456-7890', '0812-3456-7890', etc.)
//   - strip non-digits, drop leading zeros, prepend country code if missing
//   - leave already-international numbers untouched
//   - reject empty input, too-short input, and inputs that resolve to a
//     leading-zero E.164 form (which whatsmeow would refuse anyway, but we
//     want a clean error here instead of a cryptic server-side rejection).
func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		cc      string // country code override; "" → default 62
		want    string
		wantErr bool
	}{
		{"empty", "", "", "", true},
		{"only spaces and dashes", "+ - -", "", "", true},
		{"indonesian leading zero", "081234567890", "", "6281234567890", false},
		{"indonesian with spaces", "+62 812 3456 7890", "", "6281234567890", false},
		{"indonesian with dashes", "0812-3456-7890", "", "6281234567890", false},
		{"already international", "6281234567890", "", "6281234567890", false},
		{"us number", "12025551234", "1", "12025551234", false},
		{"us with formatting", "(202) 555-1234", "1", "12025551234", false},
		{"too short", "12345", "", "", true},
		{"too long", "1234567890123456", "", "", true},
		{"all zeros", "00000", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizePhone(tt.input, tt.cc)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizePhone(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("normalizePhone(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
