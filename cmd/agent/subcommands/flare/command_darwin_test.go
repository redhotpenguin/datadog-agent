// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024-present Datadog, Inc.

package flare

import (
	"net/http"
	"net/http/httptest"
	"path"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/datadog-agent/pkg/config/model"
)

func sysprobeSocketPath(t *testing.T) string {
	return path.Join(t.TempDir(), "sysprobe.sock")
}

// NewSystemProbeTestServer starts a new mock server to handle System Probe requests.
func NewSystemProbeTestServer(_ http.Handler) (*httptest.Server, error) {
	return nil, nil
}

// InjectConnectionFailures injects a failure in TestReadProfileDataErrors.
func InjectConnectionFailures(_ model.Config, _ model.Config) {
	// macOS doesn't currently support any system-probe modules
}

// CheckExpectedConnectionFailures checks the expected errors after simulated
// connection failures.
func CheckExpectedConnectionFailures(c *commandTestSuite, err error) {
	// System probe by default is disabled and no connection is attempted for it in the test.
	require.Regexp(c.T(), "^4 errors occurred:\n", err.Error())
}
