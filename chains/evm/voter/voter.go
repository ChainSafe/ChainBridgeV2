// Copyright 2021 ChainSafe Systems
// SPDX-License-Identifier: LGPL-3.0-only

package voter

import (
	"context"
	"math/big"
	"time"

	"github.com/ChainSafe/chainbridge-core/chains/evm/evmclient"

	"github.com/ChainSafe/chainbridge-core/relayer"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog/log"
)

var BlockRetryInterval = time.Second * 5

type ChainClient interface {
	LatestBlock() (*big.Int, error)
	SignAndSendTransaction(ctx context.Context, tx evmclient.CommonTransaction) (common.Hash, error)
	RelayerAddress() common.Address
	CallContract(ctx context.Context, callArgs map[string]interface{}, blockNumber *big.Int) ([]byte, error)
	UnsafeNonce() (*big.Int, error)
	LockNonce()
	UnlockNonce()
	UnsafeIncreaseNonce() error
	GasLimit(msg ethereum.CallMsg) *big.Int
	GasPrice() (*big.Int, error)
	ChainID(ctx context.Context) (*big.Int, error)
}

type Proposer interface {
	Status(client ChainClient) (relayer.ProposalStatus, error)
	VotedBy(client ChainClient, by common.Address) (bool, error)
	Execute(client ChainClient) error
	Vote(client ChainClient) error
}

type MessageHandler interface {
	HandleMessage(m *relayer.Message) (Proposer, error)
}

type EVMVoter struct {
	stop   <-chan struct{}
	mh     MessageHandler
	client ChainClient
}

func NewVoter(mh MessageHandler, client ChainClient) *EVMVoter {
	return &EVMVoter{
		mh:     mh,
		client: client,
	}
}

func (w *EVMVoter) VoteProposal(m *relayer.Message) error {
	prop, err := w.mh.HandleMessage(m)
	if err != nil {
		return err
	}
	ps, err := prop.Status(w.client)
	if err != nil {
		log.Error().Err(err).Msgf("error getting proposal status %+v", prop)
	}

	votedByCurrentExecutor, err := prop.VotedBy(w.client, w.client.RelayerAddress())
	if err != nil {
		return err
	}

	if votedByCurrentExecutor || ps == relayer.ProposalStatusPassed || ps == relayer.ProposalStatusCanceled || ps == relayer.ProposalStatusExecuted {
		if ps == relayer.ProposalStatusPassed {
			// We should not vote for this proposal but it is ready to be executed
			err = prop.Execute(w.client)
			if err != nil {
				log.Error().Err(err).Msgf("Executing failed")
				return err
			}
			return nil
		} else {
			log.Debug().Bool("voted", votedByCurrentExecutor).Str("voter", w.client.RelayerAddress().String()).Msgf("proposal status %s", relayer.StatusMap[ps])
			return nil
		}
	}
	err = prop.Vote(w.client)
	if err != nil {
		log.Error().Err(err).Msgf("Voting failed")
		return err
	}
	// Checking every 5 seconds does proposal is ready to be executed
	// TODO: somehow update infinity loop to break after some period of time
	for {
		select {
		case <-time.After(BlockRetryInterval):
			ps, err := prop.Status(w.client)
			log.Info().Msgf("Proposal status: %v", ps)
			if err != nil {
				log.Error().Err(err).Msgf("error getting proposal status %+v", prop)
				return err
			}
			if ps == relayer.ProposalStatusPassed {
				err = prop.Execute(w.client)
				if err != nil {
					log.Error().Err(err).Msgf("Executing failed")
					return err
				}
				return nil
			}
			continue
		case <-w.stop:
			return nil

		}
	}
}
