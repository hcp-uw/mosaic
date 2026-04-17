package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

// Account holds all server-side state for one registered user.
type Account struct {
	AccountID int
	Username  string
	SaltHex   string
	KeyHash   string
	// PublicKey is the hex PKIX DER of the HKDF-derived ECDSA P-256 key.
	// Recorded on first login; deterministic so it never changes across devices.
	PublicKey string
}

// Node represents one machine a user has logged in from.
// NodeNumber is the ID: 1, 2, 3... scoped per account.
// A node is uniquely identified globally by (AccountID, NodeNumber).
type Node struct {
	AccountID  int
	NodeNumber int
}

var db *sql.DB

// loadDB opens (or creates) the SQLite database at path and runs migrations.
func loadDB(path string) error {
	var err error
	db, err = sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("could not open database: %w", err)
	}

	// Single writer, multiple readers — safe for a single-process server.
	db.SetMaxOpenConns(1)

	if err := migrate(); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}
	return nil
}

func migrate() error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS accounts (
			account_id  INTEGER PRIMARY KEY AUTOINCREMENT,
			username    TEXT    NOT NULL UNIQUE,
			salt_hex    TEXT    NOT NULL,
			key_hash    TEXT    NOT NULL,
			public_key  TEXT    NOT NULL DEFAULT ''
		);

		CREATE TABLE IF NOT EXISTS nodes (
			account_id   INTEGER NOT NULL REFERENCES accounts(account_id),
			node_number  INTEGER NOT NULL,
			PRIMARY KEY (account_id, node_number)
		);
	`)
	return err
}

// formatAccountID returns the zero-padded 9-digit display form of an account ID.
// e.g. 1 → "000000001", 12345 → "000012345"
func formatAccountID(id int) string {
	return fmt.Sprintf("%09d", id)
}

// ── Accounts ──────────────────────────────────────────────────────────────────

// createAccount registers a new account. Returns an error if the username is taken.
func createAccount(username, loginKey string) (*Account, error) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("could not generate salt: %w", err)
	}

	saltHex := hex.EncodeToString(salt)
	keyHash := hashLoginKey(salt, loginKey)

	res, err := db.Exec(
		`INSERT INTO accounts (username, salt_hex, key_hash) VALUES (?, ?, ?)`,
		username, saltHex, keyHash,
	)
	if err != nil {
		return nil, fmt.Errorf("username already taken")
	}

	id, _ := res.LastInsertId()
	return &Account{
		AccountID: int(id),
		Username:  username,
		SaltHex:   saltHex,
		KeyHash:   keyHash,
	}, nil
}

// lookupByUsername returns the account for a username, or nil if not found.
func lookupByUsername(username string) *Account {
	row := db.QueryRow(
		`SELECT account_id, username, salt_hex, key_hash, public_key
		 FROM accounts WHERE username = ?`, username,
	)
	return scanAccount(row)
}

// lookupByID returns the account for an accountID, or nil if not found.
func lookupByID(accountID int) *Account {
	row := db.QueryRow(
		`SELECT account_id, username, salt_hex, key_hash, public_key
		 FROM accounts WHERE account_id = ?`, accountID,
	)
	return scanAccount(row)
}

func scanAccount(row *sql.Row) *Account {
	var acc Account
	if err := row.Scan(
		&acc.AccountID, &acc.Username, &acc.SaltHex,
		&acc.KeyHash, &acc.PublicKey,
	); err != nil {
		return nil
	}
	return &acc
}

// setPublicKey records the user's derived public key after a successful login.
func setPublicKey(accountID int, publicKey string) error {
	_, err := db.Exec(
		`UPDATE accounts SET public_key = ? WHERE account_id = ?`,
		publicKey, accountID,
	)
	return err
}

// verifyLoginKey checks loginKey against the stored hash for an account.
func verifyLoginKey(acc *Account, loginKey string) bool {
	salt, err := hex.DecodeString(acc.SaltHex)
	if err != nil {
		return false
	}
	return hashLoginKey(salt, loginKey) == acc.KeyHash
}

// hashLoginKey returns hex(SHA-256(salt || loginKey)).
// NOTE: upgrade to bcrypt/argon2 before deploying to production.
func hashLoginKey(salt []byte, loginKey string) string {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(loginKey))
	return hex.EncodeToString(h.Sum(nil))
}

// ── Nodes ─────────────────────────────────────────────────────────────────────

// createNode creates a new node for the account and returns it.
// NodeNumber auto-increments per user: first device = 1, second = 2, etc.
func createNode(accountID int) (*Node, error) {
	var maxNum int
	row := db.QueryRow(
		`SELECT COALESCE(MAX(node_number), 0) FROM nodes WHERE account_id = ?`, accountID,
	)
	if err := row.Scan(&maxNum); err != nil {
		return nil, fmt.Errorf("could not determine next node number: %w", err)
	}
	nodeNumber := maxNum + 1

	_, err := db.Exec(
		`INSERT INTO nodes (account_id, node_number) VALUES (?, ?)`,
		accountID, nodeNumber,
	)
	if err != nil {
		return nil, fmt.Errorf("could not create node: %w", err)
	}

	return &Node{AccountID: accountID, NodeNumber: nodeNumber}, nil
}

// lookupNode returns the node for (accountID, nodeNumber), or nil if not found.
func lookupNode(accountID, nodeNumber int) *Node {
	var n Node
	row := db.QueryRow(
		`SELECT account_id, node_number FROM nodes WHERE account_id = ? AND node_number = ?`,
		accountID, nodeNumber,
	)
	if err := row.Scan(&n.AccountID, &n.NodeNumber); err != nil {
		return nil
	}
	return &n
}

// ── Misc ──────────────────────────────────────────────────────────────────────

// defaultDBPath returns the default database path.
func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "mosaic-auth.db"
	}
	return home + "/.mosaic-auth.db"
}
