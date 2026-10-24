#!/usr/bin/env python3
"""
listen_webhooks.py - Minimal HTTP Server for receiving & verifying HttpSMS Gateway webhooks.
Uses standard library only (no external dependencies).
"""

import hmac
import hashlib
import json
import sys
from http.server import HTTPServer, BaseHTTPRequestHandler

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 9000
SECRET = sys.argv[2] if len(sys.argv) > 2 else "whsec_test_secret_123456"

class WebhookHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length)

        sig_header = self.headers.get("X-Signature-SHA256", "")
        event_header = self.headers.get("X-Event-Type", "")

        computed_sig = hmac.new(
            SECRET.encode("utf-8"),
            body,
            hashlib.sha256
        ).hexdigest()

        is_valid = hmac.compare_digest(sig_header, computed_sig) if sig_header else False

        print(f"\n[RECEIVED WEBHOOK] Event: {event_header}")
        print(f"Signature Valid: {is_valid} (Header: {sig_header[:16]}... vs Computed: {computed_sig[:16]}...)")
        
        try:
            payload = json.loads(body.decode("utf-8"))
            print(json.dumps(payload, indent=2))
        except Exception:
            print(body.decode("utf-8", errors="replace"))

        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"status":"ok"}')

    def log_message(self, format, *args):
        # Suppress default server log lines to keep output clean
        return

def main():
    print(f"🚀 HttpSMS Webhook Listener running on http://0.0.0.0:{PORT}")
    print(f"🔑 Verifying HMAC with secret: '{SECRET}'")
    print("Press Ctrl+C to stop.\n")
    server = HTTPServer(("0.0.0.0", PORT), WebhookHandler)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nShutting down webhook listener.")
        server.server_close()

if __name__ == "__main__":
    main()
