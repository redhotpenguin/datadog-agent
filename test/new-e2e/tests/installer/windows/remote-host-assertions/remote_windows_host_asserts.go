// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package assertions

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/DataDog/datadog-agent/test/new-e2e/pkg/components"
	"github.com/DataDog/datadog-agent/test/new-e2e/tests/installer/windows/consts"
	"github.com/DataDog/datadog-agent/test/new-e2e/tests/windows/common"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	defaultAgentBinPath = "C:\\Program Files\\Datadog\\Datadog Agent\\bin\\agent.exe"
)

// RemoteWindowsHostAssertions is a type that extends the SuiteAssertions to add assertions
// executing on a RemoteHost.
type RemoteWindowsHostAssertions struct {
	// Don't embed the "require.Assertions" type because that could confuse the caller as to which code executes
	// on the remoteHost vs locally.
	// With a "private" require.Assertions, when the caller uses suite.Require().Host() they will
	// only get access to the assertions that can effectively run on the remote server, preventing
	// accidental misuse.
	require    *require.Assertions
	suite      suite.TestingSuite
	remoteHost *components.RemoteHost
}

// New returns a new RemoteWindowsHostAssertions
func New(assertions *require.Assertions, suite suite.TestingSuite, remoteHost *components.RemoteHost) *RemoteWindowsHostAssertions {
	return &RemoteWindowsHostAssertions{
		require:    assertions,
		suite:      suite,
		remoteHost: remoteHost,
	}
}

// HasAService returns an assertion object that can be used to assert things about
// a given Windows service. If the service doesn't exist, it fails.
func (r *RemoteWindowsHostAssertions) HasAService(serviceName string) *RemoteWindowsServiceAssertions {
	r.suite.T().Helper()
	serviceConfig, err := common.GetServiceConfig(r.remoteHost, serviceName)
	r.require.NoError(err)
	return &RemoteWindowsServiceAssertions{RemoteWindowsHostAssertions: r, serviceConfig: serviceConfig}
}

// HasNoService returns an assertion object that can be used to assert things about
// a given Windows service. If the service doesn't exist, it fails.
func (r *RemoteWindowsHostAssertions) HasNoService(serviceName string) *RemoteWindowsHostAssertions {
	r.suite.T().Helper()
	_, err := common.GetServiceConfig(r.remoteHost, serviceName)
	r.require.Error(err)
	return r
}

// DirExists checks whether a directory exists in the given path. It also fails if
// the path points to a directory or there is an error when trying to check the file.
func (r *RemoteWindowsHostAssertions) DirExists(path string, msgAndArgs ...interface{}) *RemoteWindowsHostAssertions {
	r.suite.T().Helper()
	_, err := r.remoteHost.Lstat(path)
	r.require.NoError(err, msgAndArgs...)
	return r
}

// NoDirExists checks whether a directory does not exist in the given path.
func (r *RemoteWindowsHostAssertions) NoDirExists(path string, msgAndArgs ...interface{}) *RemoteWindowsHostAssertions {
	r.suite.T().Helper()
	_, err := r.remoteHost.Lstat(path)
	r.require.ErrorIs(err, fs.ErrNotExist, msgAndArgs...)
	return r
}

// FileExists checks whether a file exists in the given path. It also fails if
// the path points to a directory or there is an error when trying to check the file.
func (r *RemoteWindowsHostAssertions) FileExists(path string, msgAndArgs ...interface{}) *RemoteWindowsHostAssertions {
	r.suite.T().Helper()
	exists, err := r.remoteHost.FileExists(path)
	r.require.NoError(err)
	r.require.True(exists, msgAndArgs...)
	return r
}

// NoFileExists checks whether a file does not exist in the given path. It also fails if
// the path points to a directory or there is an error when trying to check the file.
func (r *RemoteWindowsHostAssertions) NoFileExists(path string, msgAndArgs ...interface{}) *RemoteWindowsHostAssertions {
	r.suite.T().Helper()
	exists, err := r.remoteHost.FileExists(path)
	r.require.NoError(err)
	r.require.False(exists, msgAndArgs...)
	return r
}

// HasARunningDatadogAgentService checks if the remote host has a Datadog Agent installed & running.
// It does not run a full test suite on it, but merely checks if it has the required
// service running.
func (r *RemoteWindowsHostAssertions) HasARunningDatadogAgentService() *RemoteWindowsBinaryAssertions {
	r.suite.T().Helper()
	r.FileExists(defaultAgentBinPath)
	r.HasAService("datadogagent").WithStatus("Running")
	return &RemoteWindowsBinaryAssertions{
		RemoteWindowsHostAssertions: r,
		binaryPath:                  defaultAgentBinPath,
	}
}

// HasNoDatadogAgentService checks if the remote host doesn't have a Datadog Agent installed.
func (r *RemoteWindowsHostAssertions) HasNoDatadogAgentService() *RemoteWindowsBinaryAssertions {
	r.suite.T().Helper()
	r.NoFileExists(defaultAgentBinPath)
	r.HasNoService("datadogagent")
	return &RemoteWindowsBinaryAssertions{
		RemoteWindowsHostAssertions: r,
		binaryPath:                  defaultAgentBinPath,
	}
}

// HasBinary checks if a binary exists on the remote host and returns a more specific assertion
// allowing to run further tests on the binary.
func (r *RemoteWindowsHostAssertions) HasBinary(path string) *RemoteWindowsBinaryAssertions {
	r.suite.T().Helper()
	r.FileExists(path)
	return &RemoteWindowsBinaryAssertions{
		RemoteWindowsHostAssertions: r,
		binaryPath:                  path,
	}
}

// HasRegistryKey checks if a registry key exists on the remote host.
func (r *RemoteWindowsHostAssertions) HasRegistryKey(key string) *RemoteWindowsRegistryKeyAssertions {
	r.suite.T().Helper()
	exists, err := common.RegistryKeyExists(r.remoteHost, key)
	r.require.NoError(err)
	r.require.True(exists)
	return &RemoteWindowsRegistryKeyAssertions{
		RemoteWindowsHostAssertions: r,
		keyPath:                     key,
	}
}

// HasNoRegistryKey checks if a registry key does not exist on the remote host.
func (r *RemoteWindowsHostAssertions) HasNoRegistryKey(key string) *RemoteWindowsHostAssertions {
	r.suite.T().Helper()
	exists, err := common.RegistryKeyExists(r.remoteHost, key)
	r.require.NoError(err)
	r.require.False(exists)
	return r
}

// HasNamedPipe checks if a named pipe exists on the remote host
func (r *RemoteWindowsHostAssertions) HasNamedPipe(pipeName string) *RemoteWindowsNamedPipeAssertions {
	r.suite.T().Helper()

	cmd := fmt.Sprintf("Test-Path '%s'", pipeName)
	out, err := r.remoteHost.Execute(cmd)
	r.require.NoError(err)
	out = strings.TrimSpace(out)
	r.require.Equal("True", out)

	return &RemoteWindowsNamedPipeAssertions{
		RemoteWindowsHostAssertions: r,
		pipename:                    pipeName,
	}
}

// HasNoNamedPipe checks if a named pipe does not exist on the remote host
func (r *RemoteWindowsHostAssertions) HasNoNamedPipe(pipeName string) *RemoteWindowsHostAssertions {
	r.suite.T().Helper()

	cmd := fmt.Sprintf("Test-Path '%s'", pipeName)
	out, err := r.remoteHost.Execute(cmd)
	r.require.NoError(err)
	out = strings.TrimSpace(out)
	r.require.Equal("False", out)

	return r
}

// HasARunningDatadogInstallerService verifies that the Datadog Installer service is installed and correctly configured.
func (r *RemoteWindowsHostAssertions) HasARunningDatadogInstallerService() *RemoteWindowsHostAssertions {
	r.suite.T().Helper()

	r.HasAService(consts.ServiceName).
		WithStatus("Running").
		HasNamedPipe(consts.NamedPipe).
		WithSecurity(
			// Only accessible to Administrators and LocalSystem
			common.NewProtectedSecurityInfo(
				common.GetIdentityForSID(common.AdministratorsSID),
				common.GetIdentityForSID(common.LocalSystemSID),
				[]common.AccessRule{
					common.NewExplicitAccessRule(
						common.GetIdentityForSID(common.LocalSystemSID),
						common.FileFullControl,
						common.AccessControlTypeAllow,
					),
					common.NewExplicitAccessRule(
						common.GetIdentityForSID(common.AdministratorsSID),
						common.FileFullControl,
						common.AccessControlTypeAllow,
					),
				},
			))
	return r
}

// HasDatadogInstaller verifies that the Datadog Installer is installed on the remote host.
func (r *RemoteWindowsHostAssertions) HasDatadogInstaller() *RemoteWindowsInstallerAssertions {
	r.suite.T().Helper()

	// TODO: custom install path
	bin := r.HasBinary(consts.BinaryPath)
	return &RemoteWindowsInstallerAssertions{
		RemoteWindowsBinaryAssertions: bin,
	}
}
