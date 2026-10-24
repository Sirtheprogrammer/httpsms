#!/usr/bin/env bash
#
# check_gateway.sh - Query gateway health, statistics, and connected Android handsets
# Usage: ./check_gateway.sh [-u BASE_URL] [-k API_KEY]
#

set -euo pipefail

BASE_URL="${HTTPSMS_BASE_URL:-http://localhost:8080}"
API_KEY="${HTTPSMS_API_KEY:-htsms_live_mcp_test_token_20261024_secret}"

while [[ $# -gt 0 ]]; do
  case $1 in
    -u|--url)
      BASE_URL="$2"
      shift 2
      ;;
    -k|--key)
      API_KEY="$2"
      shift 2
      ;;
    *)
      echo "Unknown flag $1"
      exit 1
      ;;
  esac
done

echo "================================================"
echo "         HTTPSMS GATEWAY TELEMETRY              "
echo "================================================"
echo "Target URL: $BASE_URL"
echo ""

echo "[1/3] System Statistics:"
curl -s -f -X GET "$BASE_URL/api/v1/system/stats" \
  -H "Authorization: Bearer $API_KEY" | python3 -m json.tool || echo "Failed to fetch system stats."

echo ""
echo "[2/3] Connected Gateway Devices:"
curl -s -f -X GET "$BASE_URL/api/v1/devices" \
  -H "Authorization: Bearer $API_KEY" | python3 -m json.tool || echo "Failed to fetch devices."

echo ""
echo "[3/3] Recent Message Traffic (Latest 5):"
curl -s -f -X GET "$BASE_URL/api/v1/messages?limit=5" \
  -H "Authorization: Bearer $API_KEY" | python3 -c '
import sys, json
try:
    data = json.load(sys.stdin)
    msgs = data.get("data", [])
    if not msgs:
        print("  No messages found.")
    for m in msgs:
        direction = "OUT ->" if m.get("direction") == "outbound" else "<- IN "
        status = m.get("status", "unknown")
        dest = m.get("recipient") if m.get("direction") == "outbound" else m.get("sender")
        body = m.get("body", "").replace("\n", " ")
        if len(body) > 40:
            body = body[:37] + "..."
        created = m.get("created_at", "")[:19]
        print(f"  [{created}] {direction} {dest:15} | Status: {status:10} | {body}")
except Exception as e:
    print(f"  Error parsing message data: {e}")
' || echo "Failed to fetch messages."
echo "================================================"
