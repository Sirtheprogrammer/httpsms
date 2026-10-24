# HttpSMS Gateway & Telemetry Platform

An enterprise-grade, self-hosted SMS gateway infrastructure that transforms Android devices into programmable, high-reliability SMS endpoints. Features zero-duplicate delivery semantics, full bi-directional cellular bridging, signed webhooks with exponential backoff, scoped API token authorization, Over-The-Air (OTA) app updates, multi-user accounts, and a visual workflow builder for SMS AI agents (OpenAI, Anthropic, Gemini, DeepSeek; bring your own key).

---

## ⚡ Architectural Highlights

- **Duplex Persistent Bridge (WebSocket TLS)**: Bypasses mobile carrier NAT and cellular firewalls without requiring static or public IPs on Android handsets.
- **Zero-Duplicate Guarantee**: Multi-layer deduplication combining on-device Room SQLite fingerprinting:
  `SHA-256(sender + floor(timestamp / 60000) + sim_slot + normalized_body)`
  with server-side idempotency keys and transactional outbox queues.
- **Dual-SIM & Multipart SMS Routing**: Automatic detection of active SIM slots via Android `SubscriptionManager`, with automatic GSM 7-bit vs UCS-2 Unicode character analysis.
- **Webhook Dispatcher Pool**: Real-time async webhook delivery with HMAC-SHA256 payload signing (`X-Signature-SHA256`), configurable event subscriptions, and exponential retry backoff (15s, 1m, 5m, 15m, 1h).
- **Over-The-Air (OTA) Updates**: Remote APK publishing, integrity checksum validation, and native Android `PackageInstaller` auto-updates.
- **Multi-user**: open sign-up; the first account becomes the administrator. Each user owns their own phones, messages, webhooks, API tokens and workflows. Admins see everything, manage users and publish app releases.
- **Workflow builder for AI agents**: a drag-and-drop canvas (React Flow). Trigger on incoming SMS, call an AI model, branch on conditions, send SMS, call HTTP APIs, wait. AI steps can remember the conversation with each contact.
- **Bring your own key**: users add their own OpenAI, Anthropic, Gemini or DeepSeek keys, encrypted at rest with AES-256-GCM and never shown to anyone (admins included).
- **React dashboard**: light/dark, mobile-friendly, live updates over an authenticated WebSocket.

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

Open `http://localhost:8080`. Visitors who aren't signed in see a landing page with the latest Android APK (fetched from GitHub Releases) and a link to sign up. Create the first account; it becomes the administrator.

| Variable | Purpose |
|---|---|
| `SECRET_KEY` | Encrypts users' AI provider keys. If unset, a random key is created at `data/secret.key`. Back it up: losing it makes stored keys unreadable. |
| `MASTER_SECRET` | Optional legacy admin Bearer token for scripts. Leave empty to disable. |
| `DB_PATH`, `OTA_DIR`, `WEB_DIR`, `PORT` | Storage paths, built dashboard directory, listen port. |

To view container logs:
```bash
docker compose logs -f
```

---

## Local Development & Make Commands

```bash
# Build the React dashboard (dashboard/dist) and the Go server
make all

# Run the server on :8080 (serves dashboard/dist)
make run

# Dashboard with hot reload on :5173, proxying API calls to :8080
make dev

# Execute Go unit tests (Deduplication & HMAC tests)
make test

# Build and run Docker container
make docker-build
make docker-up
```

---

##  Android Gateway App Setup

1. Install the APK. Every push to `main` that touches `android/` builds one and attaches it to a
   [GitHub release](https://github.com/Sirtheprogrammer/httpsms/releases). To build locally (JDK 17–21):
   ```bash
   cd android && ./gradlew assembleDebug
   adb install -r app/build/outputs/apk/debug/app-debug.apk
   ```
2. Open the dashboard → **Gateway Nodes** → **Pair New Phone**. Check the
   **Server address the phone will use**: it must be reachable from the phone (your machine's LAN IP or
   public domain, never `localhost`).
3. In the app tap **Scan pairing QR** and point it at the code. The app saves the server and token,
   asks for SMS permissions and connects. The dashboard confirms when the phone is online.
   (No camera? Use **Enter details manually** with the URL and token from *Can't scan?*.)
4. Work through the app's **Keep running in the background** checklist: allow notifications,
   set battery to unrestricted, and turn on the vendor autostart switch where the phone has one.

### Background survival

The gateway runs as a `remoteMessaging` foreground service with a persistent notification and:

- holds a renewable partial wake lock and a Wi-Fi lock so the socket survives screen-off;
- reconnects with exponential backoff (2 s → 60 s) and immediately when the network returns;
- restarts after reboot and after app updates (only if it was running before);
- runs a WorkManager watchdog every 15 minutes that restarts the service if the OS killed it.

Android still lets some vendors (Xiaomi, Huawei, Oppo, Vivo, Samsung…) kill background apps
unless the user exempts them, which is what the in-app checklist and the
[dontkillmyapp.com](https://dontkillmyapp.com) link are for.

---

## Workflows & AI agents

1. **AI keys** → *Add key*: pick a provider and paste your key. It's verified by listing the provider's models, then stored encrypted.
2. **Workflows** → *New workflow*: start from a template (AI auto-reply, keyword responder, AI triage + escalation, forward to API) or a blank canvas.
3. Click a step to configure it. Text fields accept variables such as `{{trigger.body}}`, `{{trigger.sender}}` or `{{ai_1.text}}` (click the chips to insert them).
4. **Test** runs the canvas against a sample SMS: AI steps really call the model, SMS and non-GET HTTP steps are simulated. Then flip the toggle to go live.

Safety: a workflow stops running for a sender after 10 runs in 10 minutes, so two auto-replying gateways can't text each other forever. Waits are capped at 5 minutes and runs at 50 steps.

---

##  REST API Reference

API tokens (`htsms_live_…`, created under **API tokens**) belong to the user who made them and only see that user's data. Scopes: `sms:send`, `sms:read`, `devices:manage`, `webhooks:manage`, `workflows:manage`, or `*`.


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

