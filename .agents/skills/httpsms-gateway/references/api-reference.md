# HttpSMS Gateway REST API Reference

Base URL: `http://localhost:8080/api/v1` (or your production domain).

## Authentication
Every request must supply a Bearer token header:
```http
Authorization: Bearer <API_TOKEN>
```

API tokens can be created via the web dashboard or `POST /api/v1/tokens`.

---

## Endpoints

### 1. Send SMS
- **Endpoint**: `POST /api/v1/sms/send`
- **Required Scope**: `sms:send`
- **Request Body**:
```json
{
  "recipient": "+15551234567",
  "body": "Hello world from HttpSMS Gateway!",
  "sim_slot": 0,
  "device_id": "optional-device-uuid",
  "idempotency_key": "unique-transaction-key-1234"
}
```
- **Response** (`202 Accepted`):
```json
{
  "id": "7f8b9c2a-1122-3344-5566-778899aabbcc",
  "direction": "outbound",
  "recipient": "+15551234567",
  "body": "Hello world from HttpSMS Gateway!",
  "sim_slot": 0,
  "status": "queued",
  "parts_count": 1,
  "created_at": "2026-10-24T19:00:00Z"
}
```

---

### 2. List Messages
- **Endpoint**: `GET /api/v1/messages`
- **Required Scope**: `sms:read`
- **Query Parameters**:
  - `direction`: `inbound` | `outbound` | *(empty for all)*
  - `search`: Filter matching phone number or text snippet
  - `limit`: Number of records (1-100, default: 20)
  - `offset`: Pagination offset index
- **Response** (`200 OK`):
```json
{
  "data": [
    {
      "id": "...",
      "direction": "inbound",
      "sender": "+15559876543",
      "recipient": "local_sim",
      "body": "Yes, I confirm my booking.",
      "status": "received",
      "idempotency_key": "sha256_fingerprint",
      "parts_count": 1,
      "created_at": "2026-10-24T19:05:00Z"
    }
  ],
  "limit": 20,
  "offset": 0,
  "total": 42
}
```

---

### 3. List Gateway Devices
- **Endpoint**: `GET /api/v1/devices`
- **Required Scope**: `devices:manage`
- **Response** (`200 OK`):
```json
[
  {
    "id": "device-uuid",
    "name": "Samsung Galaxy S24 Gateway",
    "status": "online",
    "battery_level": 94,
    "is_charging": true,
    "app_version": "1.0.0",
    "last_seen_at": "2026-10-24T19:15:30Z",
    "sim_cards": [
      {
        "slot_index": 0,
        "carrier_name": "T-Mobile",
        "phone_number": "+15551234567",
        "signal_level": 4,
        "signal_dbm": -76,
        "is_active": true
      }
    ]
  }
]
```

---

### 4. Create Webhook
- **Endpoint**: `POST /api/v1/webhooks`
- **Required Scope**: `webhooks:manage`
- **Request Body**:
```json
{
  "url": "https://api.yourdomain.com/webhooks/sms",
  "events": ["sms.received", "sms.delivered", "sms.failed"],
  "secret": "whsec_optional_signing_secret"
}
```

---

### 5. System Stats
- **Endpoint**: `GET /api/v1/system/stats`
- **Required Scope**: `sms:read`
- **Response** (`200 OK`):
```json
{
  "online_devices": 1,
  "total_devices": 1,
  "total_inbound": 128,
  "total_outbound": 240,
  "active_webhooks": 2,
  "timestamp": "2026-10-24T19:20:00Z"
}
```

