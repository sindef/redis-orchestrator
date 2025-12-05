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
	HeaderTimestamp = "X-Orchestrator-Timestamp"
	HeaderSignature = "X-Orchestrator-Signature"
	// MaxClockSkew prevents replay attacks by rejecting requests with timestamps
	// too far from the current time. This window accounts for reasonable clock
	// differences between client and server.
	MaxClockSkew = 30 * time.Second
)

type Authenticator struct {
	sharedSecret string
}

func New(sharedSecret string) *Authenticator {
	return &Authenticator{
		sharedSecret: sharedSecret,
	}
}

func (a *Authenticator) SignRequest(req *http.Request) error {
	// Allow requests to proceed without signing if no secret is configured.
	// This enables graceful degradation when auth is disabled.
	if a.sharedSecret == "" {
		return nil
	}

	timestamp := time.Now().Unix()
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(timestamp, 10))

	signature := a.generateSignature(req.Method, req.URL.Path, timestamp)
	req.Header.Set(HeaderSignature, signature)

	return nil
}

func (a *Authenticator) ValidateRequest(req *http.Request) error {
	// Skip validation if no secret is configured, allowing the system to
	// operate without authentication when needed.
	if a.sharedSecret == "" {
		return nil
	}

	timestampStr := req.Header.Get(HeaderTimestamp)
	if timestampStr == "" {
		return fmt.Errorf("missing timestamp header")
	}

	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}

	// Check timestamp to prevent replay attacks. The absolute difference
	// handles both future and past timestamps (e.g., from clock drift).
	now := time.Now().Unix()
	diff := now - timestamp
	if diff < 0 {
		diff = -diff
	}
	if time.Duration(diff)*time.Second > MaxClockSkew {
		return fmt.Errorf("timestamp outside allowed window (skew: %ds)", diff)
	}

	expectedSig := a.generateSignature(req.Method, req.URL.Path, timestamp)
	actualSig := req.Header.Get(HeaderSignature)

	// Use hmac.Equal instead of == to prevent timing attacks that could
	// leak information about the expected signature.
	if !hmac.Equal([]byte(expectedSig), []byte(actualSig)) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

func (a *Authenticator) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.ValidateRequest(r); err != nil {
			http.Error(w, fmt.Sprintf("Authentication failed: %v", err), http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// generateSignature creates an HMAC-SHA256 signature over the request method,
// path, and timestamp. The message format "method:path:timestamp" must remain
// consistent for signature validation to work correctly.
func (a *Authenticator) generateSignature(method, path string, timestamp int64) string {
	message := fmt.Sprintf("%s:%s:%d", method, path, timestamp)
	mac := hmac.New(sha256.New, []byte(a.sharedSecret))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}
