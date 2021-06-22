package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type GeneralChainConfig struct {
	Name           string `mapstructure:"name"`
	Type           string `mapstructure:"type"`
	Id             *uint8 `mapstructure:"id"`
	Endpoint       string `mapstructure:"endpoint"`
	From           string `mapstructure:"from"`
	KeystorePath   string
	BlockstorePath string
	FreshStart     bool
	LatestBlock    bool
}

func (c *GeneralChainConfig) Validate() error {
	// viper defaults to 0 for not specified ints, but we must have a valid chain id
	// Previous method of checking used a string cast like below
	//chainId := string(c.Id)
	if c.Id == nil {
		return fmt.Errorf("required field chain.Id empty for chain %v", c.Id)
	}
	if c.Type == "" {
		return fmt.Errorf("required field chain.Type empty for chain %v", *c.Id)
	}
	if c.Endpoint == "" {
		return fmt.Errorf("required field chain.Endpoint empty for chain %v", *c.Id)
	}
	if c.Name == "" {
		return fmt.Errorf("required field chain.Name empty for chain %v", *c.Id)
	}
	if c.From == "" {
		return fmt.Errorf("required field chain.From empty for chain %v", *c.Id)
	}
	return nil
}

func (c *GeneralChainConfig) ParseConfig() {
	c.KeystorePath = viper.GetString(KeystoreFlagName)
	c.BlockstorePath = viper.GetString(BlockstoreFlagName)
	c.FreshStart = viper.GetBool(FreshStartFlagName)
	c.LatestBlock = viper.GetBool(LatestBlockFlagName)
}
