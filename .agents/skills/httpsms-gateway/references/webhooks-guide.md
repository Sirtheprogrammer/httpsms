# Webhooks Integration & Signature Verification Guide

The HttpSMS Gateway delivers real-time HTTP POST notifications whenever SMS events happen on your cellular network.

---

## Webhook Headers

Header | Description
:--- | :---
`Content-Type` | `application/json`
`User-Agent` | `HttpSMS-Gateway-Webhook/1.0`
`X-Event-Type` | The event name (e.g. `sms.received`, `sms.delivered`, `sms.failed`)
`X-Delivery-ID` | Unique UUID of the delivery attempt
`X-Signature-SHA256` | Hexadecimal HMAC-SHA256 of the raw body using your webhook secret

---

## Event Types & Payloads

### 1. `sms.received` (Inbound SMS)
Triggered when an SMS arrives on the Android phone.
```json
{
  "event": "sms.received",
  "timestamp": "2026-10-24T19:10:00Z",
  "data": {
    "id": "message-uuid",
    "direction": "inbound",
    "device_id": "device-uuid",
    "sim_slot": 0,
    "sender": "+15551234567",
    "recipient": "local_sim",
    "body": "Your appointment is confirmed for Tuesday.",
    "status": "received",
    "idempotency_key": "sha256_fingerprint",
    "parts_count": 1,
    "created_at": "2026-10-24T19:10:00Z"
  }
}
```

### 2. `sms.delivered` (Carrier Delivery Receipt)
Triggered when the cellular carrier returns a delivery confirmation report.
```json
{
  "event": "sms.delivered",
  "timestamp": "2026-10-24T19:10:05Z",
  "data": {
    "id": "message-uuid",
    "status": "delivered",
    "delivered_at": "2026-10-24T19:10:04Z"
  }
}
```

---

## Verifying Signatures

Always verify `X-Signature-SHA256` in your production receiver to guarantee requests originated from your HttpSMS Gateway instance.

### Python (FastAPI / Flask)
```python
import hmac
import hashlib
from fastapi import FastAPI, Request, HTTPException, Header

app = FastAPI()
WEBHOOK_SECRET = "whsec_your_secret_here"

@app.post("/webhooks/sms")
async def handle_sms_webhook(
    request: Request,
    x_signature_sha256: str = Header(None),
    x_event_type: str = Header(None)
):
    raw_body = await request.body()
    
    if WEBHOOK_SECRET:
        computed = hmac.new(WEBHOOK_SECRET.encode(), raw_body, hashlib.sha256).hexdigest()
        if not hmac.compare_digest(computed, x_signature_sha256 or ""):
            raise HTTPException(status_code=401, detail="Invalid signature")

    payload = await request.json()
    print(f"Received event {x_event_type}: {payload}")
    return {"status": "ok"}
```

### Node.js (Express)
```javascript
const express = require('express');
const crypto = require('crypto');

const app = express();
const WEBHOOK_SECRET = 'whsec_your_secret_here';

// Use express.raw or verify function with json parser
app.post('/webhooks/sms', express.raw({ type: 'application/json' }), (req, res) => {
  const signature = req.headers['x-signature-sha256'];
  const rawBody = req.body;

  if (WEBHOOK_SECRET) {
    const hmac = crypto.createHmac('sha256', WEBHOOK_SECRET);
    hmac.update(rawBody);
    const expected = hmac.digest('hex');

    if (!crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(signature || ''))) {
      return res.status(401).send('Invalid signature');
    }
  }

  const payload = JSON.parse(rawBody.toString('utf8'));
  console.log('Valid SMS webhook received:', payload.event, payload.data);
  res.json({ received: true });
});
```

### Go Receiver
```go
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
)

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	secret := "whsec_your_secret_here"
	sig := r.Header.Get("X-Signature-SHA256")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		http.Error(w, "Unauthorized signature", http.StatusUnauthorized)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"received": true}`))
}
```

