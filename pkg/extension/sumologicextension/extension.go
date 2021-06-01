// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sumologicextension

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.uber.org/zap"
)

type SumologicExtension struct {
	baseUrl          string
	conf             *Config
	logger           *zap.Logger
	registrationInfo OpenRegisterResponsePayload
	closeChan        chan struct{}
	closeOnce        sync.Once
}

const (
	// TODO: fix
	niteBaseUrl              = "https://nite-open-events.sumologic.net"
	heartbeatUrl             = "/api/v1/collector/heartbeat"
	registerUrl              = "/api/v1/collector/register"
	defaultHeartbeatInterval = 15 * time.Second
)

func newSumologicExtension(conf *Config, logger *zap.Logger) (*SumologicExtension, error) {
	if conf.CollectorName == "" {
		return nil, errors.New("collector name is unset")
	}
	if conf.HeartBeatInterval <= 0 {
		conf.HeartBeatInterval = defaultHeartbeatInterval
	}

	return &SumologicExtension{
		// TODO: don't hardcode
		baseUrl:   niteBaseUrl,
		conf:      conf,
		logger:    logger,
		closeChan: make(chan struct{}),
	}, nil
}

func (se *SumologicExtension) Start(ctx context.Context, host component.Host) error {
	// TODO: handle already registered collector; retrieve credentials etc.
	if err := se.register(ctx); err != nil {
		return err
	}
	go se.heartbeatLoop()

	return nil
}

// Shutdown is invoked during service shutdown.
func (se *SumologicExtension) Shutdown(ctx context.Context) error {
	se.closeOnce.Do(func() { close(se.closeChan) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

type FullRegisterKeyResponse struct {
	CollectorID   int64         `json:"collectorId"`
	CredentialID  string        `json:"credentialId"`
	CredentialKey string        `json:"credentialKey"`
	CustomerID    int64         `json:"customerId"`
	Errors        []interface{} `json:"errors"`
	HTTPCode      int64         `json:"httpCode"`
	Warnings      []interface{} `json:"warnings"`
}

type OpenRegisterRequestPayload struct {
	CollectorName string `json:"collectorName"`
	Ephemeral     bool   `json:"ephemeral"`
	Description   string `json:"description"`
	Hostname      string `json:"hostname"`
	Category      string `json:"category"`
	Timezone      string `json:"timeZone"`
}

type OpenRegisterResponsePayload struct {
	CollectorCredentialId  string `json:"collectorCredentialId"`
	CollectorCredentialKey string `json:"collectorCredentialKey"`
	CollectorId            string `json:"collectorId"`
}

func (se *SumologicExtension) register(ctx context.Context) error {
	u, err := url.Parse(se.baseUrl)
	if err != nil {
		return err
	}
	u.Path = registerUrl

	// TODO: just plain hostname or we want to add some custom logic when setting
	// hostname in request?
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("cannot get hostname: %w", err)
	}

	var buff bytes.Buffer
	if err = json.NewEncoder(&buff).Encode(OpenRegisterRequestPayload{
		Ephemeral:     true, // TODO: change that
		CollectorName: se.conf.CollectorName,
		Description:   se.conf.CollectorDescription,
		Category:      se.conf.CollectorCategory,
		Hostname:      hostname,
	}); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), &buff)
	if err != nil {
		return err
	}

	addClientCredentials(req,
		se.conf.Credentials.AccessID,
		se.conf.Credentials.AccessKey,
	)
	addJSONHeaders(req)

	se.logger.Info("Calling register API", zap.String("URL", u.String()))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to register the collector: %w", err)
	}

	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 400 {
		var buff bytes.Buffer
		if _, err := io.Copy(&buff, res.Body); err != nil {
			return fmt.Errorf(
				"failed to copy collector registration response body, status code: %d, err: %w",
				res.StatusCode, err,
			)
		}
		se.logger.Error("Collector registration failed",
			zap.Int("response status code", res.StatusCode),
			zap.String("response", buff.String()),
		)
		return nil
	}

	var resp OpenRegisterResponsePayload
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return err
	}

	se.logger.Info("Collector registered",
		zap.String("CollectorID", resp.CollectorId),
		zap.Any("response", resp),
	)

	se.registrationInfo = resp

	return nil
}

func (se *SumologicExtension) heartbeatLoop() {
	if se.registrationInfo.CollectorCredentialId == "" || se.registrationInfo.CollectorId == "" {
		se.logger.Error("Collector not registered, cannot send heartbeat")
		return
	}

	se.logger.Info("Heartbeat heartbeat API initialized. Starting sending hearbeat requests")
	for {
		select {
		case <-se.closeChan:
			se.logger.Info("Heartbeat sender turn off")
			return
		default:
			err := se.sendHeartbeat()
			if err != nil {
				se.logger.Error("Heartbeat error: ", zap.String("error: ", err.Error()))
			}
			se.logger.Debug("Heartbeat sent")
			select {
			case <-time.After(se.conf.HeartBeatInterval):
			case <-se.closeChan:
			}
		}
	}
}

func (se *SumologicExtension) sendHeartbeat() error {
	u, err := url.Parse(se.baseUrl + heartbeatUrl)
	if err != nil {
		return fmt.Errorf("unable to parse heartbeat URL %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, u.String(), nil)
	if err != nil {
		return fmt.Errorf("unable to create HTTP request %w", err)
	}

	addCollectorCredentials(req,
		se.registrationInfo.CollectorCredentialId,
		se.registrationInfo.CollectorCredentialKey,
	)
	addJSONHeaders(req)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("unable to send HTTP request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 204 {
		var buff bytes.Buffer
		if _, err := io.Copy(&buff, res.Body); err != nil {
			return fmt.Errorf(
				"failed to copy collector heartbeat response body, status code: %d, err: %w",
				res.StatusCode, err,
			)
		}
		return fmt.Errorf(
			"collector heartbeat request failed, status code: %d, body: %s",
			res.StatusCode, buff.String(),
		)
	}
	return nil

}

func (se *SumologicExtension) CollectorID() string {
	return se.registrationInfo.CollectorId
}

func (se *SumologicExtension) BaseUrl() string {
	return se.baseUrl
}

// Implement [1] in order for this extension to be used as custom exporter
// authenticator.
//
// [1]: https://github.com/open-telemetry/opentelemetry-collector/blob/2e84285efc665798d76773b9901727e8836e9d8f/config/configauth/clientauth.go#L34-L39
func (se *SumologicExtension) RoundTripper(base http.RoundTripper) (http.RoundTripper, error) {
	return roundTripper{
		collectorCredentialId:  se.registrationInfo.CollectorCredentialId,
		collectorCredentialKey: se.registrationInfo.CollectorCredentialKey,
		base:                   base,
	}, nil
}

type roundTripper struct {
	collectorCredentialId  string
	collectorCredentialKey string
	base                   http.RoundTripper
}

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	addCollectorCredentials(req, rt.collectorCredentialId, rt.collectorCredentialKey)

	return rt.base.RoundTrip(req)
}

func addClientCredentials(req *http.Request, accessID string, accessKey string) {
	// TODO: What is preferred: headers of basic auth?
	req.Header.Add("accessid", accessID)
	req.Header.Add("accesskey", accessKey)
}

func addJSONHeaders(req *http.Request) {
	req.Header.Add("Content-Type", "application/json")
}

func addCollectorCredentials(req *http.Request, collectorCredentialId string, collectorCredentialKey string) {
	req.Header.Add("collectorCredentialId", collectorCredentialId)
	req.Header.Add("collectorCredentialKey", collectorCredentialKey)
}
