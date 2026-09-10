// Command smoke opens two synthetic Pion clients against a running voice
// service and verifies that microphone Opus, camera VP8, screen VP8, and screen
// audio Opus RTP are forwarded in both directions.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/mercadophone/ppg-speak-voice/internal/ticket"
)

type message struct {
	Type      string                     `json:"type"`
	Token     string                     `json:"token,omitempty"`
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
	Source    string                     `json:"source,omitempty"`
	Enabled   *bool                      `json:"enabled,omitempty"`
	MID       string                     `json:"mid,omitempty"`
	Track     *trackInfo                 `json:"track,omitempty"`
	Tracks    []trackInfo                `json:"tracks,omitempty"`
}

type trackInfo struct {
	ID            string `json:"id"`
	ParticipantID string `json:"participantId"`
	Source        string `json:"source"`
}

type client struct {
	ws           *websocket.Conn
	pc           *webrtc.PeerConnection
	tracks       map[string]*webrtc.TrackLocalStaticRTP
	senders      map[string]*webrtc.RTPSender
	transceivers map[string]*webrtc.RTPTransceiver
	enabled      map[string]bool
	writeMu      sync.Mutex
	connected    chan struct{}
	received     chan string
	onceConn     sync.Once
	metadata     sync.Map
}

func main() {
	url := flag.String("url", "ws://127.0.0.1:8081/voice/ws", "voice signaling URL")
	flag.Parse()
	secret := os.Getenv("VOICE_TOKEN_SECRET")
	if len(secret) < 32 {
		fmt.Fprintln(os.Stderr, "VOICE_TOKEN_SECRET must contain at least 32 bytes")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := run(ctx, *url, secret); err != nil {
		fmt.Fprintln(os.Stderr, "smoke failed:", err)
		os.Exit(1)
	}
	fmt.Println("media smoke passed: two peers exchanged microphone, camera, screen, and screen audio RTP through the SFU")
}

func run(ctx context.Context, url, secret string) error {
	first, err := dial(url, signedTicket(secret, "smoke-a", "Smoke A"))
	if err != nil {
		return err
	}
	defer first.close()
	second, err := dial(url, signedTicket(secret, "smoke-b", "Smoke B"))
	if err != nil {
		return err
	}
	defer second.close()

	if err := waitBoth(ctx, first.connected, second.connected); err != nil {
		return fmt.Errorf("connect peers: %w", err)
	}
	if err := first.publishVideo(); err != nil {
		return fmt.Errorf("publish first peer video: %w", err)
	}
	if err := second.publishVideo(); err != nil {
		return fmt.Errorf("publish second peer video: %w", err)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	sequence := uint16(1)
	timestamp := uint32(960)
	firstReceived := make(map[string]bool)
	secondReceived := make(map[string]bool)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for forwarded RTP (first=%v second=%v)", firstReceived, secondReceived)
		case id := <-first.received:
			if strings.HasPrefix(id, "missing-metadata:") {
				return errors.New(id)
			}
			firstReceived[id] = true
			if receivedAll(firstReceived, "smoke-b") && receivedAll(secondReceived, "smoke-a") {
				return nil
			}
		case id := <-second.received:
			if strings.HasPrefix(id, "missing-metadata:") {
				return errors.New(id)
			}
			secondReceived[id] = true
			if receivedAll(firstReceived, "smoke-b") && receivedAll(secondReceived, "smoke-a") {
				return nil
			}
		case <-ticker.C:
			for _, peer := range []*client{first, second} {
				for _, source := range []string{"microphone", "screen_audio"} {
					_ = peer.tracks[source].WriteRTP(&rtp.Packet{
						Header:  rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: sequence, Timestamp: timestamp},
						Payload: []byte{0xf8, 0xff, 0xfe},
					})
				}
				for _, source := range []string{"camera", "screen"} {
					_ = peer.tracks[source].WriteRTP(&rtp.Packet{
						Header:  rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: sequence, Timestamp: timestamp},
						Payload: []byte{0x10, 0x00},
					})
				}
			}
			sequence++
			timestamp += 960
		}
	}
}

func receivedAll(received map[string]bool, owner string) bool {
	for _, source := range []string{"microphone", "camera", "screen", "screen_audio"} {
		if !received[owner+"-"+source] {
			return false
		}
	}
	return true
}

func dial(url, rawTicket string) (*client, error) {
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		_ = ws.Close()
		return nil, err
	}
	microphone, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
	}, "microphone", "smoke")
	if err != nil {
		_ = pc.Close()
		_ = ws.Close()
		return nil, err
	}
	camera, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
	}, "camera", "smoke")
	if err != nil {
		_ = pc.Close()
		_ = ws.Close()
		return nil, err
	}
	screen, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
	}, "screen", "smoke")
	if err != nil {
		_ = pc.Close()
		_ = ws.Close()
		return nil, err
	}
	screenAudio, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
	}, "screen_audio", "smoke")
	if err != nil {
		_ = pc.Close()
		_ = ws.Close()
		return nil, err
	}
	tracks := map[string]*webrtc.TrackLocalStaticRTP{
		"microphone":   microphone,
		"camera":       camera,
		"screen":       screen,
		"screen_audio": screenAudio,
	}
	senders := make(map[string]*webrtc.RTPSender)
	microphoneSender, addErr := pc.AddTrack(microphone)
	if addErr != nil {
		_ = pc.Close()
		_ = ws.Close()
		return nil, addErr
	}
	microphoneTransceiver, findErr := findTransceiver(pc, microphoneSender)
	if findErr != nil {
		_ = pc.Close()
		_ = ws.Close()
		return nil, findErr
	}
	transceivers := map[string]*webrtc.RTPTransceiver{"microphone": microphoneTransceiver}
	senders["microphone"] = microphoneSender
	go drainSender(microphoneSender)
	for _, slot := range []struct {
		source string
		kind   webrtc.RTPCodecType
	}{
		{"camera", webrtc.RTPCodecTypeVideo},
		{"screen", webrtc.RTPCodecTypeVideo},
		{"screen_audio", webrtc.RTPCodecTypeAudio},
	} {
		placeholder, placeholderErr := placeholderTrack(slot.source, slot.kind)
		if placeholderErr != nil {
			_ = pc.Close()
			_ = ws.Close()
			return nil, placeholderErr
		}
		sender, transceiverErr := pc.AddTrack(placeholder)
		if transceiverErr != nil {
			_ = pc.Close()
			_ = ws.Close()
			return nil, transceiverErr
		}
		transceiver, findTransceiverErr := findTransceiver(pc, sender)
		if findTransceiverErr != nil {
			_ = pc.Close()
			_ = ws.Close()
			return nil, findTransceiverErr
		}
		senders[slot.source] = sender
		transceivers[slot.source] = transceiver
		go drainSender(sender)
	}
	c := &client{
		ws: ws, pc: pc, tracks: tracks, senders: senders, transceivers: transceivers,
		enabled:   map[string]bool{"microphone": true},
		connected: make(chan struct{}), received: make(chan string, 8),
	}
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		value := candidate.ToJSON()
		_ = c.send(message{Type: "candidate", Candidate: &value})
	})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			c.onceConn.Do(func() { close(c.connected) })
		}
	})
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		go func() {
			if _, _, err := remote.ReadRTP(); err == nil {
				if _, ok := c.metadata.Load(remote.ID()); !ok {
					c.received <- "missing-metadata:" + remote.ID()
					return
				}
				c.received <- remote.ID()
			}
		}()
	})
	if err := c.send(message{Type: "join", Token: rawTicket}); err != nil {
		c.close()
		return nil, err
	}
	go c.readSignals()
	return c, nil
}

func placeholderTrack(source string, kind webrtc.RTPCodecType) (*webrtc.TrackLocalStaticRTP, error) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}
	if kind == webrtc.RTPCodecTypeAudio {
		codec = webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}
	}
	return webrtc.NewTrackLocalStaticRTP(codec, "placeholder-"+source, "smoke-placeholder")
}

func findTransceiver(pc *webrtc.PeerConnection, sender *webrtc.RTPSender) (*webrtc.RTPTransceiver, error) {
	for _, transceiver := range pc.GetTransceivers() {
		if transceiver.Sender() == sender {
			return transceiver, nil
		}
	}
	return nil, errors.New("sender transceiver not found")
}

func (c *client) readSignals() {
	for {
		var incoming message
		if err := c.ws.ReadJSON(&incoming); err != nil {
			return
		}
		switch incoming.Type {
		case "welcome":
			for _, track := range incoming.Tracks {
				c.metadata.Store(track.ID, track)
			}
		case "track_published":
			if incoming.Track != nil {
				c.metadata.Store(incoming.Track.ID, *incoming.Track)
			}
		case "track_unpublished":
			if incoming.Track != nil {
				c.metadata.Delete(incoming.Track.ID)
			}
		case "offer":
			if incoming.SDP == nil || c.pc.SetRemoteDescription(*incoming.SDP) != nil {
				return
			}
			for _, source := range []string{"microphone", "camera", "screen", "screen_audio"} {
				enabled := c.enabled[source]
				_ = c.send(message{
					Type: "media_state", Source: source, Enabled: &enabled, MID: c.transceivers[source].Mid(),
				})
			}
			answer, err := c.pc.CreateAnswer(nil)
			if err != nil || c.pc.SetLocalDescription(answer) != nil {
				return
			}
			_ = c.send(message{Type: "answer", SDP: &answer})
		case "candidate":
			if incoming.Candidate != nil {
				_ = c.pc.AddICECandidate(*incoming.Candidate)
			}
		}
	}
}

func (c *client) publishVideo() error {
	enabled := true
	for _, source := range []string{"camera", "screen", "screen_audio"} {
		if err := c.senders[source].ReplaceTrack(c.tracks[source]); err != nil {
			return err
		}
		c.enabled[source] = true
		if err := c.send(message{
			Type: "media_state", Source: source, Enabled: &enabled, MID: c.transceivers[source].Mid(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func drainSender(sender *webrtc.RTPSender) {
	buffer := make([]byte, 1500)
	for {
		if _, _, err := sender.Read(buffer); err != nil {
			return
		}
	}
}

func (c *client) send(value message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.WriteJSON(value)
}

func (c *client) close() {
	_ = c.send(message{Type: "leave"})
	_ = c.ws.Close()
	_ = c.pc.Close()
}

func signedTicket(secret, subject, name string) string {
	now := time.Now()
	claims := ticket.Claims{
		Room: "voice-smoke-room", Name: name,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "ppg-api", Subject: subject, Audience: jwt.ClaimStrings{"ppg-voice"},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
	}
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	return raw
}

func waitBoth(ctx context.Context, first, second <-chan struct{}) error {
	for _, ready := range []<-chan struct{}{first, second} {
		select {
		case <-ready:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
