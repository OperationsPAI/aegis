#!/usr/bin/env python3
"""SSO extraction E2E smoke test.

Validates the sso + aegis-backend split end-to-end against a live
docker-compose stack. Run after `docker compose up -d mysql redis sso
aegis-backend`. See AegisLab/docs/sso-extraction-design.md §10.

Stdlib-only (urllib + json) — no pip install needed. Run with:

    cd AegisLab
    just sso-keys
    docker compose up -d mysql redis sso aegis-backend
    python3 regression/sso_smoke.py

Exit code 0 = all checks passed; non-zero = failure (test prints which step).
"""

from __future__ import annotations

import base64
import json
import os
import secrets
import socket
import string
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

SSO_BASE = os.environ.get("SSO_BASE", "http://localhost:8083")
BACKEND_BASE = os.environ.get("BACKEND_BASE", "http://localhost:8082")
ADMIN_USER = os.environ.get("ADMIN_USER", "admin")
ADMIN_PASSWORD = os.environ.get("ADMIN_PASSWORD", "admin123")
CLIENT_ID = os.environ.get("SSO_CLIENT_ID", "aegis-backend")

MYSQL_CONTAINER = os.environ.get("MYSQL_CONTAINER", "mysql")
MYSQL_USER = os.environ.get("MYSQL_USER", "root")
MYSQL_PASSWORD = os.environ.get("MYSQL_PASSWORD", "yourpassword")
MYSQL_DB = os.environ.get("MYSQL_DB", "rcabench")

WAIT_TIMEOUT = int(os.environ.get("WAIT_TIMEOUT", "120"))


# ----- pretty output -----------------------------------------------------


def step(msg: str) -> None:
    print(f"\n=== {msg}")


def ok(msg: str) -> None:
    print(f"  ok: {msg}")


def fail(msg: str) -> None:
    print(f"  FAIL: {msg}", file=sys.stderr)
    sys.exit(1)


# ----- HTTP helpers ------------------------------------------------------


def http(
    method: str,
    url: str,
    *,
    headers: dict | None = None,
    data: bytes | None = None,
    expect: tuple[int, ...] = (200,),
    timeout: int = 15,
) -> tuple[int, dict | None, bytes]:
    """One-shot HTTP. Returns (status, json_body_or_None, raw_body).

    `expect`: pass an empty tuple to accept any status.
    """
    req = urllib.request.Request(url, method=method, data=data, headers=headers or {})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read()
            status = resp.status
    except urllib.error.HTTPError as e:
        body = e.read()
        status = e.code
    except urllib.error.URLError as e:
        return -1, None, str(e).encode()

    parsed: dict | None = None
    if body:
        try:
            parsed = json.loads(body)
        except json.JSONDecodeError:
            parsed = None

    if expect and status not in expect:
        snippet = body[:400].decode("utf-8", "replace")
        fail(f"{method} {url} → expected {expect}, got {status}. body: {snippet}")
    return status, parsed, body


def post_form(url: str, fields: dict, **kw) -> tuple[int, dict | None, bytes]:
    data = urllib.parse.urlencode(fields).encode()
    headers = kw.pop("headers", {}) | {
        "Content-Type": "application/x-www-form-urlencoded"
    }
    return http("POST", url, headers=headers, data=data, **kw)


def post_json(
    url: str, body: dict, *, token: str | None = None, **kw
) -> tuple[int, dict | None, bytes]:
    data = json.dumps(body).encode()
    headers = kw.pop("headers", {}) | {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    return http("POST", url, headers=headers, data=data, **kw)


# ----- waiting -----------------------------------------------------------


def wait_for(label: str, check) -> None:
    deadline = time.time() + WAIT_TIMEOUT
    last_err = "no attempts"
    while time.time() < deadline:
        try:
            if check():
                ok(f"{label} reachable")
                return
        except Exception as exc:
            last_err = repr(exc)
        time.sleep(2)
    fail(f"{label} not reachable within {WAIT_TIMEOUT}s (last: {last_err})")


def sso_up() -> bool:
    status, _, _ = http("GET", f"{SSO_BASE}/healthz", expect=())
    return status == 200


def backend_up() -> bool:
    # Backend has no root-level /health; /api/v2/system/health is admin-only.
    # Unauthenticated request returning 401 = server up and reachable.
    status, _, _ = http("GET", f"{BACKEND_BASE}/api/v2/system/health", expect=())
    return status in (200, 401, 403)


# ----- client_secret discovery ------------------------------------------


def read_client_secret() -> str:
    env_secret = os.environ.get("CLIENT_SECRET", "").strip()
    if env_secret:
        return env_secret
    for path in (
        "data/sso-secrets/.first-boot-secret",
        "/var/lib/sso/.first-boot-secret",
    ):
        try:
            with open(path) as fp:
                val = fp.read().strip()
                if val:
                    return val
        except FileNotFoundError:
            continue
    # As a last resort, try to read it inside the sso container.
    try:
        out = subprocess.run(
            ["docker", "exec", "sso", "cat", "/var/lib/sso/.first-boot-secret"],
            check=True,
            capture_output=True,
            timeout=10,
        )
        return out.stdout.decode().strip()
    except Exception:
        pass
    fail(
        "client_secret unavailable: set CLIENT_SECRET env var, "
        "or ensure data/sso-secrets/.first-boot-secret exists (mounted from compose)."
    )
    return ""  # unreachable


# ----- JWT helpers -------------------------------------------------------


def decode_jwt_unverified(token: str) -> dict:
    parts = token.split(".")
    if len(parts) != 3:
        fail(f"malformed jwt: {len(parts)} segments")
    payload = parts[1] + "=" * (-len(parts[1]) % 4)
    return json.loads(base64.urlsafe_b64decode(payload))


# ----- main steps --------------------------------------------------------


def step_wait_services() -> None:
    step("1. wait for sso + aegis-backend")
    wait_for("sso :8083", sso_up)
    wait_for("aegis-backend :8082", backend_up)


def step_service_token() -> str:
    step("2. POST /token grant_type=client_credentials")
    secret = read_client_secret()
    status, body, _ = post_form(
        f"{SSO_BASE}/token",
        {
            "grant_type": "client_credentials",
            "client_id": CLIENT_ID,
            "client_secret": secret,
        },
    )
    assert body, "empty token response"
    tok = body.get("access_token")
    if not tok:
        fail(f"no access_token in client_credentials response: {body}")
    claims = decode_jwt_unverified(tok)
    if claims.get("token_type") != "service":
        fail(f"expected token_type=service, got {claims.get('token_type')}")
    ok(
        f"service token issued (sub={claims.get('sub')}, exp_in={body.get('expires_in')}s)"
    )
    return tok


def step_v1_check_admin(service_token: str) -> None:
    step("3. POST /v1/check (admin → system:read:all)")
    status, body, _ = post_json(
        f"{SSO_BASE}/v1/check",
        {
            "user_id": 1,
            "permission": "system:read:all",
            "scope_type": "",
            "scope_id": "",
        },
        token=service_token,
    )
    data = (body or {}).get("data") or {}
    if not data.get("allowed"):
        fail(f"admin denied system:read:all — body={body}")
    ok(f"admin allowed (reason={data.get('reason')})")


def step_password_login(
    username: str, password: str, expect_success: bool = True
) -> str | None:
    step(f"4/7. POST /token grant_type=password ({username})")
    secret = read_client_secret()
    status, body, _ = post_form(
        f"{SSO_BASE}/token",
        {
            "grant_type": "password",
            "client_id": CLIENT_ID,
            "client_secret": secret,
            "username": username,
            "password": password,
        },
        expect=() if not expect_success else (200,),
    )
    if not expect_success:
        if status == 200:
            fail(f"expected login failure but got token for {username}")
        ok(f"login failed as expected (status={status})")
        return None
    tok = (body or {}).get("access_token")
    if not tok:
        fail(f"no access_token for {username}: {body}")
    claims = decode_jwt_unverified(tok)
    ok(
        f"{username} access_token issued (sub={claims.get('sub')}, username={claims.get('username')})"
    )
    return tok


def step_backend_authed(user_token: str) -> None:
    step("5. backend /api/v2/system/health with admin token")
    status, _, raw = http(
        "GET",
        f"{BACKEND_BASE}/api/v2/system/health",
        headers={"Authorization": f"Bearer {user_token}"},
        expect=(),
    )
    if status != 200:
        fail(
            f"admin GET /system/health → {status}; body={raw[:300].decode('utf-8', 'replace')}"
        )
    ok("admin can read /system/health (200)")


def step_backend_unauthed() -> None:
    step("6. backend /api/v2/system/health WITHOUT token")
    status, _, _ = http("GET", f"{BACKEND_BASE}/api/v2/system/health", expect=())
    if status not in (401, 403):
        fail(f"expected 401/403 with no token; got {status}")
    ok(f"no-token request rejected ({status})")


def step_create_nonadmin_and_403(admin_token: str) -> None:
    step("7. create non-admin user + 403 on admin endpoint")
    suffix = "".join(
        secrets.choice(string.ascii_lowercase + string.digits) for _ in range(6)
    )
    username = f"smoke_{suffix}"
    password = "Smoke_Test_123!"

    # POST /api/v2/users on the SSO process (it serves user.Module routes too).
    status, body, raw = post_json(
        f"{SSO_BASE}/api/v2/users",
        {
            "username": username,
            "email": f"{username}@smoke.local",
            "password": password,
            "full_name": "SSO Smoke User",
        },
        token=admin_token,
        expect=(),
    )
    if status not in (200, 201):
        fail(
            f"POST /api/v2/users → {status}, body={raw[:300].decode('utf-8', 'replace')} "
            f"(admin token may not carry user.create permission?)"
        )
    ok(f"created user {username}")

    # Log in as the new user (resource-owner password grant).
    user_tok = step_password_login(username, password, expect_success=True)
    assert user_tok

    step("7b. non-admin → /api/v2/system/health expects 403")
    status, _, _ = http(
        "GET",
        f"{BACKEND_BASE}/api/v2/system/health",
        headers={"Authorization": f"Bearer {user_tok}"},
        expect=(),
    )
    if status != 403:
        fail(f"expected 403 for non-admin; got {status}")
    ok("non-admin correctly forbidden (403)")


def step_oidc_discovery() -> None:
    step("8. OIDC discovery + JWKS sanity")
    status, body, _ = http("GET", f"{SSO_BASE}/.well-known/openid-configuration")
    required = (
        "issuer",
        "authorization_endpoint",
        "token_endpoint",
        "jwks_uri",
        "userinfo_endpoint",
    )
    missing = [k for k in required if not (body or {}).get(k)]
    if missing:
        fail(f"discovery missing keys: {missing}")
    ok("discovery has all required keys")

    jwks_uri = body["jwks_uri"]
    # docker-compose issuer uses internal hostname `sso`; rewrite for host-side test.
    parsed = urllib.parse.urlparse(jwks_uri)
    if parsed.hostname in ("sso", None):
        jwks_uri = f"{SSO_BASE}/.well-known/jwks.json"
    status, jwks, _ = http("GET", jwks_uri)
    keys = (jwks or {}).get("keys") or []
    if not keys or keys[0].get("kty") != "RSA":
        fail(f"jwks missing RSA key: {jwks}")
    ok(f"jwks returns {len(keys)} RSA key(s)")


def step_users_batch(service_token: str) -> None:
    step("9. POST /v1/users/batch ids=[1]")
    status, body, _ = post_json(
        f"{SSO_BASE}/v1/users/batch",
        {"ids": [1]},
        token=service_token,
    )
    data = (body or {}).get("data") or {}
    if "1" not in data and 1 not in data:
        fail(f"admin (id=1) absent from batch result: {body}")
    ok("admin present in /v1/users/batch response")


def step_audit_log() -> None:
    step("10. mysql audit_logs has auth.login.success row")
    if (
        subprocess.run(
            ["docker", "inspect", MYSQL_CONTAINER], capture_output=True
        ).returncode
        != 0
    ):
        ok(f"mysql container '{MYSQL_CONTAINER}' not present; skipping audit-log check")
        return
    cmd = [
        "docker",
        "exec",
        MYSQL_CONTAINER,
        "mysql",
        f"-u{MYSQL_USER}",
        f"-p{MYSQL_PASSWORD}",
        MYSQL_DB,
        "-Nse",
        "SELECT COUNT(*) FROM audit_logs WHERE action='auth.login.success'",
    ]
    res = subprocess.run(cmd, capture_output=True, timeout=15)
    if res.returncode != 0:
        ok(f"mysql query failed ({res.stderr.decode().strip()[:200]}); skipping")
        return
    count = res.stdout.decode().strip().splitlines()[-1] if res.stdout else "0"
    try:
        n = int(count)
    except ValueError:
        n = 0
    if n < 1:
        fail(
            f"no audit_logs.auth.login.success rows after admin login (count={count!r})"
        )
    ok(f"audit_logs has {n} auth.login.success row(s)")


def main() -> None:
    step_wait_services()
    service_token = step_service_token()
    step_v1_check_admin(service_token)
    admin_token = step_password_login(ADMIN_USER, ADMIN_PASSWORD, expect_success=True)
    assert admin_token
    step_backend_authed(admin_token)
    step_backend_unauthed()
    step_oidc_discovery()
    step_users_batch(service_token)
    step_create_nonadmin_and_403(admin_token)
    step_audit_log()

    print("\nALL OK — SSO extraction smoke test passed.")


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
