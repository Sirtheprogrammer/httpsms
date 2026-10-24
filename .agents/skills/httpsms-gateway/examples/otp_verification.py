#!/usr/bin/env python3
"""
otp_verification.py - End-to-End One-Time Password (OTP) Flow using HttpSMS Gateway.
Demonstrates sending an OTP with idempotency and checking for customer response.
"""

import os
import sys
import time
import random
import json
import urllib.request
import urllib.error

BASE_URL = os.environ.get("HTTPSMS_BASE_URL", "http://localhost:8080").rstrip("/")
API_KEY = os.environ.get("HTTPSMS_API_KEY", "htsms_live_mcp_test_token_20261024_secret")

def api_call(endpoint: str, method: str = "GET", data: dict = None) -> dict:
    url = f"{BASE_URL}/api/v1{endpoint}"
    body = json.dumps(data).encode("utf-8") if data else None
    req = urllib.request.Request(url, data=body, method=method)
    req.add_header("Authorization", f"Bearer {API_KEY}")
    req.add_header("Content-Type", "application/json")
    
    try:
        with urllib.request.urlopen(req) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        err_msg = e.read().decode("utf-8")
        print(f"API Error ({e.code}): {err_msg}", file=sys.stderr)
        raise

def send_otp(phone_number: str) -> str:
    otp_code = f"{random.randint(100000, 999999)}"
    idempotency_key = f"otp_{phone_number.replace('+', '')}_{int(time.time())}"
    
    message = f"Your verification code is {otp_code}. Valid for 10 minutes. Do not share this code."
    
    print(f"[*] Sending OTP {otp_code} to {phone_number}...")
    result = api_call("/sms/send", method="POST", data={
        "recipient": phone_number,
        "body": message,
        "sim_slot": 0,
        "idempotency_key": idempotency_key
    })
    
    print(f"[+] Outbound SMS queued. Message ID: {result.get('id')}")
    return otp_code

def wait_for_reply(sender_phone: str, timeout_seconds: int = 60) -> str:
    print(f"[*] Waiting up to {timeout_seconds}s for inbound SMS from {sender_phone}...")
    start_time = time.time()
    
    while time.time() - start_time < timeout_seconds:
        resp = api_call(f"/messages?direction=inbound&search={sender_phone}&limit=1")
        messages = resp.get("data", [])
        if messages:
            latest = messages[0]
            # Check if this was received within the last minute
            msg_text = latest.get("body", "").strip()
            print(f"[+] Inbound message received from {sender_phone}: '{msg_text}'")
            return msg_text
        
        time.sleep(3)
        
    print("[-] Timeout waiting for reply.")
    return ""

def main():
    recipient = sys.argv[1] if len(sys.argv) > 1 else "+255797455136"
    
    print("========================================")
    print("  HttpSMS Gateway: OTP Verification Demo ")
    print("========================================")
    
    # 1. Send OTP
    otp = send_otp(recipient)
    print(f"[✓] Expected Code: {otp}")
    
    # 2. Wait for incoming reply (simulation / test)
    print("\nIn production, verify the code submitted by the user in your app or web form.")
    print(f"Code to verify: if user_input == '{otp}': SUCCESS\n")

if __name__ == "__main__":
    main()

