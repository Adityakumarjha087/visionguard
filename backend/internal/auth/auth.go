// Package auth issues and verifies JWT access tokens and opaque rotating refresh tokens.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"visionguard/backend/internal/domain"
)

// ErrInvalidToken is returned for any unparsable, expired or tampered token.
var ErrInvalidToken = errors.New("invalid token")

// Claims are the access-token claims.
type Claims struct {
	Role  domain.Role `json:"role"`
	Email string      `json:"email"`
	Type  string      `json:"typ"`
	jwt.RegisteredClaims
}

// Issuer signs and verifies tokens.
type Issuer struct {
	secret     []byte
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

// NewIssuer builds an Issuer.
func NewIssuer(secret, issuer string, accessTTL, refreshTTL time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), issuer: issuer, accessTTL: accessTTL, refreshTTL: refreshTTL, now: time.Now}
}

// AccessTTL returns the access-token lifetime.
func (i *Issuer) AccessTTL() time.Duration { return i.accessTTL }

// RefreshTTL returns the refresh-token lifetime.
func (i *Issuer) RefreshTTL() time.Duration { return i.refreshTTL }

// AccessToken signs an HS256 JWT for the user.
func (i *Issuer) AccessToken(u domain.User) (string, error) {
	now := i.now()
	claims := Claims{
		Role:  u.Role,
		Email: u.Email,
		Type:  "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.ID,
			Issuer:    i.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(now.Add(i.accessTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
}

// Parse verifies signature, algorithm, issuer and expiry.
func (i *Issuer) Parse(token string) (*Claims, error) {
	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return i.secret, nil
	}, jwt.WithIssuer(i.issuer), jwt.WithValidMethods([]string{"HS256"}), jwt.WithTimeFunc(i.now))
	if err != nil || !parsed.Valid || claims.Type != "access" || !claims.Role.Valid() {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// NewRefreshToken returns a random opaque token and its SHA-256 hash (only the hash is stored).
func NewRefreshToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken hashes a refresh token for storage/lookup.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// HashPassword bcrypt-hashes a password.
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword compares in constant time.
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}
