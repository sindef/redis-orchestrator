package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthenticator(t *testing.T) {
	secret := "test-secret-key-123"
	auth := New(secret)

	t.Run("successful authentication", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/state", nil)
		
		err := auth.SignRequest(req)
		if err != nil {
			t.Fatalf("Failed to sign request: %v", err)
		}

		err = auth.ValidateRequest(req)
		if err != nil {
			t.Errorf("Failed to validate request: %v", err)
		}
	})

	t.Run("missing timestamp", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/state", nil)
		req.Header.Set(HeaderSignature, "somesignature")

		err := auth.ValidateRequest(req)
		if err == nil {
			t.Error("Expected error for missing timestamp")
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/state", nil)
		
		err := auth.SignRequest(req)
		if err != nil {
			t.Fatalf("Failed to sign request: %v", err)
		}

		req.Header.Set(HeaderSignature, "invalid")

		err = auth.ValidateRequest(req)
		if err == nil {
			t.Error("Expected error for invalid signature")
		}
	})

	t.Run("no authentication required", func(t *testing.T) {
		noAuth := New("")
		req := httptest.NewRequest("GET", "/state", nil)

		err := noAuth.SignRequest(req)
		if err != nil {
			t.Errorf("Sign should succeed with no auth: %v", err)
		}

		err = noAuth.ValidateRequest(req)
		if err != nil {
			t.Errorf("Validate should succeed with no auth: %v", err)
		}
	})
}

func TestAuthMiddleware(t *testing.T) {
	secret := "test-secret-key-123"
	auth := New(secret)

	handler := auth.Middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	t.Run("authenticated request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/state", nil)
		auth.SignRequest(req)

		rr := httptest.NewRecorder()
		handler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rr.Code)
		}
	})

	t.Run("unauthenticated request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/state", nil)
		rr := httptest.NewRecorder()
		handler(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", rr.Code)
		}
	})
}

func TestClockSkew(t *testing.T) {
	secret := "test-secret-key-123"
	auth := New(secret)

	req := httptest.NewRequest("GET", "/state", nil)
	
	oldTimestamp := time.Now().Add(-60 * time.Second).Unix()
	req.Header.Set(HeaderTimestamp, string(rune(oldTimestamp)))
	req.Header.Set(HeaderSignature, auth.generateSignature("GET", "/state", oldTimestamp))

	err := auth.ValidateRequest(req)
	if err == nil {
		t.Error("Expected error for excessive clock skew")
	}
}

