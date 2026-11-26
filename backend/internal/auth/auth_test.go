package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"visionguard/backend/internal/domain"
)

func TestAccessTokenRoundTrip(t *testing.T) {
	iss := NewIssuer("0123456789abcdef0123", "visionguard", time.Minute, time.Hour)
	tok, err := iss.AccessToken(domain.User{ID: "u1", Email: "a@b.c", Role: domain.RoleOperator})
	require.NoError(t, err)
	claims, err := iss.Parse(tok)
	require.NoError(t, err)
	assert.Equal(t, "u1", claims.Subject)
	assert.Equal(t, domain.RoleOperator, claims.Role)
	assert.Equal(t, time.Minute, iss.AccessTTL())
	assert.Equal(t, time.Hour, iss.RefreshTTL())
}

func TestParseRejectsTamperedExpiredAndForeignTokens(t *testing.T) {
	iss := NewIssuer("0123456789abcdef0123", "visionguard", time.Minute, time.Hour)
	tok, _ := iss.AccessToken(domain.User{ID: "u1", Role: domain.RoleAdmin})
	_, err := iss.Parse(tok + "x")
	assert.ErrorIs(t, err, ErrInvalidToken)

	other := NewIssuer("another-secret-0123456", "visionguard", time.Minute, time.Hour)
	_, err = other.Parse(tok)
	assert.ErrorIs(t, err, ErrInvalidToken)

	iss.now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
	old, _ := iss.AccessToken(domain.User{ID: "u1", Role: domain.RoleAdmin})
	iss.now = time.Now
	_, err = iss.Parse(old)
	assert.ErrorIs(t, err, ErrInvalidToken)

	// alg=none and wrong typ are refused.
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{Role: domain.RoleAdmin, Type: "access"}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	_, err = iss.Parse(none)
	assert.ErrorIs(t, err, ErrInvalidToken)
	refreshTyp, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{Role: domain.RoleAdmin, Type: "refresh",
		RegisteredClaims: jwt.RegisteredClaims{Issuer: "visionguard", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}).
		SignedString([]byte("0123456789abcdef0123"))
	_, err = iss.Parse(refreshTyp)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestPasswordsAndRefreshTokens(t *testing.T) {
	h, err := HashPassword("s3cret-pass")
	require.NoError(t, err)
	assert.True(t, CheckPassword(h, "s3cret-pass"))
	assert.False(t, CheckPassword(h, "nope"))
	tok, hash, err := NewRefreshToken()
	require.NoError(t, err)
	assert.Len(t, tok, 43)
	assert.Equal(t, hash, HashToken(tok))
	tok2, _, _ := NewRefreshToken()
	assert.NotEqual(t, tok, tok2)
}

func TestRoles(t *testing.T) {
	assert.True(t, domain.RoleAdmin.AtLeast(domain.RoleOperator))
	assert.False(t, domain.RoleViewer.AtLeast(domain.RoleOperator))
	assert.False(t, domain.Role("root").Valid())
}
