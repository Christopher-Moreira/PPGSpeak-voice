package sfu

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

type Participant struct {
	id     string
	name   string
	room   *Room
	pc     *webrtc.PeerConnection
	ws     *websocket.Conn
	logger *slog.Logger

	writeMu sync.Mutex
	stateMu sync.Mutex
	senders map[string]*webrtc.RTPSender
	pending bool
	inOffer bool
	closed  atomic.Bool
	onClose func()
}

func (p *Participant) send(message serverMessage) error {
	if p.closed.Load() {
		return errors.New("participant closed")
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_ = p.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return p.ws.WriteJSON(message)
}

func (p *Participant) addOutbound(ownerID string, track *webrtc.TrackLocalStaticRTP) {
	if p.closed.Load() || ownerID == p.id {
		return
	}
	p.stateMu.Lock()
	if _, exists := p.senders[ownerID]; exists {
		p.stateMu.Unlock()
		return
	}
	sender, err := p.pc.AddTrack(track)
	if err == nil {
		p.senders[ownerID] = sender
	}
	p.stateMu.Unlock()
	if err != nil {
		p.logger.Debug("could not add outbound track", "user", p.id, "owner", ownerID, "error", err)
		return
	}
	go drainRTCP(sender)
	p.requestNegotiation()
}

func (p *Participant) removeOutbound(ownerID string) {
	p.stateMu.Lock()
	sender := p.senders[ownerID]
	delete(p.senders, ownerID)
	p.stateMu.Unlock()
	if sender != nil && !p.closed.Load() {
		if err := p.pc.RemoveTrack(sender); err == nil {
			p.requestNegotiation()
		}
	}
}

func drainRTCP(sender *webrtc.RTPSender) {
	buffer := make([]byte, 1500)
	for {
		if _, _, err := sender.Read(buffer); err != nil {
			return
		}
	}
}

func (p *Participant) requestNegotiation() {
	p.stateMu.Lock()
	if p.closed.Load() {
		p.stateMu.Unlock()
		return
	}
	if p.inOffer || p.pc.SignalingState() != webrtc.SignalingStateStable {
		p.pending = true
		p.stateMu.Unlock()
		return
	}
	p.inOffer = true
	p.stateMu.Unlock()

	go func() {
		offer, err := p.pc.CreateOffer(nil)
		if err == nil {
			err = p.pc.SetLocalDescription(offer)
		}
		if err == nil {
			err = p.send(serverMessage{Type: "offer", SDP: &offer})
		}
		if err != nil {
			p.logger.Warn("voice negotiation failed", "room", p.room.id, "user", p.id, "error", err)
			p.Close()
		}
	}()
}

func (p *Participant) acceptAnswer(answer webrtc.SessionDescription) error {
	if answer.Type != webrtc.SDPTypeAnswer {
		return errors.New("expected SDP answer")
	}
	if err := p.pc.SetRemoteDescription(answer); err != nil {
		return err
	}
	p.stateMu.Lock()
	p.inOffer = false
	pending := p.pending
	p.pending = false
	p.stateMu.Unlock()
	if pending {
		p.requestNegotiation()
	}
	return nil
}

func (p *Participant) Close() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	if p.onClose != nil {
		p.onClose()
	}
	_ = p.pc.Close()
	p.writeMu.Lock()
	_ = p.ws.Close()
	p.writeMu.Unlock()
}
