package sfu

import "github.com/pion/webrtc/v4"

type clientMessage struct {
	Type      string                     `json:"type"`
	Token     string                     `json:"token,omitempty"`
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
	Source    MediaSource                `json:"source,omitempty"`
	Enabled   *bool                      `json:"enabled,omitempty"`
	MID       string                     `json:"mid,omitempty"`
}

type serverMessage struct {
	Type         string                     `json:"type"`
	SDP          *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate    *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
	Participant  *ParticipantInfo           `json:"participant,omitempty"`
	Participants []ParticipantInfo          `json:"participants,omitempty"`
	Track        *TrackInfo                 `json:"track,omitempty"`
	Tracks       []TrackInfo                `json:"tracks,omitempty"`
	ICEServers   []iceServerJSON            `json:"iceServers,omitempty"`
	Code         string                     `json:"code,omitempty"`
	Message      string                     `json:"message,omitempty"`
}

type ParticipantInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type MediaSource string

const (
	MediaSourceMicrophone  MediaSource = "microphone"
	MediaSourceCamera      MediaSource = "camera"
	MediaSourceScreen      MediaSource = "screen"
	MediaSourceScreenAudio MediaSource = "screen_audio"
)

func (s MediaSource) valid() bool {
	switch s {
	case MediaSourceMicrophone, MediaSourceCamera, MediaSourceScreen, MediaSourceScreenAudio:
		return true
	default:
		return false
	}
}

func (s MediaSource) kind() webrtc.RTPCodecType {
	if s == MediaSourceCamera || s == MediaSourceScreen {
		return webrtc.RTPCodecTypeVideo
	}
	return webrtc.RTPCodecTypeAudio
}

type TrackInfo struct {
	ID            string      `json:"id"`
	ParticipantID string      `json:"participantId"`
	Source        MediaSource `json:"source"`
	Kind          string      `json:"kind"`
}

type iceServerJSON struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential any      `json:"credential,omitempty"`
}
