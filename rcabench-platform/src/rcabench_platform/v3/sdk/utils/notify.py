"""Lightweight notification utilities with rate limiting.

Supports Feishu (Lark) webhook and email (SMTP).
All configuration is read from environment variables:

Feishu webhook:
    NOTIFY_FEISHU_WEBHOOK_URL   – full webhook URL (required to enable)

Email (SMTP):
    NOTIFY_SMTP_HOST            – SMTP server host (required to enable)
    NOTIFY_SMTP_PORT            – SMTP server port (default: 465 for SSL)
    NOTIFY_SMTP_USER            – login username
    NOTIFY_SMTP_PASSWORD        – login password
    NOTIFY_SMTP_FROM            – sender address (defaults to SMTP_USER)
    NOTIFY_SMTP_TO              – comma-separated recipient addresses
    NOTIFY_SMTP_SSL             – "true" to use SSL (default: true)

Rate limiting:
    NOTIFY_MIN_INTERVAL         – minimum seconds between messages (default: 60)

Usage:
    from rcabench_platform.v3.sdk.utils.notify import Notifier

    notifier = Notifier()                        # reads env vars
    notifier.info("Eval finished", "ok=10 ...")  # completion
    notifier.error("Sample failed", "timeout")   # error (rate-limited)

    # Or use the module-level convenience functions:
    from rcabench_platform.v3.sdk.utils.notify import notify_info, notify_error
    notify_info("Done", "ok=10")
    notify_error("Failed", "timeout on sample 3")
"""

from __future__ import annotations

import logging
import os
import smtplib
import threading
import time
from email.mime.text import MIMEText
from typing import Sequence

import requests

logger = logging.getLogger(__name__)

_LEVEL_EMOJI = {
    "info": "✅",
    "error": "❌",
    "warning": "⚠️",
}

_LEVEL_COLOR = {
    "info": "green",
    "error": "red",
    "warning": "orange",
}


# ── Feishu webhook ────────────────────────────────────────────────────


def send_feishu(
    title: str,
    content: str,
    *,
    level: str = "info",
    webhook_url: str | None = None,
) -> bool:
    """Send a Feishu (Lark) webhook message.

    Returns True on success, False on failure (logged, never raises).
    """
    url = webhook_url or os.getenv("NOTIFY_FEISHU_WEBHOOK_URL")
    if not url:
        return False

    emoji = _LEVEL_EMOJI.get(level, "")
    color = _LEVEL_COLOR.get(level, "blue")

    payload = {
        "msg_type": "interactive",
        "card": {
            "header": {
                "title": {"tag": "plain_text", "content": f"{emoji} {title}"},
                "template": color,
            },
            "elements": [
                {
                    "tag": "markdown",
                    "content": content,
                },
            ],
        },
    }

    try:
        resp = requests.post(url, json=payload, timeout=10)
        resp.raise_for_status()
        data = resp.json()
        if data.get("code", 0) != 0:
            logger.warning("Feishu API error: %s", data)
            return False
        return True
    except Exception:
        logger.exception("Failed to send Feishu notification")
        return False


# ── Email (SMTP) ──────────────────────────────────────────────────────


def send_email(
    title: str,
    content: str,
    *,
    to: str | Sequence[str] | None = None,
) -> bool:
    """Send an email notification via SMTP.

    Returns True on success, False on failure (logged, never raises).
    """
    host = os.getenv("NOTIFY_SMTP_HOST")
    if not host:
        return False

    port = int(os.getenv("NOTIFY_SMTP_PORT", "465"))
    user = os.getenv("NOTIFY_SMTP_USER", "")
    password = os.getenv("NOTIFY_SMTP_PASSWORD", "")
    sender = os.getenv("NOTIFY_SMTP_FROM", user)
    use_ssl = os.getenv("NOTIFY_SMTP_SSL", "true").lower() in ("true", "1")

    # Resolve recipients
    if to is None:
        raw = os.getenv("NOTIFY_SMTP_TO", "")
        recipients = [addr.strip() for addr in raw.split(",") if addr.strip()]
    elif isinstance(to, str):
        recipients = [to]
    else:
        recipients = list(to)

    if not recipients:
        logger.warning("No email recipients configured")
        return False

    msg = MIMEText(content, "plain", "utf-8")
    msg["Subject"] = title
    msg["From"] = sender
    msg["To"] = ", ".join(recipients)

    try:
        if use_ssl:
            with smtplib.SMTP_SSL(host, port, timeout=15) as srv:
                if user and password:
                    srv.login(user, password)
                srv.sendmail(sender, recipients, msg.as_string())
        else:
            with smtplib.SMTP(host, port, timeout=15) as srv:
                srv.ehlo()
                srv.starttls()
                srv.ehlo()
                if user and password:
                    srv.login(user, password)
                srv.sendmail(sender, recipients, msg.as_string())
        return True
    except Exception:
        logger.exception("Failed to send email notification")
        return False


# ── Rate limiter ──────────────────────────────────────────────────────


class _RateLimiter:
    """Per-key rate limiter: allows at most one send per `min_interval` seconds."""

    def __init__(self, min_interval: float):
        self._min_interval = min_interval
        self._last_sent: dict[str, float] = {}
        self._suppressed: dict[str, int] = {}
        self._lock = threading.Lock()

    def try_acquire(self, key: str) -> tuple[bool, int]:
        """Try to acquire permission to send.

        Returns (allowed, suppressed_count).
        suppressed_count is the number of messages that were dropped since last send.
        """
        now = time.monotonic()
        with self._lock:
            last = self._last_sent.get(key, 0.0)
            if now - last >= self._min_interval:
                suppressed = self._suppressed.pop(key, 0)
                self._last_sent[key] = now
                return True, suppressed
            else:
                self._suppressed[key] = self._suppressed.get(key, 0) + 1
                return False, 0

    def flush(self, key: str) -> int:
        """Return and clear the suppressed count for a key."""
        with self._lock:
            return self._suppressed.pop(key, 0)


# ── Notifier ──────────────────────────────────────────────────────────


class Notifier:
    """Notification sender with rate limiting.

    Rate limiting applies per level (info/error/warning). Completion
    messages (info) are always sent immediately. Error/warning messages
    are throttled so that at most one is sent per `min_interval`.

    When a throttled message finally goes through, the content is
    appended with how many messages were suppressed in between.
    """

    def __init__(self, *, min_interval: float | None = None):
        if min_interval is None:
            min_interval = float(os.getenv("NOTIFY_MIN_INTERVAL", "60"))
        self._limiter = _RateLimiter(min_interval)

    def _send(self, title: str, content: str, level: str) -> dict[str, bool]:
        results: dict[str, bool] = {}

        if os.getenv("NOTIFY_FEISHU_WEBHOOK_URL"):
            results["feishu"] = send_feishu(title, content, level=level)

        if os.getenv("NOTIFY_SMTP_HOST"):
            results["email"] = send_email(title, f"[{level.upper()}] {title}\n\n{content}")

        if not results:
            logger.debug("No notification channels configured, skipping")

        return results

    def info(self, title: str, content: str) -> dict[str, bool]:
        """Send a completion/info notification. Always sent (no throttle)."""
        return self._send(title, content, "info")

    def error(self, title: str, content: str) -> dict[str, bool]:
        """Send an error notification (rate-limited)."""
        return self._throttled_send(title, content, "error")

    def warning(self, title: str, content: str) -> dict[str, bool]:
        """Send a warning notification (rate-limited)."""
        return self._throttled_send(title, content, "warning")

    def _throttled_send(self, title: str, content: str, level: str) -> dict[str, bool]:
        allowed, suppressed = self._limiter.try_acquire(level)
        if not allowed:
            logger.debug("Notification suppressed (rate limit): [%s] %s", level, title)
            return {}

        if suppressed > 0:
            content += f"\n\n_(+{suppressed} similar messages suppressed)_"

        return self._send(title, content, level)

    def flush(self) -> dict[str, bool]:
        """Send a summary of any remaining suppressed messages.

        Call this at the end of a run to make sure nothing is silently lost.
        """
        results: dict[str, bool] = {}
        for level in ("error", "warning"):
            count = self._limiter.flush(level)
            if count > 0:
                results.update(
                    self._send(
                        f"Suppressed {level}s",
                        f"{count} {level} message(s) were suppressed by rate limiting.",
                        level,
                    )
                )
        return results


# ── Module-level convenience ──────────────────────────────────────────

_default_notifier: Notifier | None = None
_default_lock = threading.Lock()


def _get_default() -> Notifier:
    global _default_notifier
    if _default_notifier is None:
        with _default_lock:
            if _default_notifier is None:
                _default_notifier = Notifier()
    return _default_notifier


def notify_info(title: str, content: str) -> dict[str, bool]:
    """Send a completion/info notification (no throttle)."""
    return _get_default().info(title, content)


def notify_error(title: str, content: str) -> dict[str, bool]:
    """Send an error notification (rate-limited)."""
    return _get_default().error(title, content)


def notify_warning(title: str, content: str) -> dict[str, bool]:
    """Send a warning notification (rate-limited)."""
    return _get_default().warning(title, content)
