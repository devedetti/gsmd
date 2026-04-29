# gsmd

HTTP daemon that wraps a ZTE 4G CPE router and exposes a single endpoint to
send SMS over its web API.

- `POST /sms` — bearer-token auth, rate-limited, 64 KB body cap.
- `GET /healthz` — unauthenticated liveness probe.

Runs as an unprivileged process on Alpine Linux (OpenRC) on the same LAN as
the router. No reception/inbox features — write-only.

---

## Build

Static x86-64 binary, no CGO:

```sh
make build
```

Output: `./gsmd` (~6 MB, statically linked). Cross-compiles for `linux/amd64`
from any host; override with `GOOS=... GOARCH=... make build`.

---

## Configure

The daemon reads a single JSON file. Default path: `/etc/gsmd.conf`. Override
with `-config /custom/path`.

### Generate tokens

One token per caller (Home Assistant, Grafana, manual scripts, etc.). Each
should be 32 random bytes hex-encoded:

```sh
openssl rand -hex 32
```

Run it once per caller. Each output is a 64-character hex string — paste it
into the `tokens` map of the config.

### Write the config

Copy [`gsmd.conf.example`](gsmd.conf.example) and edit:

```sh
sudo install -m 0640 -o root -g gsmd gsmd.conf.example /etc/gsmd.conf
sudo $EDITOR /etc/gsmd.conf
```

Schema:

```json
{
  "cpe_host": "192.168.2.4",
  "cpe_user": "admin",
  "cpe_pass": "<router-admin-password>",
  "listen": ":8080",
  "rate_limit_per_min": 30,
  "rate_limit_burst": 5,
  "tokens": {
    "ha":      "<openssl-rand-hex-32-output>",
    "grafana": "<another-openssl-rand-hex-32-output>",
    "manual":  "<another-openssl-rand-hex-32-output>"
  }
}
```

| Field | Required | Notes |
|---|---|---|
| `cpe_host` | yes | Router LAN IP. |
| `cpe_user` / `cpe_pass` | yes | Router admin credentials. |
| `listen` | yes | `host:port` for the HTTP listener. |
| `rate_limit_per_min` | no | Defaults to `30`. Token-bucket refill rate. |
| `rate_limit_burst` | no | Defaults to `5`. Max burst above the steady rate. |
| `tokens` | yes (≥1) | Caller-name → bearer token. The name appears in logs. |

Permissions: `chmod 0640 /etc/gsmd.conf`, owned by `root:gsmd` so only the
daemon can read it.

---

## Install on Alpine (OpenRC)

```sh
# 1. Service user
sudo adduser -S -D -H -s /sbin/nologin gsmd

# 2. Binary
sudo install -m 0755 gsmd /usr/local/bin/gsmd

# 3. Config (see "Write the config" above)

# 4. Init script
sudo install -m 0755 init.d/gsmd /etc/init.d/gsmd

# 5. Enable + start
sudo rc-update add gsmd default
sudo rc-service gsmd start
sudo rc-service gsmd status
```

Logs land in `/var/log/gsmd/gsmd.log` (stdout) and `/var/log/gsmd/gsmd.err`
(stderr). The init script creates the directory on first start.

To stop / restart / disable:

```sh
sudo rc-service gsmd stop
sudo rc-service gsmd restart
sudo rc-update del gsmd default
```

---

## Use it

From any caller on the LAN:

```sh
curl -X POST http://gsm-host:8080/sms \
  -H "Authorization: Bearer $GSM_TOKEN_HA" \
  -H "Content-Type: application/json" \
  -d '{"number":"+5491100000000","message":"hello from ha"}'
```

Responses:

| Status | Meaning |
|---|---|
| `200` | SMS accepted by the router. |
| `400` | Malformed JSON or missing `number`/`message`. |
| `401` | Missing, malformed, or unknown bearer token. |
| `413` | Body larger than 64 KB. |
| `429` | Rate limit exceeded. |
| `502` | Router rejected the request or is unreachable. |

Healthcheck (no auth):

```sh
curl http://gsm-host:8080/healthz
# → ok
```

---

## Operations

### Rotate a token

1. Generate a new value: `openssl rand -hex 32`.
2. Replace the entry in `/etc/gsmd.conf`.
3. `sudo rc-service gsmd restart`.
4. Update the caller's secret store with the new value.

Other tokens stay valid; only the rotated caller is affected.

### Recovering from a lockout-protection trip

If `cpe_pass` is wrong, the daemon counts consecutive auth failures and refuses
to attempt further logins after **3** failures. This stops the router from
locking the account at its own threshold (5 failures). Symptoms:

- Logs: `login failed (auth fail N/3): ...`, then `login disabled after 3 ...`.
- `POST /sms` returns 502 with the `login disabled` message.

Fix: correct `cpe_pass` in `/etc/gsmd.conf` and `rc-service gsmd restart`.
The counter resets on every successful login.

### Tuning the rate limit

`rate_limit_per_min` is the steady-state ceiling; `rate_limit_burst` is how
many requests can fire back-to-back before throttling kicks in. Defaults
(30/min, burst 5) are reasonable for alerting use cases. To raise: edit the
config and restart.

---

## Project layout

```
.
├── main.go                       HTTP server, flag parsing, middleware chain
├── internal/
│   ├── config/                   /etc/gsmd.conf loader + validation
│   ├── httpx/                    Auth, RateLimit, MaxBody middleware
│   └── zte/                      ZTE CPE web-API client (login, SendSMS, ...)
├── init.d/gsmd                   OpenRC service script
├── gsmd.conf.example             Config template (no real secrets)
├── Makefile                      build / test / vet
└── legacy/                       Original Python prototype (reference only)
```

The `internal/zte` package knows nothing about HTTP servers, configs, or
rate limits — it speaks the router's protocol and nothing else. New
integrations (MQTT, another HTTP shape, a CLI) should reuse this package
rather than duplicate the protocol logic.
