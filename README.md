# HttpSMS Gateway & Telemetry Platform

An enterprise-grade, self-hosted SMS gateway infrastructure that transforms Android devices into programmable, high-reliability SMS endpoints. Features zero-duplicate delivery semantics, full bi-directional cellular bridging, signed webhooks with exponential backoff, scoped API token authorization, Over-The-Air (OTA) app updates, and a high-density telecommunications telemetry dashboard built with Vanilla JS.

---

## ⚡ Architectural Highlights

- **Duplex Persistent Bridge (WebSocket TLS)**: Bypasses mobile carrier NAT and cellular firewalls without requiring static or public IPs on Android handsets.
- **Zero-Duplicate Guarantee**: Multi-layer deduplication combining on-device Room SQLite fingerprinting:
  $$\text{SHA-256}(\text{Sender} + \lfloor\text{Timestamp}/60000\rfloor + \text{SIM\_Slot} + \text{NormalizedBody})$$
  with server-side idempotency keys and transactional outbox queues.
- **Dual-SIM & Multipart SMS Routing**: Automatic detection of active SIM slots via Android `SubscriptionManager`, with automatic GSM 7-bit vs UCS-2 Unicode character analysis.
- **Webhook Dispatcher Pool**: Real-time async webhook delivery with HMAC-SHA256 payload signing (`X-Signature-SHA256`), configurable event subscriptions, and exponential retry backoff (15s, 1m, 5m, 15m, 1h).
- **Over-The-Air (OTA) Updates**: Remote APK publishing, integrity checksum validation, and native Android `PackageInstaller` auto-updates.
- **Bespoke Telemetry Dashboard**: Crafted with modern Vanilla JS and clean CSS tokens — rejecting cliché AI-generated designs in favor of a sleek, high-density telecommunications operations console.

---

## Quickstart (Docker Compose)

The easiest way to run HttpSMS Gateway is via Docker Compose:

```bash
# Clone the repository
git clone https://github.com/Sirtheprogrammer/httpsms.git
cd httpsms

# Launch the server in detached mode
docker compose up -d
```

Access the Web Console at `http://localhost:8080`.

To view container logs:
```bash
docker compose logs -f
```

---

## Local Development & Make Commands

```bash
# Build the Go backend binary
make build

# Run local development server
make run

# Execute Go unit tests (Deduplication & HMAC tests)
make test

# Build and run Docker container
make docker-build
make docker-up
```

---

##  Android Gateway App Setup

1. Open the project in Android Studio (`/android` directory).
2. Build and install the APK onto your Android phone:
   ```bash
   ./gradlew assembleDebug
   adb install -r app/build/outputs/apk/debug/app-debug.apk
   ```
3. Launch the app and grant required permissions:
   - SMS Receive & Send (`RECEIVE_SMS`, `SEND_SMS`)
   - Phone State & SIM Info (`READ_PHONE_STATE`, `READ_PHONE_NUMBERS`)
   - Battery Optimization Exemption
4. Open the Web Dashboard at `http://<your-server-ip>:8080` -> Navigate to **Gateway Nodes** -> Click **Pair New Phone**.
5. Scan the QR code or enter the Server URL and Token into the Android app.
6. Tap **Start Gateway Service**. Your phone's SIM cards, battery level, and signal strength will immediately appear on the dashboard!

---

##  REST API Reference

All requests must include an `Authorization: Bearer <API_TOKEN>` header (or the master administrative secret).

### 1. Send SMS
```http
POST /api/v1/sms/send
Content-Type: application/json
Authorization: Bearer htsms_live_...

{
  "recipient": "+1234567890",
  "body": "Your one-time login code is 829104",
  "sim_slot": 0,
  "idempotency_key": "unique_tx_order_1002"
}
```

### 2. Query Messages (Inbound & Outbound)
```http
GET /api/v1/messages?direction=inbound&search=+1234567890&limit=20
Authorization: Bearer htsms_live_...
```

### 3. List Gateway Devices & SIM Slots
```http
GET /api/v1/devices
Authorization: Bearer htsms_live_...
```

### 4. Register Webhook
```http
POST /api/v1/webhooks
Content-Type: application/json
Authorization: Bearer htsms_live_...

{
  "url": "https://api.example.com/sms/webhook",
  "events": ["sms.received", "sms.delivered", "sms.failed"],
  "secret": "whsec_your_custom_signing_secret"
}
```

---

##  Verifying Webhook Signatures

Every webhook delivery includes an `X-Signature-SHA256` header containing the hexadecimal HMAC-SHA256 hash of the raw request payload.

### Python Verification Example
```python
import hmac
import hashlib

def verify_webhook(raw_payload: bytes, signature_header: str, secret: str) -> bool:
    computed = hmac.new(secret.encode(), raw_payload, hashlib.sha256).hexdigest()
    return hmac.compare_digest(computed, signature_header)
```

### Node.js Verification Example
```javascript
const crypto = require('crypto');

function verifyWebhook(rawPayload, signatureHeader, secret) {
  const computed = crypto.createHmac('sha256', secret).update(rawPayload).digest('hex');
  return crypto.timingSafeEqual(Buffer.from(computed), Buffer.from(signatureHeader));
}
```

---

##  Over-The-Air (OTA) Updates

1. In the Web Console, navigate to **OTA Releases**.
2. Select your newly compiled `app-release.apk`, assign a Semantic Version (e.g. `1.1.0`) and Version Code (e.g. `2`), and click **Publish & Broadcast OTA Update**.
3. All connected Android devices receive an instant WebSocket notification, verify the SHA-256 hash against the server manifest, and trigger the native Android package installer.

