package config

import (
	"fmt"
	"math/big"
)

const DefaultGasLimit = 6721975
const DefaultGasPrice = 20000000000
const DefaultGasMultiplier = 1
const DefaultBlockConfirmations = 10

type SharedEVMConfig struct {
	GeneralChainConfig GeneralChainConfig
	Bridge             string
	Erc20Handler       string
	Erc721Handler      string
	GenericHandler     string
	MaxGasPrice        *big.Int
	GasMultiplier      *big.Float
	GasLimit           *big.Int
	StartBlock         *big.Int
	BlockConfirmations *big.Int
}

type RawSharedEVMConfig struct {
	GeneralChainConfig `mapstructure:",squash"`
	Bridge             string  `mapstructure:"bridge"`
	Erc20Handler       string  `mapstructure:"erc20Handler"`
	Erc721Handler      string  `mapstructure:"erc721Handler"`
	GenericHandler     string  `mapstructure:"genericHandler"`
	MaxGasPrice        int64   `mapstructure:"maxGasPrice"`
	GasMultiplier      float64 `mapstructure:"gasMultiplier"`
	GasLimit           int64   `mapstructure:"gasLimit"`
	StartBlock         int64   `mapstructure:"startBlock"`
	BlockConfirmations int64   `mapstructure:"blockConfirmations"`
}

func (c *RawSharedEVMConfig) Validate() error {
	if err := c.GeneralChainConfig.Validate(); err != nil {
		return err
	}
	if c.Bridge == "" {
		return fmt.Errorf("required field chain.Bridge empty for chain %v", *c.Id)
	}
	return nil
}

func (c *RawSharedEVMConfig) ParseConfig() (*SharedEVMConfig, error) {

	c.GeneralChainConfig.ParseConfig()

	config := &SharedEVMConfig{
		GeneralChainConfig: c.GeneralChainConfig,
		Erc20Handler:       c.Erc20Handler,
		Erc721Handler:      c.Erc721Handler,
		GenericHandler:     c.GenericHandler,
		GasLimit:           big.NewInt(DefaultGasLimit),
		MaxGasPrice:        big.NewInt(DefaultGasPrice),
		GasMultiplier:      big.NewFloat(DefaultGasMultiplier),
		StartBlock:         big.NewInt(c.StartBlock),
		BlockConfirmations: big.NewInt(DefaultBlockConfirmations),
	}

	if c.Bridge != "" {
		config.Bridge = c.Bridge
	} else {
		return nil, fmt.Errorf("must provide opts.bridge field for ethereum config")
	}

	if c.GasLimit != 0 {
		config.GasLimit = big.NewInt(c.GasLimit)
	}

	// Allow gas price of zero for testing with Optimism until they integrate gas prices
	// if c.MaxGasPrice != 0 {
	// 	config.MaxGasPrice = big.NewInt(c.MaxGasPrice)
	// }
	config.MaxGasPrice = big.NewInt(c.MaxGasPrice)

	if c.GasMultiplier != 0 {
		config.GasMultiplier = big.NewFloat(c.GasMultiplier)
	}

	if c.BlockConfirmations != 0 {
		config.BlockConfirmations = big.NewInt(c.BlockConfirmations)
	}

	return config, nil
}
