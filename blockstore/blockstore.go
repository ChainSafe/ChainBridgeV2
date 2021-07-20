// Copyright 2021 ChainSafe Systems
// SPDX-License-Identifier: LGPL-3.0-only

package blockstore

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	"github.com/ChainSafe/chainbridge-core/config"
	"github.com/syndtr/goleveldb/leveldb"
)

type KeyValueReaderWriter interface {
	KeyValueReader
	KeyValueWriter
}

type KeyValueReader interface {
	GetByKey(key []byte) ([]byte, error)
}

type KeyValueWriter interface {
	SetByKey(key []byte, value []byte) error
}

var (
	ErrNotFound = errors.New("key not found")
)

func StoreBlock(db KeyValueWriter, block *big.Int, chainID uint8) error {
	key := bytes.Buffer{}
	keyS := fmt.Sprintf("chain:%s:block", string(chainID))
	key.WriteString(keyS)
	err := db.SetByKey(key.Bytes(), block.Bytes())
	if err != nil {
		return err
	}
	return nil
}

func GetLastStoredBlock(db KeyValueReader, chainID uint8) (*big.Int, error) {
	key := bytes.Buffer{}
	keyS := fmt.Sprintf("chain:%s:block", string(chainID))
	key.WriteString(keyS)
	v, err := db.GetByKey(key.Bytes())
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return big.NewInt(0), nil
		}
		return nil, err
	}
	block := big.NewInt(0).SetBytes(v)
	return block, nil
}

// SetupBlockstore queries the blockstore for the latest known block. If the latest block is
// greater than config.StartBlock, then config.StartBlock is replaced with the latest known block.
func SetupBlockstore(generalConfig *config.GeneralChainConfig, kvdb KeyValueReaderWriter, startBlock *big.Int) (*big.Int, error) {
	latestBlock, err := GetLastStoredBlock(kvdb, *generalConfig.Id)
	if err != nil {
		return nil, err
	}

	if !generalConfig.FreshStart {
		if latestBlock.Cmp(startBlock) == 1 {
			return latestBlock, nil
		} else {
			return startBlock, nil
		}
	}
	return startBlock, nil
}
