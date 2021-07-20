package listener

import (
	"context"
	"errors"
	"math/big"
	"strings"

	"github.com/ChainSafe/chainbridge-core/relayer"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

type EventHandlers map[common.Address]EventHandlerFunc
type EventHandlerFunc func(sourceID, destId uint8, nonce uint64, handlerContractAddress common.Address, caller ChainClient) (*relayer.Message, error)

type ETHEventHandler struct {
	bridgeAddress common.Address
	eventHandlers EventHandlers
	client        ChainClient
}

func NewETHEventHandler(address common.Address, client ChainClient) *ETHEventHandler {
	return &ETHEventHandler{
		bridgeAddress: address,
		client:        client,
	}
}

func (e *ETHEventHandler) HandleEvent(sourceID, destID uint8, depositNonce uint64, rID [32]byte) (*relayer.Message, error) {
	addr, err := e.matchResourceIDToHandlerAddress(rID)
	if err != nil {
		return nil, err
	}

	eventHandler, err := e.matchAddressWithHandlerFunc(addr)
	if err != nil {
		return nil, err
	}

	return eventHandler(sourceID, destID, depositNonce, addr, e.client)
}

func (e *ETHEventHandler) matchResourceIDToHandlerAddress(rID [32]byte) (common.Address, error) {
	definition := "[{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"\",\"type\":\"bytes32\"}],\"name\":\"_resourceIDToHandlerAddress\",\"outputs\":[{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"}],\"stateMutability\":\"view\",\"type\":\"function\"}]"
	a, err := abi.JSON(strings.NewReader(definition))
	if err != nil {
		return common.Address{}, err
	}
	input, err := a.Pack("_resourceIDToHandlerAddress", rID)
	if err != nil {
		return common.Address{}, err
	}
	msg := ethereum.CallMsg{From: common.Address{}, To: &e.bridgeAddress, Data: input}
	out, err := e.client.CallContract(context.TODO(), toCallArg(msg), nil)
	if err != nil {
		return common.Address{}, err
	}
	res, err := a.Unpack("_resourceIDToHandlerAddress", out)
	out0 := *abi.ConvertType(res[0], new(common.Address)).(*common.Address)
	return out0, nil
}

func (e *ETHEventHandler) matchAddressWithHandlerFunc(addr common.Address) (EventHandlerFunc, error) {
	hf, ok := e.eventHandlers[addr]
	if !ok {
		return nil, errors.New("no corresponding event handler for this address exists")
	}
	return hf, nil
}

func (e *ETHEventHandler) RegisterEventHandler(address string, handler EventHandlerFunc) {
	if e.eventHandlers == nil {
		e.eventHandlers = make(map[common.Address]EventHandlerFunc)
	}
	e.eventHandlers[common.HexToAddress(address)] = handler
}

func toCallArg(msg ethereum.CallMsg) map[string]interface{} {
	arg := map[string]interface{}{
		"from": msg.From,
		"to":   msg.To,
	}
	if len(msg.Data) > 0 {
		arg["data"] = hexutil.Bytes(msg.Data)
	}
	if msg.Value != nil {
		arg["value"] = (*hexutil.Big)(msg.Value)
	}
	if msg.Gas != 0 {
		arg["gas"] = hexutil.Uint64(msg.Gas)
	}
	if msg.GasPrice != nil {
		arg["gasPrice"] = (*hexutil.Big)(msg.GasPrice)
	}
	return arg
}

func Erc20EventHandler(sourceID, destId uint8, nonce uint64, handlerContractAddress common.Address, client ChainClient) (*relayer.Message, error) {
	definition := "[{\"inputs\":[{\"internalType\":\"uint64\",\"name\":\"depositNonce\",\"type\":\"uint64\"},{\"internalType\":\"uint8\",\"name\":\"destId\",\"type\":\"uint8\"}],\"name\":\"getDepositRecord\",\"outputs\":[{\"components\":[{\"internalType\":\"address\",\"name\":\"_tokenAddress\",\"type\":\"address\"},{\"internalType\":\"uint8\",\"name\":\"_destinationChainID\",\"type\":\"uint8\"},{\"internalType\":\"bytes32\",\"name\":\"_resourceID\",\"type\":\"bytes32\"},{\"internalType\":\"bytes\",\"name\":\"_destinationRecipientAddress\",\"type\":\"bytes\"},{\"internalType\":\"address\",\"name\":\"_depositer\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"_amount\",\"type\":\"uint256\"}],\"internalType\":\"structERC20Handler.DepositRecord\",\"name\":\"\",\"type\":\"tuple\"}],\"stateMutability\":\"view\",\"type\":\"function\"}]"
	type ERC20HandlerDepositRecord struct {
		TokenAddress                common.Address
		DestinationChainID          uint8
		ResourceID                  [32]byte
		DestinationRecipientAddress []byte
		Depositer                   common.Address
		Amount                      *big.Int
	}
	a, err := abi.JSON(strings.NewReader(definition))
	input, err := a.Pack("getDepositRecord", nonce, destId)
	if err != nil {
		return nil, err
	}

	msg := ethereum.CallMsg{From: common.Address{}, To: &handlerContractAddress, Data: input}
	out, err := client.CallContract(context.TODO(), toCallArg(msg), nil)
	if err != nil {
		return nil, err
	}
	res, err := a.Unpack("getDepositRecord", out)
	if len(res) == 0 {
		return nil, errors.New("no handler associated with such resourceID")
	}

	out0 := *abi.ConvertType(res[0], new(ERC20HandlerDepositRecord)).(*ERC20HandlerDepositRecord)
	return &relayer.Message{
		Source:       sourceID,
		Destination:  destId,
		DepositNonce: nonce,
		ResourceId:   out0.ResourceID,
		Type:         relayer.FungibleTransfer,
		Payload: []interface{}{
			out0.Amount.Bytes(),
			out0.DestinationRecipientAddress,
		},
	}, nil
}
