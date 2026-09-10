// Package ticket verifies the short-lived room credentials issued by the API.
package ticket

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	issuer   = "ppg-api"
	audience = "ppg-voice"
)

// Claims is the authenticated identity and room scope carried by a ticket.
type Claims struct {
	Room string `json:"room"`
	Name string `json:"name"`
	jwt.RegisteredClaims
}

// Verifier accepts only HS256 tickets created by the application API.
type Verifier struct{ secret []byte }

func NewVerifier(secret string) *Verifier { return &Verifier{secret: []byte(secret)} }

func (v *Verifier) Verify(raw string) (Claims, error) {
	claims := Claims{}
	token, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		return v.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithLeeway(5*time.Second),
	)
	if err != nil || !token.Valid {
		return Claims{}, errors.New("invalid voice ticket")
	}
	if claims.Subject == "" || claims.Room == "" || claims.Name == "" {
		return Claims{}, errors.New("voice ticket has incomplete claims")
	}
	return claims, nil
}
