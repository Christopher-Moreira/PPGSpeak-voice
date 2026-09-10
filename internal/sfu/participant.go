package sfu

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

type Participant struct {
	id     string
	name   string
	room   *Room
	pc     *webrtc.PeerConnection
	ws     *websocket.Conn
	logger *slog.Logger

	writeMu    sync.Mutex
	stateMu    sync.Mutex
	senders    map[string]*webrtc.RTPSender
	inbound    map[MediaSource]*publishedTrack
	enabled    map[MediaSource]bool
	midSources map[string]MediaSource
	sourceMIDs map[MediaSource]string
	pending    bool
	inOffer    bool
	closed     atomic.Bool
	onClose    func()
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

func (p *Participant) addOutbound(published *publishedTrack) {
	if p.closed.Load() || published.owner.id == p.id {
		return
	}
	key := published.key()
	p.stateMu.Lock()
	if _, exists := p.senders[key]; exists {
		p.stateMu.Unlock()
		return
	}
	sender, err := p.pc.AddTrack(published.track)
	if err == nil {
		p.senders[key] = sender
	}
	p.stateMu.Unlock()
	if err != nil {
		p.logger.Debug("could not add outbound track", "user", p.id, "owner", published.owner.id, "source", published.source, "error", err)
		return
	}
	go drainRTCP(sender, published)
	p.requestNegotiation()
}

func (p *Participant) removeOutbound(key string) {
	p.stateMu.Lock()
	sender := p.senders[key]
	delete(p.senders, key)
	p.stateMu.Unlock()
	if sender != nil && !p.closed.Load() {
		if err := p.pc.RemoveTrack(sender); err == nil {
			p.requestNegotiation()
		}
	}
}

func drainRTCP(sender *webrtc.RTPSender, published *publishedTrack) {
	for {
		packets, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		for _, packet := range packets {
			switch packet.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				_ = published.owner.pc.WriteRTCP([]rtcp.Packet{
					&rtcp.PictureLossIndication{MediaSSRC: published.ssrc},
				})
			}
		}
	}
}

func (p *Participant) setSourceEnabled(source MediaSource, enabled bool, mid string) {
	p.stateMu.Lock()
	if mid != "" {
		if previous := p.sourceMIDs[source]; previous != "" && previous != mid {
			delete(p.midSources, previous)
		}
		p.sourceMIDs[source] = mid
		p.midSources[mid] = source
	}
	wasEnabled := p.enabled[source]
	p.enabled[source] = enabled
	track := p.inbound[source]
	p.stateMu.Unlock()
	if enabled && !wasEnabled && track != nil {
		p.room.publish(p, source, track.track, track.ssrc)
	} else if !enabled && wasEnabled {
		p.room.unpublish(p, source)
	}
}

func (p *Participant) sourceEnabled(source MediaSource) bool {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.enabled[source]
}

func (p *Participant) setInbound(source MediaSource, track *webrtc.TrackLocalStaticRTP, ssrc uint32) {
	p.stateMu.Lock()
	p.inbound[source] = &publishedTrack{owner: p, source: source, track: track, ssrc: ssrc}
	p.stateMu.Unlock()
}

func (p *Participant) sourceFor(receiver *webrtc.RTPReceiver) (MediaSource, string, bool) {
	var mid string
	for _, transceiver := range p.pc.GetTransceivers() {
		if transceiver.Receiver() == receiver {
			mid = transceiver.Mid()
			break
		}
	}
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	source, ok := p.midSources[mid]
	return source, mid, ok
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
