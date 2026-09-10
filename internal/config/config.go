// Package config is the only package that reads process environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
)

// Config contains the runtime settings for the standalone voice SFU.
type Config struct {
	HTTPAddr       string
	TokenSecret    string
	WebhookURL     string
	WebhookSecret  string
	AllowedOrigins []string
	PublicIP       string
	UDPPortMin     uint16
	UDPPortMax     uint16
	ICEServers     []webrtc.ICEServer
	JoinTimeout    time.Duration
}

// Load reads and validates configuration.
func Load() (Config, error) {
	minPort, err := uint16Env("VOICE_UDP_PORT_MIN", 50000)
	if err != nil {
		return Config{}, err
	}
	maxPort, err := uint16Env("VOICE_UDP_PORT_MAX", 50100)
	if err != nil {
		return Config{}, err
	}
	c := Config{
		HTTPAddr:       env("VOICE_HTTP_ADDR", ":8081"),
		TokenSecret:    os.Getenv("VOICE_TOKEN_SECRET"),
		WebhookURL:     os.Getenv("VOICE_WEBHOOK_URL"),
		WebhookSecret:  os.Getenv("VOICE_WEBHOOK_SECRET"),
		AllowedOrigins: splitCSV(env("VOICE_ALLOWED_ORIGINS", "http://localhost,http://localhost:3000")),
		PublicIP:       os.Getenv("VOICE_PUBLIC_IP"),
		UDPPortMin:     minPort,
		UDPPortMax:     maxPort,
		JoinTimeout:    durationEnv("VOICE_JOIN_TIMEOUT", 10*time.Second),
	}
	if len(c.TokenSecret) < 32 {
		return Config{}, fmt.Errorf("config: VOICE_TOKEN_SECRET must contain at least 32 bytes")
	}
	if c.WebhookURL != "" && len(c.WebhookSecret) < 32 {
		return Config{}, fmt.Errorf("config: VOICE_WEBHOOK_SECRET must contain at least 32 bytes when VOICE_WEBHOOK_URL is set")
	}
	if c.UDPPortMin == 0 || c.UDPPortMax < c.UDPPortMin {
		return Config{}, fmt.Errorf("config: invalid UDP port range %d-%d", c.UDPPortMin, c.UDPPortMax)
	}

	stunURL := os.Getenv("VOICE_STUN_URL")
	turnURL := os.Getenv("VOICE_TURN_URL")
	if stunURL != "" {
		c.ICEServers = append(c.ICEServers, webrtc.ICEServer{URLs: []string{stunURL}})
	}
	if turnURL != "" {
		c.ICEServers = append(c.ICEServers, webrtc.ICEServer{
			URLs:       []string{turnURL},
			Username:   os.Getenv("VOICE_TURN_USERNAME"),
			Credential: os.Getenv("VOICE_TURN_CREDENTIAL"),
		})
	}
	return c, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func uint16Env(key string, fallback uint16) (uint16, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a valid UDP port: %w", key, err)
	}
	return uint16(n), nil
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return d
}
