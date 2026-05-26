package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const stubSyncInterval = time.Minute

type shardSet struct {
	filename       string
	ciphertextSize int64
	shards         []string
}

func (c *client) runStubSync(base string) {
	c.syncStubs(base)
	t := time.NewTicker(stubSyncInterval)
	defer t.Stop()
	for range t.C {
		c.syncStubs(base)
	}
}

func (c *client) syncStubs(base string) {
	addresses, err := c.listShardsNet(discoveryTimeout)
	if err != nil {
		log.Printf("client: stub sync list_shards: %v", err)
		return
	}
	byFile := make(map[string]*shardSet)
	for _, addr := range addresses {
		p, err := parseShardAddress(addr)
		if err != nil || p.Identity != c.id {
			continue
		}
		name, err := decryptFilenameToken(c.key, p.FilenameToken)
		if err != nil || name == "" || strings.Contains(name, "/") {
			continue
		}
		s := byFile[name]
		if s == nil {
			s = &shardSet{
				filename:       name,
				ciphertextSize: p.CiphertextSize,
				shards:         make([]string, numShards),
			}
			byFile[name] = s
		}
		if p.Index >= 0 && p.Index < numShards {
			s.shards[p.Index] = addr
		}
	}
	for _, s := range byFile {
		if err := c.ensureStub(base, s); err != nil {
			log.Printf("client: stub sync %q: %v", s.filename, err)
		}
	}
}

func (c *client) ensureStub(base string, s *shardSet) error {
	stubPath := filepath.Join(base, s.filename+stubExt)
	if _, err := os.Stat(stubPath); err == nil {
		return nil
	}
	addrs := make([]string, 0, numShards)
	for _, a := range s.shards {
		if a != "" {
			addrs = append(addrs, a)
		}
	}
	if len(addrs) < dataShards {
		return nil
	}
	m := manifest{
		Name:           s.filename,
		CiphertextSize: s.ciphertextSize,
		Identity:       c.id,
		DataShards:     dataShards,
		ParityShards:   parityShards,
		Shards:         addrs,
	}
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(stubPath, mb, 0o644); err != nil {
		return err
	}
	log.Printf("client: stub sync restored %s", filepath.Base(stubPath))
	return nil
}
