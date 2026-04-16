# Authentication — What's Left to Build

---

## What's Already Done

- **Login key persistence** — `mos login key <key>` saves the raw key to `~/.mosaic-login.key` (0600)
- **Deterministic keypair derivation** — `DeriveUserKeyFromLoginKey` runs HKDF-SHA256 on the login key to produce a fixed ECDSA P-256 keypair, cached to `~/.mosaic-user.key`. Same login key on any machine → same keypair
- **ECDSA signing** — every user's manifest section is signed with their private key when written
- **ECDSA verification** — `MergeNetworkManifest` calls `VerifyUserEntry` on every incoming entry; tampered entries are dropped before any disk write

---

## What's Still Stubbed

`GetAccountID()`, `GetUsername()`, and `GetNodeID()` all return hardcoded values. This means:

- Every node running the code claims the same identity
- Nothing stops a malicious node from writing a manifest entry with any `UserID` it wants — the ECDSA signature will be internally valid, but it proves nothing about who actually owns that `UserID`

The ECDSA layer proves **"these bytes were not tampered with after signing."** It does not prove **"this `UserID` legitimately belongs to the person who signed."** That second guarantee requires a server.

---

## What the Auth Server Needs to Do

### Registration (one time)

1. User creates an account — username, password, email
2. Server assigns a permanent `AccountID` (opaque integer the user never picks)
3. Server stores `{ accountID, username, passwordHash, publicKey: null }`

No public key is bound yet — that happens at first login on a device.

### Login

1. User runs `mos login key <loginKey>` on their machine
2. Daemon derives the ECDSA keypair from the login key (already implemented)
3. Daemon calls `POST /auth/login`:
   ```json
   {
     "loginKey": "<the key>",
     "publicKey": "<PKIX DER hex of derived public key>"
   }
   ```
4. Server validates the login key against the account, then records the public key for that account
5. Server returns a signed JWT:
   ```json
   {
     "accountID": 12304938,
     "username": "GavJoons",
     "nodeID": 10,
     "publicKey": "<hex>",
     "exp": 1718000000
   }
   ```
6. Daemon writes the JWT to `~/.mosaic-session` and reads identity values from it instead of the hardcoded helpers

### Public Key Registry

Server exposes `GET /auth/pubkey/{userID}` → returns the canonical public key for that account.

During `MergeNetworkManifest`, when a peer encounters a `UserNetworkEntry` from an unknown user, it can call this endpoint to confirm that the `PublicKey` embedded in the entry actually belongs to the claimed `UserID`. A node that fabricated a `UserID` will have a public key the server has never seen for that account — its entry gets rejected.

---

## Why the Binding Matters

The attack without this check:

1. Malicious node generates its own keypair
2. Writes a manifest entry claiming `UserID = 12304938` with its own public key
3. Signs the entry with its own private key — signature is valid
4. `VerifyUserEntry` passes because it only checks that the signature matches the embedded key
5. The malicious entry merges into every peer's manifest, overwriting the real user's entry

With the registry check:
- Step 4 calls `GET /auth/pubkey/12304938`
- Server returns the real user's public key
- The malicious node's embedded key doesn't match → entry rejected

---

## Files to Change

| File | Change |
|------|--------|
| `internal/daemon/handlers/loginWithKey.go` | After saving the login key, call `POST /auth/login`, write the returned JWT to `~/.mosaic-session` |
| `internal/daemon/handlers/helpers/getAccountID.go` | Read `accountID` from `~/.mosaic-session` instead of returning a constant |
| `internal/daemon/handlers/helpers/getUsername.go` | Read `username` from `~/.mosaic-session` |
| `internal/daemon/handlers/helpers/getNodeID.go` | Read `nodeID` from `~/.mosaic-session` |
| `internal/daemon/handlers/handleLogout.go` | Clear `~/.mosaic-session`, `~/.mosaic-login.key`, and `~/.mosaic-user.key` |
| `internal/fileSystem/networkManifest.go` | `MergeNetworkManifest` should accept an optional verify callback so callers can pass in a `GET /auth/pubkey/{userID}` lookup |

---

## Session File

`~/.mosaic-session` (0600) holds the decoded JWT claims as JSON so the daemon can read identity without re-parsing or re-validating the token on every call:

```json
{
  "accountID": 12304938,
  "username": "GavJoons",
  "nodeID": 10,
  "publicKey": "<hex>",
  "expiresAt": "2025-06-10T00:00:00Z"
}
```

On startup the daemon checks `expiresAt`. If expired, it clears the session and asks the user to log in again.
