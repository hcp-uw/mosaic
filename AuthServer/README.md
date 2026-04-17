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

## Files

```
AuthServer/
├── main.go        — HTTP server setup, graceful shutdown, file paths
├── db.go          — SQLite database: accounts table, all read/write functions
├── token.go       — JWT issuance and parsing (HS256, 30-day expiry)
├── handlers.go    — HTTP handlers for the three endpoints
├── mosaic-auth    — compiled binary (not committed, rebuild with go build)
├── mosaic-auth.db — SQLite database (not committed, auto-created on first run)
└── .mosaic-auth-secret — HMAC signing key (not committed, auto-generated on first run)
```

### mosaic-auth.db

The SQLite database. Created automatically in the `AuthServer/` folder on first run. Contains one table:

```sql
accounts (
    account_id  INTEGER PRIMARY KEY,
    username    TEXT    NOT NULL UNIQUE,
    salt_hex    TEXT    NOT NULL,   -- random 32-byte salt for key hashing
    key_hash    TEXT    NOT NULL,   -- hex(SHA-256(salt || loginKey))
    public_key  TEXT    NOT NULL,   -- hex PKIX DER of ECDSA P-256 public key
    node_id     INTEGER NOT NULL    -- same as account_id for now
)
```

### .mosaic-auth-secret

A random 32-byte hex string generated on first run. Used as the HMAC-SHA256 signing key for JWTs. Keep this secret:

- If someone gets a copy they can forge valid tokens for any account
- If you delete it, the server regenerates a new one on next start and all existing sessions are invalidated (everyone has to log in again)
- Never commit this file — it is in `.gitignore`

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
  "accountID": 1,
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

Validates the login key, records the user's derived public key (on first login), and returns a signed JWT. The daemon writes this token to `~/.mosaic-session` so that `GetAccountID()`, `GetUsername()`, and `GetNodeID()` return real values instead of hardcoded ones.

**Request:**
```json
{
  "username":  "alice",
  "loginKey":  "my-secret-key",
  "publicKey": "3059301306..."   // hex PKIX DER of the HKDF-derived ECDSA key
}
```

**Response (success):**
```json
{
  "success": true,
  "token":   "eyJhbGci...",
  "details": "logged in"
}
```

The token is a standard HS256 JWT. Its payload contains:

```json
{
  "accountID": 1,
  "username":  "alice",
  "nodeID":    1,
  "publicKey": "3059301306...",
  "iat":       1718000000,
  "exp":       1720592000
}
```

Tokens expire after 30 days. The daemon checks expiry on every call to `LoadSession()` and asks the user to log in again if expired.

**Response (wrong key):**
```json
{
  "success": false,
  "details": "invalid username or login key"
}
```

Note: the same error is returned for both unknown username and wrong key to avoid leaking which usernames exist.

---

### `GET /auth/pubkey/{userID}`

Returns the canonical public key for an account ID. Peers call this during manifest merge to confirm that a `UserNetworkEntry`'s embedded public key actually belongs to the claimed `UserID`.

**Response (success):**
```json
{
  "success":   true,
  "accountID": 1,
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

| Variable    | Default                   | Description                        |
|-------------|---------------------------|------------------------------------|
| `AUTH_PORT` | `8081`                    | Port to listen on                  |
| `AUTH_DB`   | `AuthServer/mosaic-auth.db` | Path to the SQLite database      |
| `AUTH_SERVER` | `http://localhost:8081` | Used by the daemon to reach the server |

### Check if it's running

```bash
curl http://localhost:8081/auth/pubkey/1
```

---

## Integration with the Daemon

The daemon talks to the auth server in two places:

**`createAccount.go`** — calls `POST /auth/register` when the user runs `mos create account <username> <key>`

**`loginWithKey.go`** — calls `POST /auth/login`, parses the returned JWT, and writes the claims to `~/.mosaic-session`. After this, all identity helpers read from the session:

- `GetAccountID()` → `session.AccountID`
- `GetUsername()` → `session.Username`
- `GetNodeID()` → `session.NodeID`

The daemon locates the auth server via the `AUTH_SERVER` environment variable (default `http://localhost:8081`). In production this would point to a deployed server rather than localhost.

---

## Security Notes

- **Login keys are never stored in plaintext.** The server stores `hex(SHA-256(salt || loginKey))`. A database breach doesn't expose keys.
- **The same error is returned for unknown username and wrong key** to prevent username enumeration.
- **JWT signatures use HMAC-SHA256** with the server secret. The daemon decodes the payload without verifying the signature — it trusts the connection to the auth server for transport security. In production the server should run over HTTPS.
- **The login key hash uses SHA-256**, which is fast and therefore easier to brute-force than bcrypt or Argon2. This is fine for development but should be upgraded before any real deployment.
- **The public key is recorded on first login and never changes.** Because the keypair is derived deterministically from the login key, logging in from a new device produces the same public key — the server confirms this and the record stays consistent.
