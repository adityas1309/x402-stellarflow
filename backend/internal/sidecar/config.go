// Package sidecar implements a lightweight x402 reverse-proxy that sits
// in front of any HTTP backend and handles payment verification + settlement.
// No database, no Redis, no worker — just YAML config + facilitator + proxy.
package sidecar

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// EndpointDef is one paid endpoint defined in the YAML config.
type EndpointDef struct {
	Path        string  `yaml:"path"`
	Method      string  `yaml:"method"`
	PriceUSDC   float64 `yaml:"price_usdc"`
	Description string  `yaml:"description"`
}

// EndpointsConfig is the top-level YAML structure.
type EndpointsConfig struct {
	Endpoints []EndpointDef `yaml:"endpoints"`
}

// LoadEndpoints reads the YAML file and returns the parsed config.
func LoadEndpoints(path string) (*EndpointsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read endpoints file %q: %w", path, err)
	}
	var cfg EndpointsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse endpoints file %q: %w", path, err)
	}
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("endpoints file %q has no endpoints defined", path)
	}
	for i, ep := range cfg.Endpoints {
		if ep.Path == "" {
			return nil, fmt.Errorf("endpoint %d has no path", i)
		}
		if ep.Method == "" {
			cfg.Endpoints[i].Method = "POST"
		}
		if ep.PriceUSDC <= 0 {
			return nil, fmt.Errorf("endpoint %q has invalid price_usdc: %f", ep.Path, ep.PriceUSDC)
		}
	}
	return &cfg, nil
}
