package ticket

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerify(t *testing.T) {
	secret := strings.Repeat("s", 40)
	claims := Claims{
		Room: "room-1",
		Name: "Ada",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: issuer, Subject: "user-1", Audience: jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewVerifier(secret).Verify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Room != claims.Room || got.Subject != claims.Subject || got.Name != claims.Name {
		t.Fatalf("unexpected claims: %#v", got)
	}
}

func TestRejectsWrongSecret(t *testing.T) {
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		Room: "room-1", Name: "Ada",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: issuer, Subject: "user-1", Audience: jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}).SignedString([]byte(strings.Repeat("a", 40)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewVerifier(strings.Repeat("b", 40)).Verify(raw); err == nil {
		t.Fatal("ticket signed with another secret should be rejected")
	}
}
