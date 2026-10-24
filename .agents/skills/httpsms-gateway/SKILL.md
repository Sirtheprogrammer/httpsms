---
name: httpsms-gateway
description: >-
  Send and receive SMS messages, check cellular gateway devices and dual-SIM telemetry,
  inspect received text messages and OTP codes, configure webhooks, and trigger SMS
  automation workflows via the HttpSMS Gateway. Use whenever the user or system needs to
  send text messages, verify incoming SMS, inspect phone SIM status, or integrate cellular
  SMS into an AI workflow.
---

# HttpSMS Gateway Specialist Skill

This skill provides comprehensive instructions for interacting with the **HttpSMS Gateway** platform, which connects cellular Android phones as high-reliability SMS endpoints.

---

## Capabilities & Toolset

When the HttpSMS Gateway MCP server is active, the following tools are available:

Tool Name | Purpose | Key Parameters
:--- | :--- | :---
`httpsms_send_sms` | Dispatch an outbound SMS through an Android phone | `recipient`, `body`, `sim_slot`, `idempotency_key`, `device_id`
`httpsms_list_messages` | List, search, or filter inbound & outbound SMS | `direction`, `search`, `limit`, `offset`
`httpsms_get_message` | Get single message status & carrier delivery confirmation | `id`
`httpsms_list_devices` | Inspect paired phones, battery %, SIM carriers, signal (dBm) | *(none)*
`httpsms_create_webhook` | Register an HTTP endpoint to receive real-time SMS events | `url`, `events`, `secret`
`httpsms_list_webhooks` | List active webhook subscriptions | *(none)*
`httpsms_test_webhook` | Dispatch an instant verification ping to a webhook | `id`
`httpsms_get_stats` | Retrieve system telemetry (inbound/outbound counts, online nodes) | *(none)*
`httpsms_list_workflows` | List automated AI responder workflows | *(none)*

---

## Core Operational Rules & Best Practices

### 1. Character Budget & Encoding Awareness
SMS cost and segment count depend strictly on the character encoding:
- **GSM 7-bit Standard**: Standard Latin letters (`A-Z`, `a-z`), numbers (`0-9`), and basic punctuation.
  - Single message: **Up to 160 characters**.
  - Multi-part message: **153 characters per segment** (7 characters reserved for UDH headers).
- **UCS-2 (Unicode)**: Triggered whenever text contains emojis (e.g. 🚀, 📱), accented letters outside GSM-7, or non-Latin alphabets (Arabic, Chinese, Cyrillic, Hebrew, etc.).
  - Single message: **Up to 70 characters**.
  - Multi-part message: **67 characters per segment**.
- **Rule**: If character length or carrier charges matter, avoid decorative emojis in automated alerts.

### 2. Mandatory Idempotency for Critical Alerts
Always generate and pass a unique `idempotency_key` (e.g. UUIDv4 or order hash `order_1042_shipment`) when calling `httpsms_send_sms` for transactional SMS, OTPs, or automated alerts. The gateway automatically discards duplicates if a network retry occurs.

### 3. Dual-SIM Selection
Modern Android phones frequently have two SIM cards (e.g., Slot 0 = Personal/Carrier A, Slot 1 = Business/Carrier B).
- Call `httpsms_list_devices` to inspect which carriers are in Slot 0 and Slot 1, and check their signal dBm.
- Pass `sim_slot: 0` or `sim_slot: 1` in `httpsms_send_sms` to select the carrier with better local rates or signal strength.

### 4. Zero-Repetition Inbound Verification
Incoming SMS messages are deduplicated at both the Android handset level (via Room SQLite fingerprint) and server level. When looking for an incoming OTP or customer reply:
1. Call `httpsms_list_messages` with `direction: "inbound"` and `search: "+1234567890"`.
2. Inspect the latest message timestamp and body.
3. If setting up ongoing real-time ingestion, use `httpsms_create_webhook` instead of aggressive polling loops.

---

## Common Workflows & Procedures

### Workflow A: Send an Outbound SMS
1. Verify device status:
   ```json
   // Call httpsms_list_devices to ensure at least 1 gateway handset is online
   ```
2. Compute or verify message length:
   - Recipient must be formatted with international country code (E.164, e.g. `+15551234567`).
3. Send message:
   ```json
   {
     "recipient": "+15551234567",
     "body": "Your verification code is 482910. Expires in 10 minutes.",
     "sim_slot": 0,
     "idempotency_key": "otp_tx_94821a"
   }
   ```
4. Verify returned JSON has status `queued` or `dispatched`.

### Workflow B: Check for Inbound SMS / OTP Code
1. Call `httpsms_list_messages`:
   ```json
   {
     "direction": "inbound",
     "limit": 10
   }
   ```
2. If looking for a message from a specific contact, provide `search: "+15551234567"`.
3. Read the `body` field of the matching message.

### Workflow C: Set Up Real-Time Webhook Forwarding
1. Call `httpsms_create_webhook`:
   ```json
   {
     "url": "https://api.yourdomain.com/webhooks/sms",
     "events": ["sms.received", "sms.delivered", "sms.failed"],
     "secret": "whsec_super_secret_signing_key_32bytes"
   }
   ```
2. Verify delivery with `httpsms_test_webhook` using the returned webhook ID.

---

## Detailed References & Guides

- [API Specification & Endpoints](./references/api-reference.md)
- [Webhook Verification Guide (HMAC-SHA256)](./references/webhooks-guide.md)
- [MCP Server Setup & Config](./references/mcp-setup.md)
- [Python & Node.js Developer SDKs](../../../integrations/README.md)

