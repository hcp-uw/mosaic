# Mosaic Auth Server

The auth server is a small HTTP server that handles account registration, login, and public key lookups. It is the root of trust for identity in the Mosaic network — every node's claimed identity is ultimately verified against this server.

---

## Why It Exists

Every user in Mosaic has a manifest section in the network manifest, signed with their ECDSA private key. Any peer can verify that signature, which proves the bytes weren't tampered with. But that alone doesn't prove the person who signed it actually owns the `UserID` they claimed.

Without the auth server, a malicious node could write a manifest entry claiming any `UserID` they want, sign it with their own keypair, and pass signature verification. The auth server closes this gap by being the canonical authority on which public key belongs to which account. Peers can call it to confirm a manifest entry's claimed identity is real.

---

## How Keys Work

When you run `mos login key <username> <key>`, the daemon derives a deterministic ECDSA P-256 keypair from your login key using HKDF-SHA256:

```
loginKey → HKDF(SHA-256, salt="mosaic-user-key") → 32-byte seed → ECDSA P-256 keypair
```

Because this derivation is deterministic, the exact same keypair is produced on every machine you log into with the same login key. The auth server records this public key the first time you log in on any device. From that point on, any peer in the network can call `GET /auth/pubkey/{userID}` to confirm that your manifest entry's embedded public key matches what the auth server has on file.

---

## JWT Security Model (ES256)

The auth server issues **ES256 JWTs** (ECDSA P-256 + SHA-256). This is asymmetric: the server signs tokens with its **private key** and publishes the corresponding **public key** at `GET /auth/pubkey/server`.

When the daemon logs in:
1. It calls `POST /auth/login` and receives a signed JWT.
2. It calls `GET /auth/pubkey/server` to fetch the server's ECDSA public key.
3. It verifies the JWT's ES256 signature locally against that public key.
4. The public key is cached in memory — all subsequent token verifications are local with no network calls.

This means:
- **No shared secret.** There is no HMAC key that both parties must keep secure. The private key never leaves the auth server.
- **Daemon verifies independently.** After the initial key fetch, the daemon can verify any token offline. Even if the auth server goes down temporarily, existing sessions continue to work.
- **Forgery is cryptographically hard.** An attacker who intercepts a token cannot modify it — the ES256 signature covers the header and payload.
- **The STUN server also verifies tokens** by calling `POST /auth/verify` on the auth server before allowing a client to register.

---

## Files

```
AuthServer/
├── main.go                    — HTTP server setup, graceful shutdown, file paths
├── db.go                      — SQLite database: accounts + nodes tables, all read/write functions
├── token.go                   — ES256 JWT issuance and verification (ECDSA P-256, 30-day expiry)
├── handlers.go                — HTTP handlers for all endpoints
├── mosaic-auth                — compiled binary (not committed, rebuild with go build)
├── mosaic-auth.db             — SQLite database (not committed, auto-created on first run)
└── .mosaic-auth-signing.pem   — ECDSA P-256 private key (not committed, auto-generated on first run)
```

### mosaic-auth.db

The SQLite database. Created automatically in the same directory as the binary on first run. Contains two tables:

```sql
accounts (
    account_id  INTEGER PRIMARY KEY AUTOINCREMENT,
    username    TEXT    NOT NULL UNIQUE,
    salt_hex    TEXT    NOT NULL,   -- random 32-byte salt for key hashing
    key_hash    TEXT    NOT NULL,   -- hex(SHA-256(salt || loginKey))
    public_key  TEXT    DEFAULT ''  -- hex PKIX DER of ECDSA P-256 public key (set on first login)
)

nodes (
    node_id     INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id  INTEGER NOT NULL,
    node_number INTEGER NOT NULL,
    UNIQUE(account_id, node_number)
)
```

### .mosaic-auth-signing.pem

An ECDSA P-256 private key generated on first run and saved as a PEM file (`EC PRIVATE KEY` block). Used to sign all JWTs.

- If someone gets a copy they can forge valid tokens for any account
- If you delete it, the server regenerates a new keypair on next start and all existing sessions are invalidated (everyone has to log in again, because the old signatures won't verify against the new public key)
- Never commit this file — it is in `.gitignore`
- The corresponding public key is served at `GET /auth/pubkey/server` so daemons can verify tokens locally

---

## Endpoints

### `POST /auth/register`

Creates a new account. The login key is salted and hashed — the plaintext is never stored.

**Request:**
```json
{
  "username": "alice",
  "loginKey":  "my-secret-key"
}
```

**Response (success):**
```json
{
  "success":   true,
  "accountID": "000000001",
  "details":   "account created"
}
```

**Response (username taken):**
```json
{
  "success": false,
  "details": "username already taken"
}
```

---

### `POST /auth/login`

Validates the login key, assigns or reuses a node number for this device, records the user's derived public key (on first login), and returns a signed ES256 JWT. The daemon verifies this token locally against the server's cached public key before writing it to `~/.mosaic-session`.

**Request:**
```json
{
  "username":   "alice",
  "loginKey":   "my-secret-key",
  "publicKey":  "3059301306...",  // hex PKIX DER of the HKDF-derived ECDSA key
  "nodeNumber": 0                 // 0 = new device, non-zero = re-login on known device
}
```

**Response (success):**
```json
{
  "success":    true,
  "token":      "eyJhbGci...",
  "nodeNumber": 1,
  "details":    "logged in on node-1"
}
```

The token is an ES256 JWT. Its payload contains:

```json
{
  "accountID":  1,
  "username":   "alice",
  "nodeNumber": 1,
  "publicKey":  "3059301306...",
  "iat":        1718000000,
  "exp":        1720592000
}
```

Tokens expire after 30 days.

**Node number behaviour:**
- `nodeNumber: 0` — first login on this device. The server creates a new node (node-1, node-2, ...) and returns its number. The daemon saves this for future re-logins.
- `nodeNumber: N` — re-login on a known device. The server reuses that node if it exists, or creates a fresh one if the DB was reset.

**Response (wrong key):**
```json
{
  "success": false,
  "details": "invalid username or login key"
}
```

The same error is returned for both unknown username and wrong key to avoid leaking which usernames exist.

---

### `POST /auth/verify`

Called by the STUN server to validate a client JWT before allowing it to register. Returns the token's claims on success.

**Request:**
```json
{
  "token": "eyJhbGci..."
}
```

**Response (valid):**
```json
{
  "success":    true,
  "accountID":  1,
  "username":   "alice",
  "nodeNumber": 1,
  "details":    "ok"
}
```

**Response (invalid/expired):**
```json
{
  "success": false,
  "details": "invalid token: token expired"
}
```

---

### `GET /auth/pubkey/server`

Returns the server's ECDSA P-256 public key as a PEM string. Daemons call this once at login time and cache it in memory for all future local JWT verifications.

**Response:**
```json
{
  "success":   true,
  "publicKey": "-----BEGIN PUBLIC KEY-----\nMFkwEwYH...\n-----END PUBLIC KEY-----\n",
  "details":   "ok"
}
```

---

### `GET /auth/pubkey/{userID}`

Returns the canonical public key for an account ID. Peers call this during manifest merge to confirm that a `UserNetworkEntry`'s embedded public key actually belongs to the claimed account ID.

**Response (success):**
```json
{
  "success":   true,
  "accountID": "000000001",
  "username":  "alice",
  "publicKey": "3059301306...",
  "details":   "ok"
}
```

**Response (not found):**
```json
{
  "success": false,
  "details": "account not found"
}
```

---

## Running the Server

### Build the binary (required once, and after any source changes)

```bash
go build -o AuthServer/mosaic-auth ./AuthServer/
```

### Start (foreground — Ctrl+C to stop)

```bash
./AuthServer/mosaic-auth
```

### Start in the background

```bash
./AuthServer/mosaic-auth > AuthServer/auth.log 2>&1 &
echo $! > AuthServer/auth.pid
```

Stop it later:
```bash
kill $(cat AuthServer/auth.pid)
```

### Run without building (development)

```bash
go run ./AuthServer/
```

Recompiles every time, fine for development.

### Environment variables

| Variable       | Default                     | Description                                    |
|----------------|-----------------------------|------------------------------------------------|
| `AUTH_PORT`    | `8081`                      | Port to listen on                              |
| `AUTH_DB`      | `<binary dir>/mosaic-auth.db` | Path to the SQLite database                  |
| `AUTH_SERVER`  | `http://localhost:8081`     | Used by the daemon to reach the server         |
| `RATE_LIMIT`   | (enabled)                   | Set to `false` to disable rate limiting (dev)  |

### Check if it's running

```bash
curl http://localhost:8081/auth/pubkey/server
```

---

## Integration with the Daemon

The daemon talks to the auth server in three places:

**`createAccount.go`** — calls `POST /auth/register` when the user runs `mos create account <username> <key>`

**`loginWithKey.go`** — calls `POST /auth/login`, then:
1. Calls `GET /auth/pubkey/server` to fetch the server's ECDSA public key (cached in memory)
2. Verifies the ES256 JWT signature locally against that public key
3. Writes the verified claims to `~/.mosaic-session`

After login, identity helpers read from the session:
- `GetAccountID()` → `session.AccountID`
- `GetUsername()` → `session.Username`
- `GetNodeNumber()` → `session.NodeNumber`

**`joinNetwork.go`** — passes the session JWT to the P2P client, which sends it to the STUN server during registration. The STUN server calls `POST /auth/verify` to validate it.

The daemon locates the auth server via the `AUTH_SERVER` environment variable (default `http://localhost:8081`). In production this would point to a deployed server rather than localhost.

---

## Security Notes

- **Login keys are never stored in plaintext.** The server stores `hex(SHA-256(salt || loginKey))`. A database breach doesn't expose keys.
- **The same error is returned for unknown username and wrong key** to prevent username enumeration.
- **JWT signatures use ES256 (ECDSA P-256 + SHA-256).** This is asymmetric — the private key never leaves the server. Daemons verify tokens locally using the published public key, with no shared secret to protect.
- **Daemon verifies JWTs locally.** After fetching the server public key once at login, all subsequent verifications happen in memory with no network calls. Tokens cannot be forged without the server's private key.
- **The STUN server validates JWTs** before pairing any two clients. Unauthenticated nodes cannot use the hole-punching service.
- **The login key hash uses SHA-256**, which is fast and therefore easier to brute-force than bcrypt or Argon2. This is fine for development but should be upgraded before any real deployment.
- **Transport is HTTP, not HTTPS.** In production the auth server should run behind TLS. Without it, login keys could be intercepted in transit. The JWT mechanism itself is secure regardless — even if a token is intercepted, it cannot be modified (the signature would break) and cannot be used to derive the private signing key.
- **The public key is recorded on first login and never changes.** Because the keypair is derived deterministically from the login key, logging in from a new device produces the same public key — the server confirms this and the record stays consistent.
