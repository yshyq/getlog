package security

import "testing"

func TestDecodePathSegmentRejectsTraversalEncodings(t *testing.T) {
	for _, raw := range []string{"%2fetc", "%5Cetc", "%00", "report%252elog"} {
		if _, err := DecodePathSegment(raw); err == nil {
			t.Fatalf("DecodePathSegment(%q) accepted unsafe value", raw)
		}
	}
}

func TestValidateFileName(t *testing.T) {
	valid := []string{"access.log", "service 2026-07-10.log", "中文.log"}
	for _, value := range valid {
		if err := ValidateFileName(value); err != nil {
			t.Fatalf("ValidateFileName(%q): %v", value, err)
		}
	}
	for _, value := range []string{"", ".", "..", "a/b", "a\\b", "a%20b", "line\nbreak"} {
		if err := ValidateFileName(value); err == nil {
			t.Fatalf("ValidateFileName(%q) accepted unsafe value", value)
		}
	}
}

func TestValidateRangeHeader(t *testing.T) {
	for _, value := range []string{"", "bytes=0-9", "bytes=5-", "bytes=-64"} {
		if err := ValidateRangeHeader(value); err != nil {
			t.Fatalf("ValidateRangeHeader(%q): %v", value, err)
		}
	}
	for _, value := range []string{"bytes=9-0", "bytes=0-1,3-4", "items=0-1", "bytes=-"} {
		if err := ValidateRangeHeader(value); err == nil {
			t.Fatalf("ValidateRangeHeader(%q) accepted invalid value", value)
		}
	}
}
