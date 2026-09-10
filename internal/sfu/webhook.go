package sfu

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type webhookEvent struct {
	Type     string `json:"type"`
	Room     string `json:"room"`
	Identity string `json:"identity"`
	Name     string `json:"name"`
	TS       string `json:"ts"`
}

type webhookClient struct {
	url    string
	secret []byte
	client *http.Client
	logger *slog.Logger
}

func newWebhookClient(url, secret string, logger *slog.Logger) *webhookClient {
	return &webhookClient{
		url: url, secret: []byte(secret), logger: logger,
		client: &http.Client{Timeout: 3 * time.Second},
	}
}

func (c *webhookClient) send(ctx context.Context, eventType string, p *Participant) {
	if c.url == "" {
		return
	}
	body, err := json.Marshal(webhookEvent{
		Type: eventType, Room: p.room.id, Identity: p.id, Name: p.name,
		TS: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	for attempt := 1; attempt <= 3; attempt++ {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
		if reqErr != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-PPG-Voice-Signature", signature)
		resp, requestErr := c.client.Do(req)
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return
			}
			requestErr = fmt.Errorf("unexpected status %s", resp.Status)
		}
		if attempt == 3 {
			c.logger.Warn("voice presence webhook failed", "type", eventType, "room", p.room.id, "user", p.id, "error", requestErr)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(attempt) * 150 * time.Millisecond):
		}
	}
}
