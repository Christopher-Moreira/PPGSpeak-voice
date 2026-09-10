package sfu

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

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

func TestMediaSources(t *testing.T) {
	tests := []struct {
		source MediaSource
		kind   webrtc.RTPCodecType
	}{
		{MediaSourceMicrophone, webrtc.RTPCodecTypeAudio},
		{MediaSourceCamera, webrtc.RTPCodecTypeVideo},
		{MediaSourceScreen, webrtc.RTPCodecTypeVideo},
		{MediaSourceScreenAudio, webrtc.RTPCodecTypeAudio},
	}
	for _, test := range tests {
		if !test.source.valid() {
			t.Fatalf("source %q should be valid", test.source)
		}
		if got := test.source.kind(); got != test.kind {
			t.Fatalf("source %q kind = %v, want %v", test.source, got, test.kind)
		}
	}
	if MediaSource("unknown").valid() {
		t.Fatal("unknown source should be rejected")
	}
}
