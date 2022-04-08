// Copyright 2021, OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rawk8seventsreceiver

import (
	"time"

	"go.opentelemetry.io/collector/config"
	k8s "k8s.io/client-go/kubernetes"
)

// Config defines configuration for the receiver.
type Config struct {
	config.ReceiverSettings `mapstructure:",squash"`
	APIConfig               `mapstructure:",squash"`
	// List of ‘namespaces’ to collect events from.
	Namespaces []string `mapstructure:"namespaces"`

	// For mocking
	makeClient func(apiConf APIConfig) (k8s.Interface, error)

	// ConsumeRetryDelay is the retry delay for recoverable pipeline errors
	// one frequent source of these kinds of errors is the memory_limiter processor
	ConsumeRetryDelay time.Duration `mapstructure:"consume_retry_delay"`

	// ConsumeMaxRetries is the maximum number of retries for recoverable pipeline errors
	ConsumeMaxRetries uint64 `mapstructure:"consume_max_retries"`
}

// Validate checks if the receiver configuration is valid
func (cfg *Config) Validate() error {
	if err := cfg.ReceiverSettings.Validate(); err != nil {
		return err
	}
	return cfg.APIConfig.Validate()
}
