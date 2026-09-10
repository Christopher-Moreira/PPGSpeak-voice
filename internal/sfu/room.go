package sfu

import (
	"sync"

	"github.com/pion/webrtc/v4"
)

type publishedTrack struct {
	owner  *Participant
	source MediaSource
	track  *webrtc.TrackLocalStaticRTP
	ssrc   uint32
}

func trackKey(ownerID string, source MediaSource) string { return ownerID + "\x00" + string(source) }
func (t *publishedTrack) key() string                    { return trackKey(t.owner.id, t.source) }
func (t *publishedTrack) info() TrackInfo {
	return TrackInfo{ID: t.track.ID(), ParticipantID: t.owner.id, Source: t.source, Kind: t.track.Kind().String()}
}

type Room struct {
	id           string
	manager      *Manager
	mu           sync.RWMutex
	participants map[string]*Participant
	tracks       map[string]*publishedTrack
}

func newRoom(id string, manager *Manager) *Room {
	return &Room{
		id: id, manager: manager,
		participants: make(map[string]*Participant),
		tracks:       make(map[string]*publishedTrack),
	}
}

func (r *Room) join(p *Participant) (participants []ParticipantInfo, tracks []*publishedTrack, replaced *Participant) {
	r.mu.Lock()
	replaced = r.participants[p.id]
	var replacedKeys []string
	if replaced != nil {
		for key, track := range r.tracks {
			if track.owner == replaced {
				delete(r.tracks, key)
				replacedKeys = append(replacedKeys, key)
			}
		}
	}
	for _, other := range r.participants {
		if other != replaced {
			participants = append(participants, ParticipantInfo{ID: other.id, Name: other.name})
		}
	}
	for _, track := range r.tracks {
		if track.owner != replaced {
			tracks = append(tracks, track)
		}
	}
	r.participants[p.id] = p
	peers := make([]*Participant, 0, len(r.participants))
	for _, peer := range r.participants {
		if peer != p && peer != replaced {
			peers = append(peers, peer)
		}
	}
	r.mu.Unlock()
	for _, peer := range peers {
		for _, key := range replacedKeys {
			peer.removeOutbound(key)
		}
	}
	return participants, tracks, replaced
}

func (r *Room) publish(owner *Participant, source MediaSource, track *webrtc.TrackLocalStaticRTP, ssrc uint32) {
	key := trackKey(owner.id, source)
	r.mu.Lock()
	previous := r.tracks[key]
	published := &publishedTrack{owner: owner, source: source, track: track, ssrc: ssrc}
	r.tracks[key] = published
	peers := make([]*Participant, 0, len(r.participants))
	for _, peer := range r.participants {
		if peer != owner {
			peers = append(peers, peer)
		}
	}
	r.mu.Unlock()

	if previous != nil {
		for _, peer := range peers {
			peer.removeOutbound(key)
		}
	}
	for _, peer := range peers {
		info := published.info()
		_ = peer.send(serverMessage{Type: "track_published", Track: &info})
		peer.addOutbound(published)
	}
}

func (r *Room) unpublish(owner *Participant, source MediaSource) {
	key := trackKey(owner.id, source)
	r.mu.Lock()
	current := r.tracks[key]
	if current == nil || current.owner != owner {
		r.mu.Unlock()
		return
	}
	delete(r.tracks, key)
	peers := make([]*Participant, 0, len(r.participants))
	for _, peer := range r.participants {
		if peer != owner {
			peers = append(peers, peer)
		}
	}
	r.mu.Unlock()
	for _, peer := range peers {
		info := current.info()
		_ = peer.send(serverMessage{Type: "track_unpublished", Track: &info})
		peer.removeOutbound(key)
	}
}

func (r *Room) leave(p *Participant) bool {
	r.mu.Lock()
	if r.participants[p.id] != p {
		r.mu.Unlock()
		return false
	}
	delete(r.participants, p.id)
	var removed []*publishedTrack
	for key, current := range r.tracks {
		if current.owner == p {
			delete(r.tracks, key)
			removed = append(removed, current)
		}
	}
	peers := make([]*Participant, 0, len(r.participants))
	for _, peer := range r.participants {
		peers = append(peers, peer)
	}
	empty := len(r.participants) == 0
	r.mu.Unlock()

	for _, peer := range peers {
		for _, track := range removed {
			peer.removeOutbound(track.key())
		}
		_ = peer.send(serverMessage{Type: "participant_left", Participant: &ParticipantInfo{ID: p.id, Name: p.name}})
	}
	if empty {
		r.manager.removeIfEmpty(r)
	}
	return true
}

func (r *Room) broadcastJoined(p *Participant) {
	r.mu.RLock()
	peers := make([]*Participant, 0, len(r.participants))
	for _, peer := range r.participants {
		if peer != p {
			peers = append(peers, peer)
		}
	}
	r.mu.RUnlock()
	for _, peer := range peers {
		_ = peer.send(serverMessage{Type: "participant_joined", Participant: &ParticipantInfo{ID: p.id, Name: p.name}})
	}
}

func (r *Room) counts() (participants int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.participants)
}

type Manager struct {
	mu    sync.RWMutex
	rooms map[string]*Room
}

func NewManager() *Manager { return &Manager{rooms: make(map[string]*Room)} }

func (m *Manager) room(id string) *Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	if room := m.rooms[id]; room != nil {
		return room
	}
	room := newRoom(id, m)
	m.rooms[id] = room
	return room
}

func (m *Manager) removeIfEmpty(room *Room) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rooms[room.id] == room && room.counts() == 0 {
		delete(m.rooms, room.id)
	}
}

func (m *Manager) Counts() (rooms, participants int) {
	m.mu.RLock()
	list := make([]*Room, 0, len(m.rooms))
	for _, room := range m.rooms {
		list = append(list, room)
	}
	m.mu.RUnlock()
	for _, room := range list {
		participants += room.counts()
	}
	return len(list), participants
}
