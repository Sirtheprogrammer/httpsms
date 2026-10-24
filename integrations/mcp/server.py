#!/usr/bin/env python3
"""
HttpSMS Gateway Model Context Protocol (MCP) Server (Python Stdio)
Provides agentic AI tool access to send SMS, read incoming messages, query telemetry,
manage webhooks, and trigger workflows.
"""

import sys
import json
import os
import urllib.request
import urllib.error
from typing import Any, Dict, List

BASE_URL = os.environ.get("HTTPSMS_BASE_URL", "http://localhost:8080").rstrip("/")
API_KEY = os.environ.get("HTTPSMS_API_KEY", "htsms_admin_secret_change_me")

TOOLS: List[Dict[str, Any]] = [
    {
        "name": "httpsms_send_sms",
        "description": "Send an outbound SMS message through a connected Android handset gateway. Supports dual SIM slots, character counting, and idempotency deduplication.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "recipient": {
                    "type": "string",
                    "description": "Destination mobile phone number in E.164 format (e.g. '+15551234567')."
                },
                "body": {
                    "type": "string",
                    "description": "SMS message text payload. GSM 7-bit allows 160 chars/part; Unicode allows 70 chars/part."
                },
                "sim_slot": {
                    "type": "number",
                    "description": "SIM slot index (0 for SIM 1, 1 for SIM 2). Defaults to 0."
                },
                "device_id": {
                    "type": "string",
                    "description": "Optional target Android device UUID. If omitted, gateway automatically routes to any online device."
                },
                "idempotency_key": {
                    "type": "string",
                    "description": "Optional unique transaction key. Prevents sending duplicate messages on retries."
                }
            },
            "required": ["recipient", "body"]
        }
    },
    {
        "name": "httpsms_list_messages",
        "description": "List or search messages (inbound received SMS and outbound sent SMS) stored in the gateway database.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "direction": {
                    "type": "string",
                    "description": "Filter by direction: 'inbound', 'outbound', or leave empty for all.",
                    "enum": ["inbound", "outbound"]
                },
                "search": {
                    "type": "string",
                    "description": "Search text query matching sender, recipient, or message body content."
                },
                "limit": {
                    "type": "number",
                    "description": "Maximum number of messages to return (1-100, default: 20)."
                },
                "offset": {
                    "type": "number",
                    "description": "Pagination offset index (default: 0)."
                }
            }
        }
    },
    {
        "name": "httpsms_get_message",
        "description": "Retrieve detailed delivery telemetry for a specific message by its UUID.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "id": {
                    "type": "string",
                    "description": "The message UUID."
                }
            },
            "required": ["id"]
        }
    },
    {
        "name": "httpsms_list_devices",
        "description": "List all paired Android gateway handsets, their active SIM cards, battery %, charging state, signal level (dBm), and connection status.",
        "inputSchema": {
            "type": "object",
            "properties": {}
        }
    },
    {
        "name": "httpsms_create_webhook",
        "description": "Register a webhook endpoint on the gateway to receive real-time signed HTTP POST notifications for SMS events.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "url": {
                    "type": "string",
                    "description": "HTTPS destination webhook URL."
                },
                "events": {
                    "type": "array",
                    "description": "List of event types to subscribe to: 'sms.received', 'sms.sent', 'sms.delivered', 'sms.failed', 'device.heartbeat'. Use ['*'] for all.",
                    "items": {"type": "string"}
                },
                "secret": {
                    "type": "string",
                    "description": "Optional secret for HMAC-SHA256 signature calculation (sent in X-Signature-SHA256 header)."
                }
            },
            "required": ["url"]
        }
    },
    {
        "name": "httpsms_list_webhooks",
        "description": "List all configured webhooks and inspect their subscribed events and status.",
        "inputSchema": {
            "type": "object",
            "properties": {}
        }
    },
    {
        "name": "httpsms_test_webhook",
        "description": "Trigger an immediate test ping delivery to a registered webhook and return latency and HTTP response status.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "id": {
                    "type": "string",
                    "description": "Webhook UUID to test."
                }
            },
            "required": ["id"]
        }
    },
    {
        "name": "httpsms_get_stats",
        "description": "Retrieve high-level system metrics (online devices, total inbound SMS received, total outbound SMS sent, active webhooks, active workflows).",
        "inputSchema": {
            "type": "object",
            "properties": {}
        }
    },
    {
        "name": "httpsms_list_workflows",
        "description": "List all configured automated SMS responder workflows and their AI prompts.",
        "inputSchema": {
            "type": "object",
            "properties": {}
        }
    }
]

def make_request(method: str, path: str, body: Dict[str, Any] = None) -> str:
    url = f"{BASE_URL}{path}"
    headers = {
        "Authorization": f"Bearer {API_KEY}",
        "Content-Type": "application/json",
        "User-Agent": "HttpSMS-Python-MCP/1.0"
    }
    data = json.dumps(body).encode("utf-8") if body is not None else None
    req = urllib.request.Request(url, data=data, headers=headers, method=method)

    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return resp.read().decode("utf-8")
    except urllib.error.HTTPError as e:
        err_body = e.read().decode("utf-8")
        raise RuntimeError(f"Gateway HTTP {e.code}: {err_body}")
    except Exception as e:
        raise RuntimeError(f"Network error: {str(e)}")

def handle_call_tool(name: str, arguments: Dict[str, Any]) -> Dict[str, Any]:
    try:
        if name == "httpsms_send_sms":
            payload = {
                "recipient": arguments.get("recipient", ""),
                "body": arguments.get("body", ""),
                "sim_slot": int(arguments.get("sim_slot", 0)),
                "device_id": arguments.get("device_id", ""),
                "idempotency_key": arguments.get("idempotency_key", "")
            }
            res = make_request("POST", "/api/v1/sms/send", payload)
            return {"content": [{"type": "text", "text": res}]}

        elif name == "httpsms_list_messages":
            params = []
            if "direction" in arguments and arguments["direction"]:
                params.append(f"direction={arguments['direction']}")
            if "search" in arguments and arguments["search"]:
                params.append(f"search={urllib.parse.quote(arguments['search'])}")
            if "limit" in arguments:
                params.append(f"limit={int(arguments['limit'])}")
            if "offset" in arguments:
                params.append(f"offset={int(arguments['offset'])}")
            q = ("?" + "&".join(params)) if params else ""
            res = make_request("GET", f"/api/v1/messages{q}")
            return {"content": [{"type": "text", "text": res}]}

        elif name == "httpsms_get_message":
            msg_id = arguments.get("id", "")
            res = make_request("GET", f"/api/v1/messages/{msg_id}")
            return {"content": [{"type": "text", "text": res}]}

        elif name == "httpsms_list_devices":
            res = make_request("GET", "/api/v1/devices")
            return {"content": [{"type": "text", "text": res}]}

        elif name == "httpsms_create_webhook":
            payload = {
                "url": arguments.get("url", ""),
                "secret": arguments.get("secret", ""),
                "events": arguments.get("events", ["*"])
            }
            res = make_request("POST", "/api/v1/webhooks", payload)
            return {"content": [{"type": "text", "text": res}]}

        elif name == "httpsms_list_webhooks":
            res = make_request("GET", "/api/v1/webhooks")
            return {"content": [{"type": "text", "text": res}]}

        elif name == "httpsms_test_webhook":
            wh_id = arguments.get("id", "")
            res = make_request("POST", f"/api/v1/webhooks/{wh_id}/test")
            return {"content": [{"type": "text", "text": res}]}

        elif name == "httpsms_get_stats":
            res = make_request("GET", "/api/v1/system/stats")
            return {"content": [{"type": "text", "text": res}]}

        elif name == "httpsms_list_workflows":
            res = make_request("GET", "/api/v1/workflows")
            return {"content": [{"type": "text", "text": res}]}

        else:
            return {"content": [{"type": "text", "text": f"Unknown tool: {name}"}], "isError": True}

    except Exception as e:
        return {"content": [{"type": "text", "text": f"Error executing {name}: {str(e)}"}], "isError": True}

def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except json.JSONDecodeError:
            continue

        req_id = req.get("id")
        method = req.get("method")
        params = req.get("params", {})

        resp: Dict[str, Any] = {"jsonrpc": "2.0", "id": req_id}

        if method == "initialize":
            resp["result"] = {
                "protocolVersion": "2024-11-05",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "httpsms-python-mcp", "version": "1.0.0"}
            }
        elif method == "notifications/initialized":
            continue
        elif method == "tools/list":
            resp["result"] = {"tools": TOOLS}
        elif method == "tools/call":
            tool_name = params.get("name", "")
            tool_args = params.get("arguments", {})
            resp["result"] = handle_call_tool(tool_name, tool_args)
        else:
            resp["error"] = {"code": -32601, "message": f"Method '{method}' not found"}

        sys.stdout.write(json.dumps(resp) + "\n")
        sys.stdout.flush()

if __name__ == "__main__":
    main()

