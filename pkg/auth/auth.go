package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const (
	// HeaderTimestamp is the HTTP header for timestamp
	HeaderTimestamp = "X-Orchestrator-Timestamp"
	// HeaderSignature is the HTTP header for HMAC signature
	HeaderSignature = "X-Orchestrator-Signature"
	// MaxClockSkew is the maximum allowed time difference
	MaxClockSkew = 30 * time.Second
)

// Authenticator handles request authentication
type Authenticator struct {
	sharedSecret string
}

// New creates a new authenticator with the given shared secret
func New(sharedSecret string) *Authenticator {
	return &Authenticator{
		sharedSecret: sharedSecret,
	}
}

// SignRequest adds authentication headers to an HTTP request
func (a *Authenticator) SignRequest(req *http.Request) error {
	if a.sharedSecret == "" {
		return nil // No authentication required
	}

	timestamp := time.Now().Unix()
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(timestamp, 10))

	signature := a.generateSignature(req.Method, req.URL.Path, timestamp)
	req.Header.Set(HeaderSignature, signature)

	return nil
}

// ValidateRequest validates the authentication headers on an HTTP request
func (a *Authenticator) ValidateRequest(req *http.Request) error {
	if a.sharedSecret == "" {
		return nil // No authentication required
	}

	// Get timestamp
	timestampStr := req.Header.Get(HeaderTimestamp)
	if timestampStr == "" {
		return fmt.Errorf("missing timestamp header")
	}

	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}

	// Check clock skew
	now := time.Now().Unix()
	diff := now - timestamp
	if diff < 0 {
		diff = -diff
	}
	if time.Duration(diff)*time.Second > MaxClockSkew {
		return fmt.Errorf("timestamp outside allowed window (skew: %ds)", diff)
	}

	// Validate signature
	expectedSig := a.generateSignature(req.Method, req.URL.Path, timestamp)
	actualSig := req.Header.Get(HeaderSignature)

	if !hmac.Equal([]byte(expectedSig), []byte(actualSig)) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

// Middleware returns an HTTP middleware that validates authentication
func (a *Authenticator) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.ValidateRequest(r); err != nil {
			http.Error(w, fmt.Sprintf("Authentication failed: %v", err), http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// generateSignature creates an HMAC-SHA256 signature
func (a *Authenticator) generateSignature(method, path string, timestamp int64) string {
	message := fmt.Sprintf("%s:%s:%d", method, path, timestamp)
	mac := hmac.New(sha256.New, []byte(a.sharedSecret))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

