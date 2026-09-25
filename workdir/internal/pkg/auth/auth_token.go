package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// tokenIssuer is the "iss" claim of every token issued by this service.
const tokenIssuer = "gogoform"

// tokenHeader is the encoded JOSE header of every token: HMAC SHA-256.
var tokenHeader = b64.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

// b64 is the unpadded base64url encoding JWTs use.
var b64 = base64.RawURLEncoding

var errInvalidToken = errors.New("invalid token")

// claims is the payload of a token. The role is informative only: requests
// are authorized with the role currently stored for the user.
type claims struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub"`
	Username  string `json:"username"`
	Role      Role   `json:"role"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

// tokenSigner issues and verifies HS256 JSON Web Tokens (RFC 7519).
type tokenSigner struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// sign returns a token for u, valid for s.ttl.
func (s tokenSigner) sign(u User) (string, time.Time, error) {
	now := s.now()
	exp := now.Add(s.ttl)
	payload, err := json.Marshal(claims{
		Issuer:    tokenIssuer,
		Subject:   strconv.Itoa(int(u.ID)),
		Username:  u.Username,
		Role:      u.Role,
		IssuedAt:  now.Unix(),
		ExpiresAt: exp.Unix(),
	})
	if err != nil {
		return "", time.Time{}, err
	}
	unsigned := tokenHeader + "." + b64.EncodeToString(payload)
	return unsigned + "." + b64.EncodeToString(s.mac(unsigned)), exp, nil
}

// verify checks the signature, issuer and expiry of token and returns the ID
// of the user it was issued to.
func (s tokenSigner) verify(token string) (int32, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0, errInvalidToken
	}
	// Only the exact header issued by sign is accepted, which rules out
	// algorithm confusion ("alg": "none", RS256 with the secret as key...).
	if parts[0] != tokenHeader {
		return 0, errInvalidToken
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, s.mac(parts[0]+"."+parts[1])) {
		return 0, errInvalidToken
	}
	payload, err := b64.DecodeString(parts[1])
	if err != nil {
		return 0, errInvalidToken
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return 0, errInvalidToken
	}
	if c.Issuer != tokenIssuer || s.now().Unix() >= c.ExpiresAt {
		return 0, errInvalidToken
	}
	id, err := strconv.ParseInt(c.Subject, 10, 32)
	if err != nil || id < 1 {
		return 0, errInvalidToken
	}
	return int32(id), nil
}

func (s tokenSigner) mac(unsigned string) []byte {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(unsigned))
	return m.Sum(nil)
}
