package sfu

import "github.com/pion/webrtc/v4"

type clientMessage struct {
	Type      string                     `json:"type"`
	Token     string                     `json:"token,omitempty"`
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
}

type serverMessage struct {
	Type         string                     `json:"type"`
	SDP          *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate    *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
	Participant  *ParticipantInfo           `json:"participant,omitempty"`
	Participants []ParticipantInfo          `json:"participants,omitempty"`
	ICEServers   []iceServerJSON            `json:"iceServers,omitempty"`
	Code         string                     `json:"code,omitempty"`
	Message      string                     `json:"message,omitempty"`
}

type ParticipantInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type iceServerJSON struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential any      `json:"credential,omitempty"`
}
