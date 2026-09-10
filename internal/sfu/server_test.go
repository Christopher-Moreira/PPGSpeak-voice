package sfu

import "testing"

func TestOriginAllowed(t *testing.T) {
	allowed := []string{"http://localhost", "http://localhost:3000"}
	if !originAllowed("http://localhost", allowed) {
		t.Fatal("configured origin should be accepted")
	}
	if originAllowed("https://evil.example", allowed) {
		t.Fatal("unconfigured origin should be rejected")
	}
	if !originAllowed("", allowed) {
		t.Fatal("non-browser clients without Origin should be accepted")
	}
}
