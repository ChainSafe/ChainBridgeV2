package optimismclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ChainSafe/chainbridge-core/chains/evm/evmclient"
	"github.com/ChainSafe/chainbridge-core/chains/optimism/listener"
	"github.com/ChainSafe/chainbridge-core/crypto/secp256k1"
	"github.com/ChainSafe/chainbridge-core/keystore"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/go-resty/resty/v2"
	"github.com/rs/zerolog/log"
)

// Batch represents the data structure that is submitted with
// a series of transactions to layer one
type Batch struct {
	Index             uint64         `json:"index"`
	Root              common.Hash    `json:"root,omitempty"`
	Size              uint32         `json:"size,omitempty"`
	PrevTotalElements uint32         `json:"prevTotalElements,omitempty"`
	ExtraData         hexutil.Bytes  `json:"extraData,omitempty"`
	BlockNumber       uint64         `json:"blockNumber"`
	Timestamp         uint64         `json:"timestamp"`
	Submitter         common.Address `json:"submitter"`
}

// transaction represents the return result of the remote server.
// It either came from a batch or was replicated from the sequencer.
type transaction struct {
	Index       uint64          `json:"index"`
	BatchIndex  uint64          `json:"batchIndex"`
	BlockNumber uint64          `json:"blockNumber"`
	Timestamp   uint64          `json:"timestamp"`
	Value       *hexutil.Big    `json:"value"`
	GasLimit    uint64          `json:"gasLimit,string"`
	Target      common.Address  `json:"target"`
	Origin      *common.Address `json:"origin"`
	Data        hexutil.Bytes   `json:"data"`
	QueueOrigin string          `json:"queueOrigin"`
	QueueIndex  *uint64         `json:"queueIndex"`
	Decoded     *decoded        `json:"decoded"`
}

// signature represents a secp256k1 ECDSA signature
type signature struct {
	R hexutil.Bytes `json:"r"`
	S hexutil.Bytes `json:"s"`
	V uint          `json:"v"`
}

// decoded represents the decoded transaction from the batch.
// When this struct exists in other structs and is set to `nil`,
// it means that the decoding failed.
type decoded struct {
	Signature signature       `json:"sig"`
	Value     *hexutil.Big    `json:"value"`
	GasLimit  uint64          `json:"gasLimit,string"`
	GasPrice  uint64          `json:"gasPrice,string"`
	Nonce     uint64          `json:"nonce,string"`
	Target    *common.Address `json:"target"`
	Data      hexutil.Bytes   `json:"data"`
}

// TransactionBatchResponse represents the response from the remote server
// when querying batches.
type TransactionBatchResponse struct {
	Batch        *Batch         `json:"batch"`
	Transactions []*transaction `json:"transactions"`
}

// SyncStatus represents the state of the remote server. The SyncService
// does not want to begin syncing until the remote server has fully synced.
type SyncStatus struct {
	Syncing                      bool   `json:"syncing"`
	HighestKnownTransactionIndex uint64 `json:"highestKnownTransactionIndex"`
	CurrentTransactionIndex      uint64 `json:"currentTransactionIndex"`
}

type OptimismClient struct {
	*ethclient.Client
	rpClient     *rpc.Client
	nonceLock    sync.Mutex
	config       *OptimismConfig
	nonce        *big.Int
	rollupClient *resty.Client
}

func NewEVMClient() *OptimismClient {
	return &OptimismClient{}
}

func (c *OptimismClient) Configurate(path string, name string) error {
	rawCfg, err := GetConfig(path, name)
	if err != nil {
		return err
	}
	cfg, err := ParseConfig(rawCfg)
	if err != nil {
		return err
	}
	c.config = cfg
	generalConfig := cfg.SharedEVMConfig.GeneralChainConfig

	kp, err := keystore.KeypairFromAddress(generalConfig.From, keystore.EthChain, generalConfig.KeystorePath, generalConfig.Insecure)
	if err != nil {
		panic(err)
	}
	krp := kp.(*secp256k1.Keypair)
	c.config.kp = krp

	log.Info().Str("url", generalConfig.Endpoint).Msg("Connecting to evm chain...")
	rpcClient, err := rpc.DialContext(context.TODO(), generalConfig.Endpoint)
	if err != nil {
		return err
	}
	c.Client = ethclient.NewClient(rpcClient)
	c.rpClient = rpcClient
	c.rollupClient = c.NewRollupClient(c.config.RollupEndpoint)
	log.Info().Msgf("rollup endpoint: %v", c.config.RollupEndpoint)
	status := c.syncRollup()
	log.Error().Msgf("status: %v", status)

	if generalConfig.LatestBlock {
		curr, err := c.LatestBlock()
		if err != nil {
			return err
		}
		cfg.SharedEVMConfig.StartBlock = curr
	}

	return nil

}

func (c *OptimismClient) NewRollupClient(endpoint string) *resty.Client {
	client := resty.New()
	client.SetHostURL(endpoint)
	client.SetHeader("User-Agent", "verifier")
	client.OnAfterResponse(func(c *resty.Client, r *resty.Response) error {
		statusCode := r.StatusCode()
		if statusCode >= 400 {
			method := r.Request.Method
			url := r.Request.URL
			return fmt.Errorf("%d cannot %s %s: %v", statusCode, method, url, "http error")
		}
		return nil
	})
	return client
}

// GetLatestTransactionBatchIndex returns the latest transaction batch index
func (c *OptimismClient) GetLatestTransactionBatchIndex() (*uint64, error) {
	batch, err := c.GetLatestTransactionBatch()
	if err != nil {
		return nil, err
	}
	index := batch.Index
	return &index, nil
}

// GetLatestTransactionBatch will return the latest transaction batch
func (c *OptimismClient) GetLatestTransactionBatch() (*Batch, error) {
	response, err := c.rollupClient.R().
		SetResult(&TransactionBatchResponse{}).
		Get("/batch/transaction/latest")

	if err != nil {
		return nil, errors.New("Cannot get latest transaction batch")
	}
	txBatch, ok := response.Result().(*TransactionBatchResponse)
	if !ok {
		return nil, fmt.Errorf("Cannot parse transaction batch response")
	}
	return txBatch.Batch, nil
}

func (c *OptimismClient) syncRollup() *SyncStatus {
	// Wait until the remote service is done syncing
	tStatus := time.NewTicker(10 * time.Second)
	var status *SyncStatus
	for ; true; <-tStatus.C {
		queriedStatus, err := c.SyncStatus()
		if err != nil {
			log.Error().Msg("Cannot get sync status")
			continue
		}
		if !queriedStatus.Syncing {
			tStatus.Stop()
			status = queriedStatus
			break
		}
		log.Info().Msgf("Still syncing", "index", queriedStatus.CurrentTransactionIndex, "tip", queriedStatus.HighestKnownTransactionIndex)
	}
	return status
}

// SyncStatus will query the remote server to determine if it is still syncing
func (c *OptimismClient) SyncStatus() (*SyncStatus, error) {
	response, err := c.rollupClient.R().
		SetResult(&SyncStatus{}).
		SetQueryParams(map[string]string{
			"backend": "l1", // We are only concerned with transactions that have been batched to Layer One
		}).
		Get("/eth/syncing")

	log.Info().Msgf("response sync status: %v", response)
	if err != nil {
		return nil, fmt.Errorf("Cannot fetch sync status: %w", err)
	}

	status, ok := response.Result().(*SyncStatus)
	if !ok {
		return nil, fmt.Errorf("Cannot parse sync status")
	}

	return status, nil
}

// TODO: WRONG LOGIC, also need to deicde best method to place this in. I am thinking inside the FetchDepositLogs after determining there has been a deposit event
func (c *OptimismClient) IsRollupVerified(blockNumber uint64) bool {
	log.Debug().Msg("Just got inside method IsRollupVerified")

	status := c.syncRollup()

	log.Debug().Msgf("Block number to check against index: %v", blockNumber)
	log.Debug().Msgf("current transaction index: %v", status.CurrentTransactionIndex)
	if blockNumber <= status.CurrentTransactionIndex {
		return true
	} else {
		return false
	}
}

type headerNumber struct {
	Number *big.Int `json:"number"           gencodec:"required"`
}

func (h *headerNumber) UnmarshalJSON(input []byte) error {
	type headerNumber struct {
		Number *hexutil.Big `json:"number" gencodec:"required"`
	}
	var dec headerNumber
	if err := json.Unmarshal(input, &dec); err != nil {
		return err
	}
	if dec.Number == nil {
		return errors.New("missing required field 'number' for Header")
	}
	h.Number = (*big.Int)(dec.Number)
	return nil
}

// LatestBlock returns the latest block from the current chain
func (c *OptimismClient) LatestBlock() (*big.Int, error) {
	var head *headerNumber

	err := c.rpClient.CallContext(context.Background(), &head, "eth_getBlockByNumber", toBlockNumArg(nil), false)
	if err == nil && head == nil {
		err = ethereum.NotFound
	}
	return head.Number, err
}

const (
	DepositSignature string = "Deposit(uint8,bytes32,uint64)"
)

func (c *OptimismClient) FetchDepositLogs(ctx context.Context, contractAddress common.Address, startBlock *big.Int, endBlock *big.Int) ([]*listener.DepositLogs, error) {
	definition := "[{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"uint8\",\"name\":\"destinationChainID\",\"type\":\"uint8\"},{\"indexed\":false,\"internalType\":\"bytes32\",\"name\":\"resourceID\",\"type\":\"bytes32\"},{\"indexed\":false,\"internalType\":\"uint64\",\"name\":\"depositNonce\",\"type\":\"uint64\"}],\"name\":\"Deposit\",\"type\":\"event\"}]"
	contractAbi, err := abi.JSON(strings.NewReader(definition))
	if err != nil {
		log.Fatal().Msgf("error: %v", err)
	}

	logs, err := c.FilterLogs(ctx, buildQuery(contractAddress, DepositSignature, startBlock, endBlock))
	if err != nil {
		return nil, err
	}
	depositLogs := make([]*listener.DepositLogs, 0)
	for _, l := range logs {
		log.Info().Msgf("deposit log block number: %v", l.BlockNumber)
		log.Info().Msgf("is rollup and batch verified: %v", c.IsRollupVerified(l.BlockNumber))
		var dl listener.DepositLogs
		err := contractAbi.UnpackIntoInterface(&dl, "Deposit", l.Data)
		if err != nil {
			log.Fatal().Msgf("error: %v", err)
		}
		log.Info().Msgf("Deposit Logs dest chain id: %v, deposit nonce: %v, resource id: %v", dl.DestinationChainID, dl.DepositNonce, dl.ResourceID)
		depositLogs = append(depositLogs, &dl)
	}

	return depositLogs, nil
}

// SendRawTransaction accepts rlp-encode of signed transaction and sends it via RPC call
func (c *OptimismClient) SendRawTransaction(ctx context.Context, tx []byte) error {
	return c.rpClient.CallContext(ctx, nil, "eth_sendRawTransaction", hexutil.Encode(tx))
}

func (c *OptimismClient) CallContract(ctx context.Context, callArgs map[string]interface{}, blockNumber *big.Int) ([]byte, error) {
	var hex hexutil.Bytes
	err := c.rpClient.CallContext(ctx, &hex, "eth_call", callArgs, toBlockNumArg(blockNumber))
	if err != nil {
		return nil, err
	}
	return hex, nil
}

func (c *OptimismClient) PendingCallContract(ctx context.Context, callArgs map[string]interface{}) ([]byte, error) {
	var hex hexutil.Bytes
	err := c.rpClient.CallContext(ctx, &hex, "eth_call", callArgs, "pending")
	if err != nil {
		return nil, err
	}
	return hex, nil
}

//func (c *EVMClient) ChainID()

func (c *OptimismClient) SignAndSendTransaction(ctx context.Context, tx evmclient.CommonTransaction) (common.Hash, error) {
	id, err := c.ChainID(ctx)
	if err != nil {
		panic(err)
	}
	rawTX, err := tx.RawWithSignature(c.config.kp.PrivateKey(), id)
	if err != nil {
		return common.Hash{}, err
	}

	err = c.SendRawTransaction(ctx, rawTX)
	if err != nil {
		return common.Hash{}, err
	}
	return tx.Hash(), nil
}

func (c *OptimismClient) RelayerAddress() common.Address {
	return c.config.kp.CommonAddress()
}

func (c *OptimismClient) LockNonce() {
	c.nonceLock.Lock()
}

func (c *OptimismClient) UnlockNonce() {
	c.nonceLock.Unlock()
}

func (c *OptimismClient) UnsafeNonce() (*big.Int, error) {
	var err error
	for i := 0; i <= 10; i++ {
		if c.nonce == nil {
			nonce, err := c.PendingNonceAt(context.Background(), c.config.kp.CommonAddress())
			if err != nil {
				time.Sleep(1)
				continue
			}
			c.nonce = big.NewInt(0).SetUint64(nonce)
			return c.nonce, nil
		}
		return c.nonce, nil
	}
	return nil, err
}

func (c *OptimismClient) UnsafeIncreaseNonce() error {
	nonce, err := c.UnsafeNonce()
	log.Debug().Str("nonce", nonce.String()).Msg("Before increase")
	if err != nil {
		return err
	}
	c.nonce = nonce.Add(nonce, big.NewInt(1))
	log.Debug().Str("nonce", c.nonce.String()).Msg("After increase")
	return nil
}

func (c *OptimismClient) GasLimit(msg ethereum.CallMsg) *big.Int {
	gas, err := c.EstimateGas(context.TODO(), msg)
	if err != nil {
		log.Fatal().Msgf("could not estimate gas when transacting with optimism: %v", err)
	}
	return big.NewInt(int64(gas))
}

func (c *OptimismClient) GasPrice() (*big.Int, error) {
	// Kovan Optimism requires this gas price at the moment
	if c.config.SharedEVMConfig.GeneralChainConfig.Name == "optimism" {
		return big.NewInt(15000000), nil
	}

	// Local optimism needs gas price of 0, set maxGasPrice to 0 in config
	gasPrice, err := c.SafeEstimateGas(context.TODO())
	if err != nil {
		return nil, err
	}
	return gasPrice, nil
}

func (c *OptimismClient) SafeEstimateGas(ctx context.Context) (*big.Int, error) {
	suggestedGasPrice, err := c.SuggestGasPrice(context.TODO())
	if err != nil {
		return nil, err
	}

	gasPrice := multiplyGasPrice(suggestedGasPrice, c.config.SharedEVMConfig.GasMultiplier)

	if gasPrice.Cmp(c.config.SharedEVMConfig.MaxGasPrice) == 1 {
		return c.config.SharedEVMConfig.MaxGasPrice, nil
	} else {
		return gasPrice, nil
	}
}

func multiplyGasPrice(gasEstimate *big.Int, gasMultiplier *big.Float) *big.Int {

	gasEstimateFloat := new(big.Float).SetInt(gasEstimate)

	result := gasEstimateFloat.Mul(gasEstimateFloat, gasMultiplier)

	gasPrice := new(big.Int)

	result.Int(gasPrice)

	return gasPrice
}

func toBlockNumArg(number *big.Int) string {
	if number == nil {
		return "latest"
	}
	return hexutil.EncodeBig(number)
}

// buildQuery constructs a query for the bridgeContract by hashing sig to get the event topic
func buildQuery(contract common.Address, sig string, startBlock *big.Int, endBlock *big.Int) ethereum.FilterQuery {
	query := ethereum.FilterQuery{
		FromBlock: startBlock,
		ToBlock:   endBlock,
		Addresses: []common.Address{contract},
		Topics: [][]common.Hash{
			{crypto.Keccak256Hash([]byte(sig))},
		},
	}
	return query
}

func (c *OptimismClient) GetConfig() *OptimismConfig {
	return c.config
}
