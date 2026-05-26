package main

import (
	"fmt"
	"strconv"
	"strings"
)

type shardAddressParts struct {
	Identity       string
	FilenameToken  string
	CiphertextSize int64
	Index          int
}

func makeShardAddress(identity, filenameToken string, ciphertextSize int64, idx int) string {
	return fmt.Sprintf("%s.%s.%d.%02d", identity, filenameToken, ciphertextSize, idx)
}

func parseShardAddress(address string) (shardAddressParts, error) {
	parts := strings.Split(address, ".")
	if len(parts) != 4 {
		return shardAddressParts{}, fmt.Errorf("invalid shard address format")
	}
	cipherSize, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || cipherSize < 0 {
		return shardAddressParts{}, fmt.Errorf("invalid ciphertext size")
	}
	idx, err := strconv.Atoi(parts[3])
	if err != nil || idx < 0 || idx >= numShards {
		return shardAddressParts{}, fmt.Errorf("invalid shard index")
	}
	return shardAddressParts{
		Identity:       parts[0],
		FilenameToken:  parts[1],
		CiphertextSize: cipherSize,
		Index:          idx,
	}, nil
}
