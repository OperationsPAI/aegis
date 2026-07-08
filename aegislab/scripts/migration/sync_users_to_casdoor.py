#!/usr/bin/env python3
"""One-time migration: push aegis MySQL users into Casdoor.

For each aegis user row this script:
  1. Creates a corresponding Casdoor user (username, email, name,
     properties.aegis_user_id, is_active).
  2. Creates Casdoor roles that match aegis role names (idempotent).
  3. Assigns users to their matching roles.

Usage:
  python sync_users_to_casdoor.py \
      --casdoor-url http://casdoor:8000 \
      --casdoor-client-id <client-id> \
      --casdoor-client-secret <client-secret> \
      --casdoor-org built-in \
      --mysql-dsn 'user:pass@tcp(host:3306)/aegis'

Environment variables CASDOOR_URL, CASDOOR_CLIENT_ID, CASDOOR_CLIENT_SECRET
may be used instead of CLI flags.
"""

from __future__ import annotations

import argparse
import os
import sys
from typing import Any

import MySQLdb  # mysqlclient
import requests


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument(
        "--casdoor-url",
        default=os.environ.get("CASDOOR_URL", ""),
        help="Casdoor base URL (env: CASDOOR_URL)",
    )
    p.add_argument(
        "--casdoor-client-id",
        default=os.environ.get("CASDOOR_CLIENT_ID", ""),
        help="Casdoor application client ID (env: CASDOOR_CLIENT_ID)",
    )
    p.add_argument(
        "--casdoor-client-secret",
        default=os.environ.get("CASDOOR_CLIENT_SECRET", ""),
        help="Casdoor application client secret (env: CASDOOR_CLIENT_SECRET)",
    )
    p.add_argument(
        "--casdoor-org",
        default=os.environ.get("CASDOOR_ORG", "built-in"),
        help="Casdoor organization name (env: CASDOOR_ORG, default: built-in)",
    )
    p.add_argument(
        "--mysql-dsn",
        default=os.environ.get("AEGIS_MYSQL_DSN", ""),
        help="MySQL DSN: user:pass@tcp(host:port)/db (env: AEGIS_MYSQL_DSN)",
    )
    p.add_argument("--dry-run", action="store_true", help="Print actions without executing")
    return p.parse_args()


# ---------------------------------------------------------------------------
# MySQL helpers
# ---------------------------------------------------------------------------

def parse_mysql_dsn(dsn: str) -> dict[str, Any]:
    """Parse 'user:pass@tcp(host:port)/db' into MySQLdb.connect kwargs."""
    user_pass, rest = dsn.split("@tcp(", 1)
    host_port, db = rest.split(")/", 1)
    user, password = user_pass.split(":", 1)
    host, port_str = host_port.split(":", 1)
    return dict(host=host, port=int(port_str), user=user, passwd=password, db=db)


def load_users(conn_args: dict) -> list[dict]:
    conn = MySQLdb.connect(**conn_args, charset="utf8mb4")
    try:
        cur = conn.cursor()
        cur.execute(
            "SELECT u.id, u.username, u.email, u.full_name, u.is_active "
            "FROM users u WHERE u.status >= 0"
        )
        cols = [d[0] for d in cur.description]
        users = [dict(zip(cols, row)) for row in cur.fetchall()]

        for u in users:
            cur.execute(
                "SELECT r.name FROM roles r "
                "JOIN user_roles ur ON ur.role_id = r.id "
                "WHERE ur.user_id = %s AND r.status = 1",
                (u["id"],),
            )
            u["roles"] = [r[0] for r in cur.fetchall()]
        return users
    finally:
        conn.close()


# ---------------------------------------------------------------------------
# Casdoor API helpers
# ---------------------------------------------------------------------------

class CasdoorClient:
    def __init__(self, base_url: str, client_id: str, client_secret: str,
                 org: str, dry_run: bool = False):
        self.base = base_url.rstrip("/")
        self.org = org
        self.session = requests.Session()
        self.session.params = {
            "clientId": client_id,
            "clientSecret": client_secret,
        }
        self.session.headers["Content-Type"] = "application/json"
        self.dry_run = dry_run
        self._role_cache: set[str] = set()

    def _api(self, method: str, path: str, **kwargs) -> requests.Response:
        url = f"{self.base}{path}"
        resp = self.session.request(method, url, **kwargs)
        resp.raise_for_status()
        return resp

    def ensure_role(self, name: str) -> None:
        if name in self._role_cache:
            return
        resp = self._api("GET", f"/api/get-role", params={
            "id": f"{self.org}/{name}",
        })
        data = resp.json()
        if data.get("data") and data["data"].get("name") == name:
            self._role_cache.add(name)
            return
        if self.dry_run:
            print(f"  [dry-run] would create role: {name}")
            self._role_cache.add(name)
            return
        self._api("POST", "/api/add-role", json={
            "owner": self.org,
            "name": name,
            "displayName": name,
        })
        self._role_cache.add(name)
        print(f"  created role: {name}")

    def find_user(self, username: str) -> dict | None:
        resp = self._api("GET", f"/api/get-user", params={
            "id": f"{self.org}/{username}",
        })
        data = resp.json()
        if data.get("data") and data["data"].get("name") == username:
            return data["data"]
        return None

    def create_user(self, user: dict) -> dict | None:
        payload = {
            "owner": self.org,
            "name": user["username"],
            "displayName": user["full_name"] or user["username"],
            "email": user["email"],
            "type": "normal-user",
            "isAdmin": any(r in ("super_admin", "admin") for r in user["roles"]),
            "isForbidden": not bool(user["is_active"]),
            "properties": {"aegis_user_id": str(user["id"])},
        }
        if self.dry_run:
            print(f"  [dry-run] would create user: {user['username']} <{user['email']}>")
            return None
        resp = self._api("POST", "/api/add-user", json=payload)
        return resp.json().get("data")

    def assign_role(self, username: str, role_name: str) -> None:
        if self.dry_run:
            return
        self._api("POST", "/api/add-role-for-user", json={
            "owner": self.org,
            "user": username,
            "role": f"{self.org}/{role_name}",
        })


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    args = parse_args()
    if not args.casdoor_url:
        sys.exit("error: --casdoor-url or CASDOOR_URL required")
    if not args.casdoor_client_id:
        sys.exit("error: --casdoor-client-id or CASDOOR_CLIENT_ID required")
    if not args.casdoor_client_secret:
        sys.exit("error: --casdoor-client-secret or CASDOOR_CLIENT_SECRET required")
    if not args.mysql_dsn:
        sys.exit("error: --mysql-dsn or AEGIS_MYSQL_DSN required")

    conn_args = parse_mysql_dsn(args.mysql_dsn)
    users = load_users(conn_args)
    print(f"loaded {len(users)} users from MySQL")

    client = CasdoorClient(
        args.casdoor_url, args.casdoor_client_id, args.casdoor_client_secret,
        args.casdoor_org, dry_run=args.dry_run,
    )

    roles_seen: set[str] = set()
    for u in users:
        roles_seen.update(u["roles"])
    for role in sorted(roles_seen):
        client.ensure_role(role)

    created, skipped, failed = 0, 0, 0
    for u in users:
        existing = client.find_user(u["username"])
        if existing:
            skipped += 1
        else:
            try:
                client.create_user(u)
                created += 1
            except requests.HTTPError as exc:
                print(f"  FAILED to create user {u['username']}: {exc}", file=sys.stderr)
                failed += 1
                continue

        for role in u["roles"]:
            try:
                client.assign_role(u["username"], role)
            except requests.HTTPError:
                pass

    print(f"\nSummary: created={created} skipped={skipped} failed={failed}")


if __name__ == "__main__":
    main()
