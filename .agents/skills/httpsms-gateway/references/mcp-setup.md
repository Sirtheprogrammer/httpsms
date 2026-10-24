# HttpSMS Gateway MCP (Model Context Protocol) Setup Guide

This guide details how to integrate the **HttpSMS Gateway MCP Server** into your AI agent toolchain (Claude Desktop, Antigravity, Cursor, Cline, Roo Code, etc.).

---

## 1. Supported MCP Servers

The project includes two pre-built MCP servers that implement the same standard Model Context Protocol (v2024-11-05) over stdio:

1. **Native Go Stdio Binary** (Recommended for speed & zero runtime dependency):
   - Location: `server/bin/httpsms-mcp`
   - Source: `server/cmd/mcp/main.go`
   - Memory footprint: ~15MB RSS, instantaneous startup.

2. **Python Stdio Server** (Pure Python 3 standard library):
   - Location: `integrations/mcp/server.py`
   - Source: `integrations/mcp/server.py`
   - Requires: Python 3.8+ (no third-party pip dependencies required).

---

## 2. Configuration Options

Both servers read configuration via environment variables:

| Environment Variable | Description | Default |
| :--- | :--- | :--- |
| `HTTPSMS_BASE_URL` | Base URL of the running HttpSMS Gateway server | `http://localhost:8080` |
| `HTTPSMS_API_KEY` | API Bearer token with appropriate scopes | *(Required for live actions)* |

> [!TIP]
> Ensure your API token has the `sms:send`, `sms:read`, `devices:manage`, and `webhooks:manage` scopes. Tokens can be generated in the Dashboard under **API Tokens** or created via the database.

---

## 3. Client Integration Examples

### Antigravity & Gemini CLI
Add to `~/.gemini/config/mcp_config.json` or project `./mcp_config.json`:

```json
{
  "mcpServers": {
    "httpsms-gateway": {
      "command": "/absolute/path/to/httpsms/server/bin/httpsms-mcp",
      "env": {
        "HTTPSMS_BASE_URL": "http://localhost:8080",
        "HTTPSMS_API_KEY": "your_api_token_here"
      }
    }
  }
}
```

### Claude Desktop
Add to `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `~/.config/Claude/claude_desktop_config.json` (Linux) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "httpsms": {
      "command": "/home/leeh/Desktop/httpsms/server/bin/httpsms-mcp",
      "env": {
        "HTTPSMS_BASE_URL": "http://localhost:8080",
        "HTTPSMS_API_KEY": "htsms_live_mcp_test_token_20261024_secret"
      }
    }
  }
}
```

Or using the Python implementation:
```json
{
  "mcpServers": {
    "httpsms": {
      "command": "python3",
      "args": ["/home/leeh/Desktop/httpsms/integrations/mcp/server.py"],
      "env": {
        "HTTPSMS_BASE_URL": "http://localhost:8080",
        "HTTPSMS_API_KEY": "htsms_live_mcp_test_token_20261024_secret"
      }
    }
  }
}
```

### Cursor IDE
Add to `.cursor/mcp.json` in your workspace root:

```json
{
  "mcpServers": {
    "httpsms": {
      "command": "/home/leeh/Desktop/httpsms/server/bin/httpsms-mcp",
      "env": {
        "HTTPSMS_BASE_URL": "http://localhost:8080",
        "HTTPSMS_API_KEY": "htsms_live_mcp_test_token_20261024_secret"
      }
    }
  }
}
```

---

## 4. Testing Your MCP Server Interactively

You can test the MCP server over stdio using `jq` or standard pipes:

```bash
# 1. Initialize MCP handshake
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0"}}}' | \
  HTTPSMS_BASE_URL="http://localhost:8080" \
  HTTPSMS_API_KEY="htsms_live_mcp_test_token_20261024_secret" \
  ./server/bin/httpsms-mcp

# 2. List available tools
echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' | \
  HTTPSMS_BASE_URL="http://localhost:8080" \
  HTTPSMS_API_KEY="htsms_live_mcp_test_token_20261024_secret" \
  ./server/bin/httpsms-mcp

# 3. Call system telemetry tool
echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"httpsms_get_stats","arguments":{}}}' | \
  HTTPSMS_BASE_URL="http://localhost:8080" \
  HTTPSMS_API_KEY="htsms_live_mcp_test_token_20261024_secret" \
  ./server/bin/httpsms-mcp
```

---

## 5. Available Tools Summary

- `httpsms_send_sms`: Dispatches an outbound SMS via Android SIM slot 0 or 1.
- `httpsms_list_messages`: Searches and lists inbound/outbound SMS records.
- `httpsms_get_message`: Inspects single message status and carrier delivery reports.
- `httpsms_list_devices`: Retrieves battery %, SIM carriers, and signal level for all paired phones.
- `httpsms_create_webhook`: Registers HTTP webhooks with HMAC-SHA256 signatures.
- `httpsms_list_webhooks`: Lists registered webhook subscriptions.
- `httpsms_test_webhook`: Fires a live test ping to verify webhook connectivity.
- `httpsms_get_stats`: Reports gateway throughput and connected node counts.
- `httpsms_list_workflows`: Lists automated AI response workflows.
