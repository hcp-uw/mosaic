// Package daemon hosts the long-running node process. It accepts
// commands from the local CLI over a Unix socket and translates them
// into operations against the identity, manifest, shard store, and
// peer-to-peer transport.
package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hcp-uw/mosaic/internal/identity"
	"github.com/hcp-uw/mosaic/internal/manifest"
	"github.com/hcp-uw/mosaic/internal/p2p"
	"github.com/hcp-uw/mosaic/internal/storage"
)

// Default per-file Reed-Solomon parameters for MVP.
const (
	DefaultDataShards   = 4
	DefaultParityShards = 2
	DefaultBlockSize    = 64 * 1024 // 64 KiB per data shard, per block
)

// Config is the on-disk node configuration written under HomeDir/config.json.
type Config struct {
	Quota         uint64 `json:"quota"`          // self-imposed storage cap in bytes (0 = unlimited)
	StunAddress   string `json:"stun_address"`   // last STUN server we joined, for reconnect
}

// App is the daemon's shared state, injected into every command handler.
type App struct {
	HomeDir           string
	Identity          *identity.Identity
	Store             *storage.ShardStore
	Manifest          *manifest.Store
	P2P               *p2p.Client
	GetRequestTimeout time.Duration // default 5s; may be lowered in tests

	mu     sync.RWMutex
	config Config
}

// NewApp opens (or creates) the on-disk state under homeDir and returns
// a fully initialized App. The P2P client is left disconnected; callers
// must JoinNetwork to bring it online.
func NewApp(homeDir string) (*App, error) {
	if homeDir == "" {
		return nil, errors.New("home dir is empty")
	}
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir home: %w", err)
	}

	id, err := identity.LoadOrCreate(filepath.Join(homeDir, "identity.key"))
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	store, err := storage.New(filepath.Join(homeDir, "shards"))
	if err != nil {
		return nil, fmt.Errorf("shard store: %w", err)
	}
	mfst, err := manifest.Open(filepath.Join(homeDir, "manifest.db"))
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}

	a := &App{
		HomeDir:           homeDir,
		Identity:          id,
		Store:             store,
		Manifest:          mfst,
		GetRequestTimeout: 5 * time.Second,
	}
	if err := a.loadConfig(); err != nil {
		return nil, err
	}
	return a, nil
}

// Close releases all resources.
func (a *App) Close() error {
	if a.P2P != nil {
		a.P2P.DisconnectFromStun()
	}
	return a.Manifest.Close()
}

// SetP2P installs the P2P client and registers the data-op handlers
// that bridge the network to the storage layer.
func (a *App) SetP2P(c *p2p.Client) {
	a.P2P = c
	c.SetDataHandler(a.dataHandler())
}

// Quota returns the current self-imposed storage cap (bytes).
// 0 means unlimited.
func (a *App) Quota() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.Quota
}

// SetQuota persists a new quota value.
func (a *App) SetQuota(q uint64) error {
	a.mu.Lock()
	a.config.Quota = q
	a.mu.Unlock()
	return a.saveConfig()
}

// SetStunAddress remembers the STUN server we last joined.
func (a *App) SetStunAddress(addr string) error {
	a.mu.Lock()
	a.config.StunAddress = addr
	a.mu.Unlock()
	return a.saveConfig()
}

// AvailableBytes reports how many additional bytes can be stored before
// hitting the quota. Returns ^uint64(0) (max) when no quota is set.
func (a *App) AvailableBytes() uint64 {
	q := a.Quota()
	if q == 0 {
		return ^uint64(0)
	}
	used := a.Store.UsedBytes()
	if used > q {
		return 0
	}
	return q - used
}

func (a *App) configPath() string {
	return filepath.Join(a.HomeDir, "config.json")
}

func (a *App) loadConfig() error {
	data, err := os.ReadFile(a.configPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	a.config = cfg
	return nil
}

func (a *App) saveConfig() error {
	a.mu.RLock()
	cfg := a.config
	a.mu.RUnlock()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(a.configPath(), data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// shardHash returns the SHA-256 of data as a storage.Hash.
func shardHash(data []byte) storage.Hash {
	sum := sha256.Sum256(data)
	var h storage.Hash
	copy(h[:], sum[:])
	return h
}

// hexHash is a small convenience wrapper.
func hexHash(h storage.Hash) string { return hex.EncodeToString(h[:]) }
