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
package mysqlrecordsreceiver

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
)

func TestMySQLReceiverIntegration(t *testing.T) {

	t.Run("Running mysql version 8.0", func(t *testing.T) {
		t.Parallel()
		container := getContainer(t, containerRequest8_0)
		defer func() {
			require.NoError(t, container.Terminate(context.Background()))
		}()
		hostname, err := container.Host(context.Background())
		require.NoError(t, err)

		f := NewFactory()
		cfg := f.CreateDefaultConfig().(*Config)
		cfg.Endpoint = net.JoinHostPort(hostname, "3306")
		cfg.Username = "otel"
		cfg.Password = "otel"
		cfg.Database = "information_schema"
		cfg.DBQueries = make([]DBQueries, 1)
		cfg.DBQueries[0].QueryId = "Q1"
		cfg.DBQueries[0].Query = "Show tables where Tables_in_information_schema='INNODB_TABLES'"

		consumer := new(consumertest.LogsSink)
		settings := componenttest.NewNopReceiverCreateSettings()
		receiver, err := f.CreateLogsReceiver(context.Background(), settings, cfg, consumer)
		require.NoError(t, err, "failed creating logs receiver")
		require.NoError(t, receiver.Start(context.Background(), componenttest.NewNopHost()))
		require.Eventuallyf(t, func() bool {
			return len(consumer.AllLogs()) > 0
		}, 2*time.Minute, 1*time.Second, "failed to receive more than 0 logs")
		actualLog := consumer.AllLogs()[0]
		logsMarshaler := plog.NewJSONMarshaler()
		buf, err := logsMarshaler.MarshalLogs(actualLog)
		require.NoError(t, err, "failed marshalling log record")
		actualRecord := bytes.NewBuffer(buf).String()
		expectedRecord, err := os.ReadFile(filepath.Join("testdata", "integration", "expected_mysql.8_0.json"))
		require.NoError(t, err, "failed reading expected log record")
		require.NotEmpty(t, actualRecord)
		require.EqualValues(t, string(expectedRecord), actualRecord)
		require.NoError(t, receiver.Shutdown(context.Background()))
	})
}

var (
	containerRequest8_0 = testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    filepath.Join("testdata", "integration"),
			Dockerfile: "Dockerfile.mysql.8_0",
		},
		ExposedPorts: []string{"3306:3306"},
		WaitingFor: wait.ForListeningPort("3306").
			WithStartupTimeout(2 * time.Minute),
	}
)

func getContainer(t *testing.T, req testcontainers.ContainerRequest) testcontainers.Container {
	require.NoError(t, req.Validate())
	container, err := testcontainers.GenericContainer(
		context.Background(),
		testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
	require.NoError(t, err)

	code, err := container.Exec(context.Background(), []string{"/setup.sh"})
	require.NoError(t, err)
	require.Equal(t, 0, code)
	return container
}
