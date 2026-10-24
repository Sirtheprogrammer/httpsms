package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/httpsms/gateway-server/internal/domain"
	"github.com/httpsms/gateway-server/internal/storage"
)

func TestWebhookDeliveryWithHMAC(t *testing.T) {
	secretKey := "test_secret_key_12345"

	receivedPayload := make(chan []byte, 1)
	receivedSignature := make(chan string, 1)

	// Mock webhook receiving server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig := r.Header.Get("X-Signature-SHA256")
		body, _ := io.ReadAll(r.Body)

		receivedSignature <- sig
		receivedPayload <- body

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"received":true}`))
	}))
	defer mockServer.Close()

	tmpDir, _ := os.MkdirTemp("", "webhook_test_*")
	defer os.RemoveAll(tmpDir)

	db, err := storage.NewDB(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}
	defer db.Close()

	dispatcher := NewDispatcher(db)
	dispatcher.Start(2)
	defer dispatcher.Stop()

	wh := domain.Webhook{
		ID:        uuid.New().String(),
		URL:       mockServer.URL,
		Secret:    secretKey,
		Events:    []string{domain.EventSMSReceived},
		IsActive:  true,
		CreatedAt: time.Now().UTC(),
	}
	if err := db.CreateWebhook(&wh); err != nil {
		t.Fatalf("Failed to create webhook: %v", err)
	}

	// Dispatch an event
	msgID := "msg_test_999"
	err = dispatcher.Dispatch(domain.EventSMSReceived, &msgID, map[string]string{
		"sender": "+1987654321",
		"body":   "Hello Webhook Test",
	})
	if err != nil {
		t.Fatalf("Failed to dispatch event: %v", err)
	}

	select {
	case payload := <-receivedPayload:
		sig := <-receivedSignature

		// Verify HMAC
		mac := hmac.New(sha256.New, []byte(secretKey))
		mac.Write(payload)
		expectedSig := hex.EncodeToString(mac.Sum(nil))

		if sig != expectedSig {
			t.Errorf("Signature mismatch! Got %s, expected %s", sig, expectedSig)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for webhook delivery")
	}
}

