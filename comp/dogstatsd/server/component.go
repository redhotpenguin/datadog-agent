// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Package server implements a component to run the dogstatsd server
package server

import (
	"time"

	"github.com/DataDog/datadog-agent/pkg/util/fxutil"
	"go.uber.org/fx"
)

// team: agent-metric-pipelines

// Component is the component type.
type Component interface {
	// IsRunning returns true if the server is running
	IsRunning() bool

	// ServerlessFlush flushes all the data to the aggregator to them send it to the Datadog intake.
	ServerlessFlush(time.Duration)

	// SetExtraTags sets extra tags. All metrics sent to the DogstatsD will be tagged with them.
	SetExtraTags(tags []string)

	// UDPLocalAddr returns the local address of the UDP statsd listener, if enabled.
	UDPLocalAddr() string
}

// Mock implements mock-specific methods.
type Mock interface {
	Component
}

// Module defines the fx options for this component.
func Module(params Params) fxutil.Module {
	return fxutil.Component(
		fx.Provide(newServer),
		fx.Supply(params))
}

// MockModule defines the fx options for the mock component.
func MockModule() fxutil.Module {
	return fxutil.Component(
		fx.Provide(newMock))
}
