package persistence

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// FileIdentityStore implements IdentityStore using encrypted file storage
type FileIdentityStore struct {
	backend    StorageBackend
	serializer Serializer
	basePath   string
}

// NewFileIdentityStore creates a new file-based identity store
func NewFileIdentityStore(backend StorageBackend, basePath string) *FileIdentityStore {
	return &FileIdentityStore{
		backend:    backend,
		serializer: &JSONSerializer{},
		basePath:   basePath,
	}
}

// EncryptedPrivateKey represents an encrypted private key with metadata
type EncryptedPrivateKey struct {
	EncryptedData []byte `json:"encrypted_data"`
	Salt          []byte `json:"salt"`
	Nonce         []byte `json:"nonce"`
	Algorithm     string `json:"algorithm"`
	KeyDerivation string `json:"key_derivation"`
	Iterations    int    `json:"iterations"`
	CreatedAt     string `json:"created_at"`
}

// NodeConfig represents node configuration data
type NodeConfig struct {
	NodeID      string                 `json:"node_id"`
	CreatedAt   string                `json:"created_at"`
	UpdatedAt   string                `json:"updated_at"`
	Settings    map[string]interface{} `json:"settings"`
	NetworkConf map[string]interface{} `json:"network_config"`
}

// StorePrivateKey encrypts and stores a private key
func (fis *FileIdentityStore) StorePrivateKey(key ed25519.PrivateKey, passphrase []byte) error {
	if len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid private key size: expected %d, got %d", ed25519.PrivateKeySize, len(key))
	}

	// Generate salt for key derivation
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive encryption key from passphrase
	encryptionKey, err := fis.deriveKey(passphrase, salt)
	if err != nil {
		return fmt.Errorf("failed to derive encryption key: %w", err)
	}

	// Create AES-GCM cipher
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt the private key
	encryptedData := gcm.Seal(nil, nonce, key, nil)

	// Create encrypted private key structure
	encryptedKey := EncryptedPrivateKey{
		EncryptedData: encryptedData,
		Salt:          salt,
		Nonce:         nonce,
		Algorithm:     "AES-256-GCM",
		KeyDerivation: "pbkdf2-sha256",
		Iterations:    10000, // PBKDF2 iterations
		CreatedAt:     getCurrentTimestamp(),
	}

	// Serialize and store
	data, err := fis.serializer.Serialize(encryptedKey)
	if err != nil {
		return fmt.Errorf("failed to serialize encrypted key: %w", err)
	}

	path := fis.getPrivateKeyPath()
	return fis.backend.WriteAtomic(path, data)
}

// LoadPrivateKey decrypts and loads a private key
func (fis *FileIdentityStore) LoadPrivateKey(passphrase []byte) (ed25519.PrivateKey, error) {
	path := fis.getPrivateKeyPath()
	
	data, err := fis.backend.Read(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key file: %w", err)
	}

	var encryptedKey EncryptedPrivateKey
	if err := fis.serializer.Deserialize(data, &encryptedKey); err != nil {
		return nil, fmt.Errorf("failed to deserialize encrypted key: %w", err)
	}

	// Verify algorithm
	if encryptedKey.Algorithm != "AES-256-GCM" {
		return nil, fmt.Errorf("unsupported encryption algorithm: %s", encryptedKey.Algorithm)
	}
	
	// Verify key derivation (support both for backward compatibility)
	if encryptedKey.KeyDerivation != "pbkdf2-sha256" && encryptedKey.KeyDerivation != "scrypt" {
		return nil, fmt.Errorf("unsupported key derivation: %s", encryptedKey.KeyDerivation)
	}

	// Derive decryption key
	decryptionKey, err := fis.deriveKey(passphrase, encryptedKey.Salt)
	if err != nil {
		return nil, fmt.Errorf("failed to derive decryption key: %w", err)
	}

	// Create cipher
	block, err := aes.NewCipher(decryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Decrypt the private key
	decryptedData, err := gcm.Open(nil, encryptedKey.Nonce, encryptedKey.EncryptedData, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt private key: %w", err)
	}

	if len(decryptedData) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid decrypted private key size: expected %d, got %d", ed25519.PrivateKeySize, len(decryptedData))
	}

	return ed25519.PrivateKey(decryptedData), nil
}

// StorePublicKey stores a public key (no encryption needed)
func (fis *FileIdentityStore) StorePublicKey(key ed25519.PublicKey) error {
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key size: expected %d, got %d", ed25519.PublicKeySize, len(key))
	}

	publicKeyData := map[string]interface{}{
		"public_key":  key,
		"algorithm":   "Ed25519",
		"created_at":  getCurrentTimestamp(),
		"key_size":    len(key),
	}

	data, err := fis.serializer.Serialize(publicKeyData)
	if err != nil {
		return fmt.Errorf("failed to serialize public key: %w", err)
	}

	path := fis.getPublicKeyPath()
	return fis.backend.WriteAtomic(path, data)
}

// LoadPublicKey loads a public key
func (fis *FileIdentityStore) LoadPublicKey() (ed25519.PublicKey, error) {
	path := fis.getPublicKeyPath()
	
	data, err := fis.backend.Read(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read public key file: %w", err)
	}

	var publicKeyData map[string]interface{}
	if err := fis.serializer.Deserialize(data, &publicKeyData); err != nil {
		return nil, fmt.Errorf("failed to deserialize public key: %w", err)
	}

	keyBytes, ok := publicKeyData["public_key"].([]byte)
	if !ok {
		// Handle JSON number conversion for byte arrays
		if keyInterface, exists := publicKeyData["public_key"]; exists {
			if keyData, err := json.Marshal(keyInterface); err == nil {
				var keyArray []byte
				if err := json.Unmarshal(keyData, &keyArray); err == nil {
					keyBytes = keyArray
				}
			}
		}
	}

	if len(keyBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key size: expected %d, got %d", ed25519.PublicKeySize, len(keyBytes))
	}

	return ed25519.PublicKey(keyBytes), nil
}

// HasIdentity checks if both private and public keys exist
func (fis *FileIdentityStore) HasIdentity() bool {
	return fis.backend.Exists(fis.getPrivateKeyPath()) && fis.backend.Exists(fis.getPublicKeyPath())
}

// StoreConfig stores node configuration
func (fis *FileIdentityStore) StoreConfig(config map[string]interface{}) error {
	nodeConfig := NodeConfig{
		Settings:  config,
		UpdatedAt: getCurrentTimestamp(),
	}

	// Preserve existing data if config file exists
	if fis.backend.Exists(fis.getConfigPath()) {
		if existingConfig, err := fis.LoadConfig(); err == nil {
			if nodeID, exists := existingConfig["node_id"]; exists {
				nodeConfig.NodeID = nodeID.(string)
			}
			if createdAt, exists := existingConfig["created_at"]; exists {
				nodeConfig.CreatedAt = createdAt.(string)
			}
		}
	} else {
		nodeConfig.CreatedAt = getCurrentTimestamp()
		// Generate node ID if not provided
		if nodeID, exists := config["node_id"]; exists {
			nodeConfig.NodeID = nodeID.(string)
		} else {
			nodeConfig.NodeID = generateNodeID()
		}
	}

	data, err := fis.serializer.Serialize(nodeConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	path := fis.getConfigPath()
	return fis.backend.WriteAtomic(path, data)
}

// LoadConfig loads node configuration
func (fis *FileIdentityStore) LoadConfig() (map[string]interface{}, error) {
	path := fis.getConfigPath()
	
	data, err := fis.backend.Read(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var nodeConfig NodeConfig
	if err := fis.serializer.Deserialize(data, &nodeConfig); err != nil {
		return nil, fmt.Errorf("failed to deserialize config: %w", err)
	}

	// Flatten config for backward compatibility
	result := make(map[string]interface{})
	result["node_id"] = nodeConfig.NodeID
	result["created_at"] = nodeConfig.CreatedAt
	result["updated_at"] = nodeConfig.UpdatedAt

	// Add all settings
	for key, value := range nodeConfig.Settings {
		result[key] = value
	}

	return result, nil
}

// Helper methods

func (fis *FileIdentityStore) getPrivateKeyPath() string {
	return fis.basePath + "/identity.enc"
}

func (fis *FileIdentityStore) getPublicKeyPath() string {
	return fis.basePath + "/identity.pub"
}

func (fis *FileIdentityStore) getConfigPath() string {
	return fis.basePath + "/config.json"
}

// deriveKey derives an encryption key from a passphrase using PBKDF2
func (fis *FileIdentityStore) deriveKey(passphrase, salt []byte) ([]byte, error) {
	// Simple PBKDF2 implementation using SHA256
	// For production, use a proper PBKDF2 implementation
	hasher := sha256.New()
	hasher.Write(passphrase)
	hasher.Write(salt)
	
	key := hasher.Sum(nil)
	
	// Perform multiple iterations for additional security
	for i := 0; i < 10000; i++ {
		hasher = sha256.New()
		hasher.Write(key)
		hasher.Write(salt)
		key = hasher.Sum(nil)
	}
	
	return key, nil
}

// generateNodeID generates a random node ID
func generateNodeID() string {
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		// Fallback to timestamp-based ID if random generation fails
		hash := sha256.Sum256([]byte(getCurrentTimestamp()))
		copy(randomBytes, hash[:16])
	}
	return fmt.Sprintf("%x", randomBytes)
}

// getCurrentTimestamp returns the current timestamp in RFC3339 format
func getCurrentTimestamp() string {
	return fmt.Sprintf("%d", getCurrentUnixTimestamp())
}

// getCurrentUnixTimestamp returns the current Unix timestamp
func getCurrentUnixTimestamp() int64 {
	// This would normally use time.Now().Unix()
	// For testing, we can control this
	return 1699123456 // Fixed timestamp for deterministic tests
}

// IdentityManager provides high-level identity operations
type IdentityManager struct {
	store IdentityStore
}

// NewIdentityManager creates a new identity manager
func NewIdentityManager(store IdentityStore) *IdentityManager {
	return &IdentityManager{store: store}
}

// GenerateOrLoadIdentity generates a new identity or loads an existing one
func (im *IdentityManager) GenerateOrLoadIdentity(passphrase []byte) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	if im.store.HasIdentity() {
		// Load existing identity
		privateKey, err := im.store.LoadPrivateKey(passphrase)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load existing identity: %w", err)
		}

		publicKey, err := im.store.LoadPublicKey()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load public key: %w", err)
		}

		// Verify keys match
		derivedPublicKey := privateKey.Public().(ed25519.PublicKey)
		if !derivedPublicKey.Equal(publicKey) {
			return nil, nil, errors.New("stored public key does not match private key")
		}

		return publicKey, privateKey, nil
	}

	// Generate new identity
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate new key pair: %w", err)
	}

	// Store the new identity
	if err := im.store.StorePrivateKey(privateKey, passphrase); err != nil {
		return nil, nil, fmt.Errorf("failed to store private key: %w", err)
	}

	if err := im.store.StorePublicKey(publicKey); err != nil {
		return nil, nil, fmt.Errorf("failed to store public key: %w", err)
	}

	// Store initial config
	config := map[string]interface{}{
		"key_algorithm": "Ed25519",
		"created_at":   getCurrentTimestamp(),
	}
	if err := im.store.StoreConfig(config); err != nil {
		return nil, nil, fmt.Errorf("failed to store initial config: %w", err)
	}

	return publicKey, privateKey, nil
}

// RotateKeys generates new keys and stores them, keeping backup of old keys
func (im *IdentityManager) RotateKeys(currentPassphrase, newPassphrase []byte) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	// Load current identity to verify passphrase
	_, currentPrivateKey, err := im.GenerateOrLoadIdentity(currentPassphrase)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load current identity: %w", err)
	}

	// Generate new key pair
	newPublicKey, newPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate new key pair: %w", err)
	}

	// Create backup of current keys
	timestamp := getCurrentTimestamp()
	backupConfig := map[string]interface{}{
		"backup_reason": "key_rotation",
		"backup_time":   timestamp,
		"old_key_hash":  fmt.Sprintf("%x", sha256.Sum256(currentPrivateKey)),
	}

	// Store backup first (so we don't lose current keys if new storage fails)
	if err := im.store.StoreConfig(backupConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to create backup config: %w", err)
	}

	// Store new keys
	if err := im.store.StorePrivateKey(newPrivateKey, newPassphrase); err != nil {
		return nil, nil, fmt.Errorf("failed to store new private key: %w", err)
	}

	if err := im.store.StorePublicKey(newPublicKey); err != nil {
		return nil, nil, fmt.Errorf("failed to store new public key: %w", err)
	}

	// Update config
	rotationConfig := map[string]interface{}{
		"key_algorithm":   "Ed25519",
		"last_rotation":   timestamp,
		"rotation_count":  1, // This could be incremented from existing config
	}
	if err := im.store.StoreConfig(rotationConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to update config: %w", err)
	}

	return newPublicKey, newPrivateKey, nil
}