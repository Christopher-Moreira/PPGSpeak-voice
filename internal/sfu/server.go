// Package sfu implements a small selective forwarding unit for voice, camera,
// and screen sharing.
package sfu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"

	"github.com/mercadophone/ppg-speak-voice/internal/config"
	"github.com/mercadophone/ppg-speak-voice/internal/ticket"
)

const maxSignalMessage = 256 << 10

type Server struct {
	cfg      config.Config
	api      *webrtc.API
	manager  *Manager
	verifier *ticket.Verifier
	webhooks *webhookClient
	logger   *slog.Logger
	upgrader websocket.Upgrader
	accepted atomic.Uint64
	rejected atomic.Uint64
}

func NewServer(cfg config.Config, logger *slog.Logger) (*Server, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("register media codecs: %w", err)
	}
	registry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, registry); err != nil {
		return nil, fmt.Errorf("register interceptors: %w", err)
	}
	settings := webrtc.SettingEngine{}
	if err := settings.SetEphemeralUDPPortRange(cfg.UDPPortMin, cfg.UDPPortMax); err != nil {
		return nil, fmt.Errorf("configure UDP range: %w", err)
	}
	if cfg.PublicIP != "" {
		settings.SetNAT1To1IPs([]string{cfg.PublicIP}, webrtc.ICECandidateTypeHost)
	}

	server := &Server{
		cfg: cfg,
		api: webrtc.NewAPI(
			webrtc.WithMediaEngine(mediaEngine),
			webrtc.WithInterceptorRegistry(registry),
			webrtc.WithSettingEngine(settings),
		),
		manager: NewManager(), verifier: ticket.NewVerifier(cfg.TokenSecret), logger: logger,
		webhooks: newWebhookClient(cfg.WebhookURL, cfg.WebhookSecret, logger),
	}
	server.upgrader = websocket.Upgrader{
		HandshakeTimeout: 5 * time.Second,
		CheckOrigin:      func(r *http.Request) bool { return originAllowed(r.Header.Get("Origin"), cfg.AllowedOrigins) },
	}
	return server, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("GET /voice/ws", s.handleWebSocket)
	return mux
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.rejected.Add(1)
		return
	}
	conn.SetReadLimit(maxSignalMessage)
	_ = conn.SetReadDeadline(time.Now().Add(s.cfg.JoinTimeout))
	var first clientMessage
	if err := conn.ReadJSON(&first); err != nil || first.Type != "join" || first.Token == "" {
		_ = conn.WriteJSON(serverMessage{Type: "error", Code: "invalid_join", Message: "a signed join ticket is required"})
		_ = conn.Close()
		s.rejected.Add(1)
		return
	}
	claims, err := s.verifier.Verify(first.Token)
	if err != nil {
		_ = conn.WriteJSON(serverMessage{Type: "error", Code: "invalid_ticket", Message: "voice ticket is invalid or expired"})
		_ = conn.Close()
		s.rejected.Add(1)
		return
	}
	participant, err := s.newParticipant(conn, claims)
	if err != nil {
		_ = conn.WriteJSON(serverMessage{Type: "error", Code: "rtc_setup_failed", Message: "could not create WebRTC transport"})
		_ = conn.Close()
		s.rejected.Add(1)
		return
	}
	s.accepted.Add(1)
	s.runParticipant(participant)
}

func (s *Server) newParticipant(conn *websocket.Conn, claims ticket.Claims) (*Participant, error) {
	pc, err := s.api.NewPeerConnection(webrtc.Configuration{ICEServers: s.cfg.ICEServers})
	if err != nil {
		return nil, err
	}
	p := &Participant{
		id: claims.Subject, name: claims.Name, pc: pc, ws: conn, logger: s.logger,
		senders: make(map[string]*webrtc.RTPSender), inbound: make(map[MediaSource]*publishedTrack),
		midSources: make(map[string]MediaSource), sourceMIDs: make(map[MediaSource]string),
		enabled: make(map[MediaSource]bool),
	}
	for _, kind := range []webrtc.RTPCodecType{
		webrtc.RTPCodecTypeAudio,
		webrtc.RTPCodecTypeVideo,
		webrtc.RTPCodecTypeVideo,
		webrtc.RTPCodecTypeAudio,
	} {
		_, addErr := pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		})
		if addErr != nil {
			_ = pc.Close()
			return nil, addErr
		}
	}
	p.room = s.manager.room(claims.Room)
	return p, nil
}

func (s *Server) runParticipant(p *Participant) {
	participants, tracks, replaced := p.room.join(p)
	p.onClose = func() {
		if !p.room.leave(p) {
			return
		}
		callbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.webhooks.send(callbackCtx, "participant_left", p)
		s.logger.Info("voice participant left", "room", p.room.id, "user", p.id)
	}
	if replaced != nil {
		replaced.Close()
	}

	p.pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		value := candidate.ToJSON()
		_ = p.send(serverMessage{Type: "candidate", Candidate: &value})
	})
	p.pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		switch state {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			p.Close()
		case webrtc.PeerConnectionStateDisconnected:
			go func() {
				time.Sleep(10 * time.Second)
				if p.pc.ConnectionState() == webrtc.PeerConnectionStateDisconnected {
					p.Close()
				}
			}()
		}
	})
	p.pc.OnTrack(func(remote *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		source, mid, ok := p.sourceFor(receiver)
		if !ok {
			p.logger.Warn("ignoring unmapped inbound media", "room", p.room.id, "user", p.id, "track", remote.ID(), "mid", mid, "kind", remote.Kind())
			return
		}
		if remote.Kind() != source.kind() {
			p.logger.Warn("ignoring media with mismatched kind", "room", p.room.id, "user", p.id, "source", source, "kind", remote.Kind())
			return
		}
		if remote.Kind() == webrtc.RTPCodecTypeAudio && !strings.EqualFold(remote.Codec().MimeType, webrtc.MimeTypeOpus) {
			return
		}
		p.logger.Info("inbound media started", "room", p.room.id, "user", p.id, "source", source, "mid", mid, "codec", remote.Codec().MimeType)
		trackID := p.id + "-" + string(source)
		local, err := webrtc.NewTrackLocalStaticRTP(remote.Codec().RTPCodecCapability, trackID, "media-"+p.id)
		if err != nil {
			return
		}
		p.setInbound(source, local, uint32(remote.SSRC()))
		if p.sourceEnabled(source) {
			p.room.publish(p, source, local, uint32(remote.SSRC()))
			defer p.room.unpublish(p, source)
		}
		for {
			packet, _, err := remote.ReadRTP()
			if err != nil {
				return
			}
			if err := local.WriteRTP(packet); err != nil && !errors.Is(err, io.ErrClosedPipe) {
				return
			}
		}
	})

	iceServers := make([]iceServerJSON, 0, len(s.cfg.ICEServers))
	for _, server := range s.cfg.ICEServers {
		iceServers = append(iceServers, iceServerJSON{URLs: server.URLs, Username: server.Username, Credential: server.Credential})
	}
	trackInfos := make([]TrackInfo, 0, len(tracks))
	for _, track := range tracks {
		trackInfos = append(trackInfos, track.info())
	}
	if err := p.send(serverMessage{
		Type: "welcome", Participant: &ParticipantInfo{ID: p.id, Name: p.name},
		Participants: participants, Tracks: trackInfos, ICEServers: iceServers,
	}); err != nil {
		p.Close()
		return
	}
	for _, track := range tracks {
		p.addOutbound(track)
	}
	p.room.broadcastJoined(p)
	go func() {
		webhookCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.webhooks.send(webhookCtx, "participant_joined", p)
	}()
	s.logger.Info("voice participant joined", "room", p.room.id, "user", p.id)
	p.requestNegotiation()

	_ = p.ws.SetReadDeadline(time.Now().Add(45 * time.Second))
	p.ws.SetPongHandler(func(string) error {
		return p.ws.SetReadDeadline(time.Now().Add(45 * time.Second))
	})
	pingDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pingDone:
				return
			case <-ticker.C:
				p.writeMu.Lock()
				_ = p.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
				err := p.ws.WriteMessage(websocket.PingMessage, nil)
				p.writeMu.Unlock()
				if err != nil {
					p.Close()
					return
				}
			}
		}
	}()
	defer close(pingDone)
	defer p.Close()

	for {
		var message clientMessage
		if err := p.ws.ReadJSON(&message); err != nil {
			return
		}
		switch message.Type {
		case "answer":
			if message.SDP == nil || p.acceptAnswer(*message.SDP) != nil {
				return
			}
		case "candidate":
			if message.Candidate == nil || p.pc.AddICECandidate(*message.Candidate) != nil {
				return
			}
		case "media_state":
			if !message.Source.valid() || message.Enabled == nil || message.MID == "" {
				_ = p.send(serverMessage{Type: "error", Code: "invalid_media_state", Message: "invalid media source state"})
				continue
			}
			p.setSourceEnabled(message.Source, *message.Enabled, message.MID)
		case "leave":
			return
		default:
			_ = p.send(serverMessage{Type: "error", Code: "unsupported_message", Message: "unsupported signaling message"})
		}
	}
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	rooms, participants := s.manager.Counts()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "# HELP ppg_voice_rooms Active voice rooms.\n# TYPE ppg_voice_rooms gauge\nppg_voice_rooms %d\n", rooms)
	_, _ = fmt.Fprintf(w, "# HELP ppg_voice_participants Active WebRTC participants.\n# TYPE ppg_voice_participants gauge\nppg_voice_participants %d\n", participants)
	_, _ = fmt.Fprintf(w, "# HELP ppg_voice_connections_total Accepted voice signaling connections.\n# TYPE ppg_voice_connections_total counter\nppg_voice_connections_total %d\n", s.accepted.Load())
	_, _ = fmt.Fprintf(w, "# HELP ppg_voice_rejected_total Rejected voice signaling connections.\n# TYPE ppg_voice_rejected_total counter\nppg_voice_rejected_total %d\n", s.rejected.Load())
}

func originAllowed(origin string, allowed []string) bool {
	if origin == "" {
		return true
	}
	for _, candidate := range allowed {
		if candidate == "*" || strings.EqualFold(strings.TrimSpace(candidate), origin) {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
