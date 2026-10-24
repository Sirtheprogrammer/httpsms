package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/httpsms/gateway-server/internal/domain"
	"github.com/httpsms/gateway-server/internal/storage"
)

type Dispatcher struct {
	store      *storage.DB
	httpClient *http.Client
	eventChan  chan deliveryJob
	stopChan   chan struct{}
	wg         sync.WaitGroup
}

type deliveryJob struct {
	Delivery domain.WebhookDelivery
	Webhook  domain.Webhook
}

func NewDispatcher(store *storage.DB) *Dispatcher {
	return &Dispatcher{
		store: store,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		eventChan: make(chan deliveryJob, 1000),
		stopChan:  make(chan struct{}),
	}
}

// Start launches worker goroutines
func (d *Dispatcher) Start(workerCount int) {
	for i := 0; i < workerCount; i++ {
		d.wg.Add(1)
		go d.worker()
	}
	// Start retry loop in background
	d.wg.Add(1)
	go d.retryLoop()
}

func (d *Dispatcher) Stop() {
	close(d.stopChan)
	d.wg.Wait()
}

// Dispatch queues an event for delivery to the owner's subscribed webhooks
func (d *Dispatcher) Dispatch(owner, eventType string, messageID *string, payloadData interface{}) error {
	if owner == "" {
		return nil // unowned data (no accounts yet) has no webhooks
	}
	webhooks, err := d.store.ListActiveWebhooksForEvent(owner, eventType)
	if err != nil {
		return fmt.Errorf("failed to query webhooks: %w", err)
	}

	if len(webhooks) == 0 {
		return nil
	}

	payloadBytes, err := json.Marshal(map[string]interface{}{
		"event":     eventType,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"data":      payloadData,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	for _, wh := range webhooks {
		delivery := domain.WebhookDelivery{
			ID:        uuid.New().String(),
			WebhookID: wh.ID,
			MessageID: messageID,
			EventType: eventType,
			Payload:   string(payloadBytes),
			Attempt:   1,
			Status:    "pending",
			CreatedAt: time.Now().UTC(),
		}

		if err := d.store.SaveWebhookDelivery(&delivery); err != nil {
			log.Printf("[Webhook] Error saving delivery %s: %v", delivery.ID, err)
			continue
		}

		select {
		case d.eventChan <- deliveryJob{Delivery: delivery, Webhook: wh}:
		default:
			log.Printf("[Webhook] Queue full, dropping job %s for webhook %s", delivery.ID, wh.ID)
		}
	}

	return nil
}

// TestWebhook sends a test payload immediately and returns the execution result
func (d *Dispatcher) TestWebhook(wh *domain.Webhook) (*domain.WebhookDelivery, error) {
	testPayload, _ := json.Marshal(map[string]interface{}{
		"event":     "webhook.test",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"data": map[string]string{
			"message": "This is a verification test ping from HttpSMS Gateway",
			"status":  "operational",
		},
	})

	delivery := domain.WebhookDelivery{
		ID:        uuid.New().String(),
		WebhookID: wh.ID,
		EventType: "webhook.test",
		Payload:   string(testPayload),
		Attempt:   1,
		Status:    "pending",
		CreatedAt: time.Now().UTC(),
	}

	d.executeDelivery(&delivery, wh)
	_ = d.store.SaveWebhookDelivery(&delivery)
	return &delivery, nil
}

func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for {
		select {
		case <-d.stopChan:
			return
		case job, ok := <-d.eventChan:
			if !ok {
				return
			}
			d.executeDelivery(&job.Delivery, &job.Webhook)
			_ = d.store.SaveWebhookDelivery(&job.Delivery)
		}
	}
}

func (d *Dispatcher) executeDelivery(delivery *domain.WebhookDelivery, wh *domain.Webhook) {
	start := time.Now()

	req, err := http.NewRequest(http.MethodPost, wh.URL, bytes.NewBufferString(delivery.Payload))
	if err != nil {
		delivery.Status = "failed"
		delivery.ResponseBody = fmt.Sprintf("failed to create request: %v", err)
		delivery.LatencyMs = time.Since(start).Milliseconds()
		d.scheduleRetry(delivery)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "HttpSMS-Gateway-Webhook/1.0")
	req.Header.Set("X-Delivery-ID", delivery.ID)
	req.Header.Set("X-Event-Type", delivery.EventType)

	// Compute HMAC-SHA256 signature if secret is configured
	if wh.Secret != "" {
		mac := hmac.New(sha256.New, []byte(wh.Secret))
		mac.Write([]byte(delivery.Payload))
		signature := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Signature-SHA256", signature)
	}

	resp, err := d.httpClient.Do(req)
	delivery.LatencyMs = time.Since(start).Milliseconds()

	if err != nil {
		delivery.Status = "retrying"
		delivery.ResponseBody = fmt.Sprintf("network error: %v", err)
		d.scheduleRetry(delivery)
		return
	}
	defer resp.Body.Close()

	delivery.ResponseStatus = resp.StatusCode
	bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	delivery.ResponseBody = string(bodySnippet)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		delivery.Status = "success"
		delivery.NextRetryAt = nil
	} else {
		delivery.Status = "retrying"
		d.scheduleRetry(delivery)
	}
}

func (d *Dispatcher) scheduleRetry(delivery *domain.WebhookDelivery) {
	// Backoff schedule: 1: 15s, 2: 1m, 3: 5m, 4: 15m, 5: 1h, then fail
	const maxAttempts = 5
	if delivery.Attempt >= maxAttempts {
		delivery.Status = "failed"
		delivery.NextRetryAt = nil
		return
	}

	delivery.Attempt++
	var delay time.Duration
	switch delivery.Attempt {
	case 2:
		delay = 15 * time.Second
	case 3:
		delay = 1 * time.Minute
	case 4:
		delay = 5 * time.Minute
	case 5:
		delay = 15 * time.Minute
	default:
		delay = 1 * time.Hour
	}

	retryTime := time.Now().UTC().Add(delay)
	delivery.NextRetryAt = &retryTime
}

func (d *Dispatcher) retryLoop() {
	defer d.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopChan:
			return
		case <-ticker.C:
			// Query pending retries from database
			deliveries, err := d.store.ListWebhookDeliveries("", "", 200)
			if err != nil {
				continue
			}

			now := time.Now().UTC()
			for _, del := range deliveries {
				if del.Status == "retrying" && del.NextRetryAt != nil && del.NextRetryAt.Before(now) {
					wh, err := d.store.GetWebhook(del.WebhookID)
					if err != nil || wh == nil || !wh.IsActive {
						continue
					}
					d.executeDelivery(&del, wh)
					_ = d.store.SaveWebhookDelivery(&del)
				}
			}
		}
	}
}
