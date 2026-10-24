# HttpSMS Gateway: Integrations & AI Agent Tooling

Welcome to the **HttpSMS Gateway Integration Suite**. This directory contains official SDKs, MCP (Model Context Protocol) servers, helper scripts, and documentation for interacting with HttpSMS Gateway from AI agents, automated workflows, and backend services.

---

## Architecture Overview

```
                               ┌────────────────────────────────┐
                               │   AI Agents & LLM Runtimes     │
                               │  (Claude, Cursor, Antigravity) │
                               └───────────────┬────────────────┘
                                               │ MCP (stdio) / Tool Calls
                                               ▼
┌───────────────────────┐      ┌────────────────────────────────┐      ┌─────────────────────────┐
│   Python Backend      │─────▶│     HttpSMS MCP Server         │◀─────│  Node / TypeScript App  │
│     (@httpsms/sdk)    │      │  (Go Binary or Python Server)  │      │      (@httpsms/sdk)     │
└───────────┬───────────┘      └───────────────┬────────────────┘      └────────────┬────────────┘
            │                                  │                                    │
            │ REST / Bearer Auth               │ REST / Bearer Auth                 │ REST / Bearer Auth
            ▼                                  ▼                                    ▼
       ┌─────────────────────────────────────────────────────────────────────────────────┐
       │                       HttpSMS Gateway Server (Golang:8080)                      │
       │                   Persistent WebSocket Duplex Hub / SQLite WAL                  │
       └───────────────────────────────────────┬─────────────────────────────────────────┘
                                               │
                                               │ WebSocket (NAT Traversal)
                                               ▼
                               ┌────────────────────────────────┐
                               │  Android Handset (Kotlin App)   │
                               │  SIM 0 (Carrier A) / SIM 1 (B) │
                               └────────────────────────────────┘
```

---

## 1. Model Context Protocol (MCP) for AI Agents

The gateway provides both native Go and Python MCP servers implementing JSON-RPC 2.0 stdio:

- **Go Binary**: `server/bin/httpsms-mcp` (zero-dependency, ~15MB RAM, instant boot).
- **Python Server**: `integrations/mcp/server.py` (pure Python 3 standard library).

### Quick Setup (`mcp_config.json`)
Add the server to your agent host configuration (e.g., Claude Desktop, Cursor, or Antigravity):

```json
{
  "mcpServers": {
    "httpsms-gateway": {
      "command": "/absolute/path/to/httpsms/server/bin/httpsms-mcp",
      "env": {
        "HTTPSMS_BASE_URL": "http://localhost:8080",
        "HTTPSMS_API_KEY": "your_api_bearer_token"
      }
    }
  }
}
```

### Supported MCP Tools

| Tool | Description |
| :--- | :--- |
| `httpsms_send_sms` | Send outbound SMS via SIM 0 or 1 with idempotency protection |
| `httpsms_list_messages` | Query received and sent SMS with pagination and search |
| `httpsms_get_message` | Fetch delivery state and timestamp for a message UUID |
| `httpsms_list_devices` | Inspect paired phones, battery %, charging state, signal (dBm) |
| `httpsms_create_webhook`| Register an HTTPS webhook for real-time signed event push |
| `httpsms_list_webhooks` | List registered webhooks and their delivery statistics |
| `httpsms_test_webhook`  | Dispatch a test ping to verify endpoint connectivity |
| `httpsms_get_stats`     | Retrieve gateway metrics (inbound/outbound/online devices) |

See [MCP Setup Guide](../.agents/skills/httpsms-gateway/references/mcp-setup.md) for full configuration steps.

---

## 2. Python SDK

Zero external dependencies! Works out of the box with standard Python 3.8+.

### Installation
```bash
pip install -e integrations/python
```

### Usage
```python
from httpsms import HttpSmsClient

client = HttpSmsClient(
    api_key="htsms_live_mcp_test_token_20261024_secret",
    base_url="http://localhost:8080"
)

# 1. Inspect connected phones & SIM cards
devices = client.devices.list()
for d in devices:
    print(f"Device: {d['name']} | Battery: {d['battery_level']}%")
    for sim in d.get("sim_cards", []):
        print(f"  Slot {sim['slot_index']}: {sim['carrier_name']} ({sim['phone_number']})")

# 2. Dispatch an outbound SMS
msg = client.sms.send(
    recipient="+255797455136",
    body="Your package has arrived at the front desk.",
    sim_slot=0,
    idempotency_key="pkg_order_48102"
)
print("Sent message UUID:", msg["id"])

# 3. List recent inbound messages
inbox = client.sms.list(direction="inbound", limit=5)
for m in inbox["data"]:
    print(f"From {m['sender']}: {m['body']}")

# 4. Webhook verification
is_valid = client.webhooks.verify_signature(
    payload_bytes=request_raw_body,
    signature_header=request.headers.get("X-Signature-SHA256"),
    secret="whsec_my_signing_key"
)
```

---

## 3. TypeScript / Node.js SDK

Fully typed, ESM & CommonJS compatible with zero dependencies (uses modern `fetch` and standard Web Crypto).

### Installation
```bash
cd integrations/typescript && npm run build
```

### Usage
```typescript
import { HttpSmsClient } from '@httpsms/sdk';

const client = new HttpSmsClient({
  apiKey: process.env.HTTPSMS_API_KEY,
  baseUrl: 'http://localhost:8080'
});

// Check system telemetry
const stats = await client.system.getStats();
console.log(`Active nodes: ${stats.online_devices}, Inbound: ${stats.total_inbound}`);

// Send SMS
const message = await client.sms.send({
  recipient: '+255797455136',
  body: 'Your appointment is confirmed for tomorrow at 10 AM.',
  sim_slot: 0,
  idempotency_key: 'appt_user_9921'
});
console.log('Status:', message.status);

// Verify Webhook signature in Express/Next.js
const isValid = await HttpSmsClient.verifyWebhookSignature(
  rawBody,
  req.headers['x-signature-sha256'],
  process.env.WEBHOOK_SECRET!
);
```

---

## 4. Antigravity Skill Integration

The gateway includes a native **Antigravity Skill** in `.agents/skills/httpsms-gateway/`:

- **Skill Entrypoint**: `SKILL.md` (provides procedural workflow logic, character encoding budgets, and dual-SIM routing).
- **Interactive Scripts**:
  - `send_sms.sh`: Send SMS directly from bash or agent tool commands.
  - `check_gateway.sh`: Inspect phone battery, cellular dBm, and message flow.
  - `listen_webhooks.py`: Local test listener for incoming webhook pings.
- **Agent Examples**:
  - `otp_verification.py`: Verification code dispatch and confirmation loop.
  - `agent_workflow.py`: OpenAI / Gemini tool declaration schemas.

