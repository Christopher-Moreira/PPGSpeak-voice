// Command smoke opens two synthetic Pion clients against a running voice
// service and verifies that Opus RTP is forwarded in both directions.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
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
}

type client struct {
	ws        *websocket.Conn
	pc        *webrtc.PeerConnection
	track     *webrtc.TrackLocalStaticRTP
	writeMu   sync.Mutex
	connected chan struct{}
	received  chan struct{}
	onceConn  sync.Once
	onceRTP   sync.Once
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
	fmt.Println("voice smoke passed: two peers exchanged Opus RTP through the SFU")
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
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	sequence := uint16(1)
	timestamp := uint32(960)
	for {
		select {
		case <-ctx.Done():
			return errors.New("timed out waiting for forwarded RTP")
		case <-ticker.C:
			packet := &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: sequence, Timestamp: timestamp}, Payload: []byte{0xf8, 0xff, 0xfe}}
			_ = first.track.WriteRTP(packet)
			_ = second.track.WriteRTP(packet)
			sequence++
			timestamp += 960
			select {
			case <-first.received:
				select {
				case <-second.received:
					return nil
				default:
				}
			default:
			}
		}
	}
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
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
	}, "microphone", "smoke")
	if err != nil {
		_ = pc.Close()
		_ = ws.Close()
		return nil, err
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		_ = pc.Close()
		_ = ws.Close()
		return nil, err
	}
	go func() {
		buffer := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buffer); err != nil {
				return
			}
		}
	}()
	c := &client{ws: ws, pc: pc, track: track, connected: make(chan struct{}), received: make(chan struct{})}
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
				c.onceRTP.Do(func() { close(c.received) })
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

func (c *client) readSignals() {
	for {
		var incoming message
		if err := c.ws.ReadJSON(&incoming); err != nil {
			return
		}
		switch incoming.Type {
		case "offer":
			if incoming.SDP == nil || c.pc.SetRemoteDescription(*incoming.SDP) != nil {
				return
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
