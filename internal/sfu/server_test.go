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

func TestDefaultSubscriptionsPreserveVoiceAndRequireScreenOptIn(t *testing.T) {
	if !defaultSubscribed(MediaSourceMicrophone) {
		t.Fatal("microphone should be subscribed by default")
	}
	if !defaultSubscribed(MediaSourceCamera) {
		t.Fatal("camera should be subscribed by default")
	}
	if defaultSubscribed(MediaSourceScreen) {
		t.Fatal("screen video should require an explicit subscription")
	}
	if defaultSubscribed(MediaSourceScreenAudio) {
		t.Fatal("screen audio should require an explicit subscription")
	}
}

func TestLegacyClientsRemainSubscribedDuringRollingDeploy(t *testing.T) {
	owner := &Participant{id: "publisher"}
	screen := &publishedTrack{owner: owner, source: MediaSourceScreen}
	legacy := &Participant{subscriptions: make(map[string]bool)}
	if !legacy.wantsTrack(screen) {
		t.Fatal("legacy clients should keep receiving screen tracks")
	}
	current := &Participant{selectiveSubscriptions: true, subscriptions: make(map[string]bool)}
	if current.wantsTrack(screen) {
		t.Fatal("current clients should opt in before receiving screen tracks")
	}
}
