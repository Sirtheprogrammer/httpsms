"""
HttpSMS Gateway Python SDK
Official Python client library for interacting with the HttpSMS Gateway server.
Zero-dependency: works with Python 3.8+ standard library.
"""

from __future__ import annotations

import hmac
import hashlib
import json
import os
import sys
import urllib.request
import urllib.error
from typing import Any, Dict, List, Optional, Union


class HttpSmsError(Exception):
    """Base exception for all HttpSMS errors."""
    def __init__(self, message: str, status_code: Optional[int] = None, response_body: Optional[str] = None):
        super().__init__(message)
        self.status_code = status_code
        self.response_body = response_body


class HttpSmsAuthError(HttpSmsError):
    """Raised when authentication fails or token lacks the required scope."""
    pass


class HttpSmsNotFoundError(HttpSmsError):
    """Raised when the requested resource (message, device, webhook) is not found."""
    pass


class _BaseAPI:
    def __init__(self, client: HttpSmsClient):
        self._client = client

    def _request(self, method: str, path: str, data: Optional[Dict[str, Any]] = None, params: Optional[Dict[str, Any]] = None) -> Any:
        return self._client._request(method, path, data=data, params=params)


class SmsAPI(_BaseAPI):
    """Operations on SMS messages (send, list, inspect)."""

    def send(
        self,
        recipient: str,
        body: str,
        sim_slot: int = 0,
        idempotency_key: Optional[str] = None,
        device_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        """
        Send an outbound SMS message via an Android gateway handset.

        :param recipient: International E.164 phone number (e.g. '+15551234567')
        :param body: Text content of the SMS message
        :param sim_slot: SIM slot index (0 for SIM 1, 1 for SIM 2; default: 0)
        :param idempotency_key: Unique client-side transaction key to prevent duplicate sends
        :param device_id: Optional UUID of a specific Android gateway handset
        :return: Created message details including status, parts_count, and UUID
        """
        payload: Dict[str, Any] = {
            "recipient": recipient,
            "body": body,
            "sim_slot": sim_slot,
        }
        if idempotency_key:
            payload["idempotency_key"] = idempotency_key
        if device_id:
            payload["device_id"] = device_id

        return self._request("POST", "/sms/send", data=payload)

    def list(
        self,
        direction: Optional[str] = None,
        search: Optional[str] = None,
        limit: int = 20,
        offset: int = 0,
    ) -> Dict[str, Any]:
        """
        List or search messages (inbound received SMS and outbound sent SMS).

        :param direction: Filter by 'inbound', 'outbound', or None for all
        :param search: Substring query matching sender, recipient, or body
        :param limit: Maximum messages to retrieve (1-100, default: 20)
        :param offset: Pagination offset (default: 0)
        :return: Dict containing 'data' (list of messages), 'limit', 'offset', 'total'
        """
        params: Dict[str, Any] = {"limit": limit, "offset": offset}
        if direction:
            params["direction"] = direction
        if search:
            params["search"] = search

        return self._request("GET", "/messages", params=params)

    def get(self, message_id: str) -> Dict[str, Any]:
        """
        Retrieve a single message and its delivery report by UUID.

        :param message_id: UUID of the message
        :return: Message record
        """
        return self._request("GET", f"/messages/{message_id}")


class DevicesAPI(_BaseAPI):
    """Operations on paired Android gateway handsets."""

    def list(self) -> List[Dict[str, Any]]:
        """
        List all paired Android gateway handsets, active SIM cards, signal levels, and battery %.

        :return: List of device objects
        """
        return self._request("GET", "/devices")


class WebhooksAPI(_BaseAPI):
    """Operations on webhooks and HMAC signature verification."""

    def create(
        self,
        url: str,
        events: List[str],
        secret: Optional[str] = None,
    ) -> Dict[str, Any]:
        """
        Register a webhook endpoint to receive real-time SMS events.

        :param url: HTTPS URL that receives webhook POST notifications
        :param events: List of subscribed events (e.g. ['sms.received', 'sms.delivered']) or ['*']
        :param secret: Signing secret for HMAC-SHA256 signature verification
        :return: Created webhook object
        """
        payload: Dict[str, Any] = {
            "url": url,
            "events": events,
        }
        if secret:
            payload["secret"] = secret

        return self._request("POST", "/webhooks", data=payload)

    def list(self) -> List[Dict[str, Any]]:
        """
        List all configured webhooks.

        :return: List of webhook objects
        """
        return self._request("GET", "/webhooks")

    def test(self, webhook_id: str) -> Dict[str, Any]:
        """
        Dispatch an instant verification test ping to a webhook.

        :param webhook_id: UUID of the webhook
        :return: Test result details
        """
        return self._request("POST", f"/webhooks/{webhook_id}/test")

    @staticmethod
    def verify_signature(payload_bytes: bytes, signature_header: str, secret: str) -> bool:
        """
        Verify the HMAC-SHA256 signature from the X-Signature-SHA256 header.

        :param payload_bytes: Raw bytes of the HTTP request body
        :param signature_header: Value of the 'X-Signature-SHA256' header
        :param secret: The webhook secret configured during registration
        :return: True if the signature matches, False otherwise
        """
        if not signature_header or not secret:
            return False

        computed = hmac.new(
            secret.encode("utf-8"),
            payload_bytes,
            hashlib.sha256
        ).hexdigest()

        return hmac.compare_digest(signature_header, computed)


class SystemAPI(_BaseAPI):
    """Operations on system telemetry and statistics."""

    def get_stats(self) -> Dict[str, Any]:
        """
        Retrieve real-time gateway throughput, online handsets, and message metrics.

        :return: System telemetry dictionary
        """
        return self._request("GET", "/system/stats")


class HttpSmsClient:
    """
    Main entry point for interacting with the HttpSMS Gateway.

    Usage:
        client = HttpSmsClient(api_key="your_api_token", base_url="http://localhost:8080")
        client.sms.send(recipient="+15551234567", body="Hello from Python!")
    """

    def __init__(
        self,
        api_key: Optional[str] = None,
        base_url: Optional[str] = None,
        timeout: int = 15,
    ):
        """
        Initialize the HttpSMS Client.

        :param api_key: Bearer token secret. If omitted, reads from HTTPSMS_API_KEY environment variable.
        :param base_url: Base URL of the gateway. Defaults to HTTPSMS_BASE_URL or 'http://localhost:8080'.
        :param timeout: Request timeout in seconds.
        """
        self.api_key = api_key or os.environ.get("HTTPSMS_API_KEY", "")
        self.base_url = (base_url or os.environ.get("HTTPSMS_BASE_URL", "http://localhost:8080")).rstrip("/")
        self.timeout = timeout

        self.sms = SmsAPI(self)
        self.devices = DevicesAPI(self)
        self.webhooks = WebhooksAPI(self)
        self.system = SystemAPI(self)

    def _request(
        self,
        method: str,
        path: str,
        data: Optional[Dict[str, Any]] = None,
        params: Optional[Dict[str, Any]] = None,
    ) -> Any:
        url = f"{self.base_url}/api/v1{path}"
        if params:
            query = "&".join(f"{k}={urllib.parse.quote(str(v))}" for k, v in params.items() if v is not None)
            if query:
                url = f"{url}?{query}"

        body_bytes = None
        if data is not None:
            body_bytes = json.dumps(data).encode("utf-8")

        req = urllib.request.Request(url, data=body_bytes, method=method)
        req.add_header("Authorization", f"Bearer {self.api_key}")
        req.add_header("Content-Type", "application/json")
        req.add_header("User-Agent", "HttpSMS-Python-SDK/1.0.0")

        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                resp_text = resp.read().decode("utf-8")
                if resp_text.strip():
                    return json.loads(resp_text)
                return {}
        except urllib.error.HTTPError as err:
            err_content = err.read().decode("utf-8", errors="replace")
            if err.code in (401, 403):
                raise HttpSmsAuthError(f"Authentication/Authorization error ({err.code}): {err_content}", err.code, err_content)
            elif err.code == 404:
                raise HttpSmsNotFoundError(f"Resource not found (404): {err_content}", err.code, err_content)
            else:
                raise HttpSmsError(f"Gateway error ({err.code}): {err_content}", err.code, err_content)
        except urllib.error.URLError as err:
            raise HttpSmsError(f"Connection failed to {self.base_url}: {err.reason}")

