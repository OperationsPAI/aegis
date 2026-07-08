#!/usr/bin/env python3
"""One-time migration: push aegis MySQL users into Authentik.

For each aegis user row this script:
  1. Creates a corresponding Authentik user (username, email, name,
     attributes.aegis_user_id, is_active).
  2. Creates Authentik groups that match aegis role names (idempotent).
  3. Adds users to their matching groups.

Usage:
  python sync_users_to_authentik.py \
      --authentik-url https://auth.example.com \
      --authentik-token <admin-api-token> \
      --mysql-dsn 'user:pass@tcp(host:3306)/aegis'

Environment variables AUTHENTIK_URL and AUTHENTIK_TOKEN may be used
instead of CLI flags.
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
        "--authentik-url",
        default=os.environ.get("AUTHENTIK_URL", ""),
        help="Authentik base URL (env: AUTHENTIK_URL)",
    )
    p.add_argument(
        "--authentik-token",
        default=os.environ.get("AUTHENTIK_TOKEN", ""),
        help="Authentik admin API token (env: AUTHENTIK_TOKEN)",
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
# Authentik API helpers
# ---------------------------------------------------------------------------

class AuthentikClient:
    def __init__(self, base_url: str, token: str, dry_run: bool = False):
        self.base = base_url.rstrip("/")
        self.session = requests.Session()
        self.session.headers["Authorization"] = f"Bearer {token}"
        self.session.headers["Content-Type"] = "application/json"
        self.dry_run = dry_run
        self._group_cache: dict[str, str] = {}

    def _api(self, method: str, path: str, **kwargs) -> requests.Response:
        url = f"{self.base}/api/v3{path}"
        resp = self.session.request(method, url, **kwargs)
        resp.raise_for_status()
        return resp

    def ensure_group(self, name: str) -> str:
        if name in self._group_cache:
            return self._group_cache[name]
        resp = self._api("GET", "/core/groups/", params={"name": name})
        results = resp.json().get("results", [])
        if results:
            pk = results[0]["pk"]
            self._group_cache[name] = pk
            return pk
        if self.dry_run:
            print(f"  [dry-run] would create group: {name}")
            self._group_cache[name] = "dry-run"
            return "dry-run"
        resp = self._api("POST", "/core/groups/", json={"name": name})
        pk = resp.json()["pk"]
        self._group_cache[name] = pk
        print(f"  created group: {name} (pk={pk})")
        return pk

    def find_user_by_username(self, username: str) -> dict | None:
        resp = self._api("GET", "/core/users/", params={"username": username})
        results = resp.json().get("results", [])
        return results[0] if results else None

    def create_user(self, user: dict) -> dict | None:
        payload = {
            "username": user["username"],
            "email": user["email"],
            "name": user["full_name"] or user["username"],
            "is_active": bool(user["is_active"]),
            "attributes": {"aegis_user_id": user["id"]},
        }
        if self.dry_run:
            print(f"  [dry-run] would create user: {user['username']} <{user['email']}>")
            return None
        resp = self._api("POST", "/core/users/", json=payload)
        return resp.json()

    def add_user_to_group(self, group_pk: str, user_pk: int) -> None:
        if self.dry_run:
            return
        self._api("POST", f"/core/groups/{group_pk}/add_user/", json={"pk": user_pk})


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    args = parse_args()
    if not args.authentik_url:
        sys.exit("error: --authentik-url or AUTHENTIK_URL required")
    if not args.authentik_token:
        sys.exit("error: --authentik-token or AUTHENTIK_TOKEN required")
    if not args.mysql_dsn:
        sys.exit("error: --mysql-dsn or AEGIS_MYSQL_DSN required")

    conn_args = parse_mysql_dsn(args.mysql_dsn)
    users = load_users(conn_args)
    print(f"loaded {len(users)} users from MySQL")

    client = AuthentikClient(args.authentik_url, args.authentik_token, dry_run=args.dry_run)

    roles_seen: set[str] = set()
    for u in users:
        roles_seen.update(u["roles"])
    for role in sorted(roles_seen):
        client.ensure_group(role)

    created, skipped, failed = 0, 0, 0
    for u in users:
        existing = client.find_user_by_username(u["username"])
        if existing:
            skipped += 1
            ak_pk = existing["pk"]
        else:
            try:
                result = client.create_user(u)
                created += 1
                ak_pk = result["pk"] if result else None
            except requests.HTTPError as exc:
                print(f"  FAILED to create user {u['username']}: {exc}", file=sys.stderr)
                failed += 1
                continue

        if ak_pk is None:
            continue
        for role in u["roles"]:
            gpk = client.ensure_group(role)
            if gpk != "dry-run":
                try:
                    client.add_user_to_group(gpk, ak_pk)
                except requests.HTTPError:
                    pass

    print(f"\nSummary: created={created} skipped={skipped} failed={failed}")


if __name__ == "__main__":
    main()
