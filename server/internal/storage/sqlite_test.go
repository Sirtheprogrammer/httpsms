package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/httpsms/gateway-server/internal/domain"
)

func TestMessageDeduplication(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "httpsms_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}
	defer db.Close()

	idempotencyKey := "test_fingerprint_sha256_abcdef123456"

	msg1 := domain.Message{
		ID:             uuid.New().String(),
		Direction:      domain.DirectionInbound,
		DeviceID:       "dev_123",
		Sender:         "+1234567890",
		Recipient:      "local_sim",
		Body:           "Verification code: 849201",
		Status:         domain.MessageStatusReceived,
		IdempotencyKey: idempotencyKey,
		PartsCount:     1,
		CreatedAt:      time.Now().UTC(),
	}

	// First insert must be unique (isNew == true)
	isNew1, err := db.SaveMessage(&msg1)
	if err != nil {
		t.Fatalf("Failed to save msg1: %v", err)
	}
	if !isNew1 {
		t.Errorf("Expected msg1 to be new, got duplicate")
	}

	// Second insert with the identical idempotency key (simulating carrier duplicate or network retry)
	msg2 := domain.Message{
		ID:             uuid.New().String(),
		Direction:      domain.DirectionInbound,
		DeviceID:       "dev_123",
		Sender:         "+1234567890",
		Recipient:      "local_sim",
		Body:           "Verification code: 849201",
		Status:         domain.MessageStatusReceived,
		IdempotencyKey: idempotencyKey,
		PartsCount:     1,
		CreatedAt:      time.Now().UTC(),
	}

	isNew2, err := db.SaveMessage(&msg2)
	if err != nil {
		t.Fatalf("Failed to handle duplicate msg2: %v", err)
	}
	if isNew2 {
		t.Errorf("Expected msg2 to be detected as DUPLICATE, but marked as new!")
	}

	// Verify only 1 message exists in total
	messages, total, err := db.ListMessages("", "", 10, 0)
	if err != nil {
		t.Fatalf("Failed to list messages: %v", err)
	}
	if total != 1 || len(messages) != 1 {
		t.Errorf("Expected exactly 1 message in database, found %d", total)
	}
	if messages[0].ID != msg1.ID {
		t.Errorf("Expected ID %s, got %s", msg1.ID, messages[0].ID)
	}
}

func TestDeviceAndSimPersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "httpsms_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}
	defer db.Close()

	dev := domain.Device{
		ID:             "dev_test_1",
		Name:           "Samsung Galaxy S23",
		TokenHash:      "hash_123",
		Status:         "online",
		BatteryLevel:   88,
		IsCharging:     true,
		AppVersion:     "1.0.0",
		AppVersionCode: 1,
		LastSeenAt:     time.Now().UTC(),
		CreatedAt:      time.Now().UTC(),
	}

	if err := db.UpsertDevice(&dev); err != nil {
		t.Fatalf("Failed to upsert device: %v", err)
	}

	sims := []domain.SimCard{
		{
			ID:          "sim_1",
			DeviceID:    dev.ID,
			SlotIndex:   0,
			SubID:       1,
			CarrierName: "Verizon",
			PhoneNumber: "+15550100",
			CountryISO:  "US",
			SignalLevel: 4,
			SignalDbm:   -75,
			IsActive:    true,
		},
		{
			ID:          "sim_2",
			DeviceID:    dev.ID,
			SlotIndex:   1,
			SubID:       2,
			CarrierName: "T-Mobile",
			PhoneNumber: "+15550200",
			CountryISO:  "US",
			SignalLevel: 3,
			SignalDbm:   -88,
			IsActive:    true,
		},
	}

	if err := db.ReplaceDeviceSimCards(dev.ID, sims); err != nil {
		t.Fatalf("Failed to replace SIM cards: %v", err)
	}

	retrieved, err := db.GetDevice(dev.ID)
	if err != nil || retrieved == nil {
		t.Fatalf("Failed to retrieve device: %v", err)
	}

	if len(retrieved.SimCards) != 2 {
		t.Errorf("Expected 2 SIM cards, got %d", len(retrieved.SimCards))
	}
	if retrieved.SimCards[0].CarrierName != "Verizon" {
		t.Errorf("Expected Verizon, got %s", retrieved.SimCards[0].CarrierName)
	}
}


func TestEmptyListsEncodeAsArrays(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}
	defer db.Close()

	devices, _ := db.ListDevices()
	webhooks, _ := db.ListWebhooks()
	tokens, _ := db.ListApiTokens()
	deliveries, _ := db.ListWebhookDeliveries("", 10)
	messages, _, _ := db.ListMessages("", "", 10, 0)

	for name, v := range map[string]interface{}{
		"devices": devices, "webhooks": webhooks, "tokens": tokens,
		"deliveries": deliveries, "messages": messages,
	} {
		b, _ := json.Marshal(v)
		if string(b) != "[]" {
			t.Errorf("%s encoded as %s, want []", name, b)
		}
	}
}

func TestReplaceDeviceSimCardsDualSim(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}
	defer db.Close()

	dev := domain.Device{ID: "dev_dual", Name: "Phone", TokenHash: "h", Status: "online", CreatedAt: time.Now().UTC()}
	if err := db.UpsertDevice(&dev); err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}

	// Phones send SIMs without IDs.
	sims := []domain.SimCard{{SlotIndex: 0, SubID: 1, IsActive: true}, {SlotIndex: 1, SubID: 2, IsActive: true}}
	if err := db.ReplaceDeviceSimCards(dev.ID, sims); err != nil {
		t.Fatalf("ReplaceDeviceSimCards: %v", err)
	}
	got, _ := db.GetDeviceSimCards(dev.ID)
	if len(got) != 2 {
		t.Fatalf("got %d SIMs, want 2", len(got))
	}
}
