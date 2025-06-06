// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build windows

// Package modules is all the module definitions for system-probe
package modules

import (
	"time"

	"github.com/DataDog/datadog-agent/pkg/system-probe/api/module"
	"github.com/DataDog/datadog-agent/pkg/system-probe/config"
	"github.com/DataDog/datadog-agent/pkg/util/winutil"
	"github.com/DataDog/datadog-agent/pkg/util/winutil/messagestrings"
)

// All System Probe modules should register their factories here
var All = []module.Factory{
	NetworkTracer,
	// there is a dependency from EventMonitor -> NetworkTracer
	// so EventMonitor has to follow NetworkTracer
	EventMonitor,
	WinCrashProbe,
	Traceroute,
}

func inactivityEventLog(duration time.Duration) {
	winutil.LogEventViewer(config.ServiceName, messagestrings.MSG_SYSPROBE_RESTART_INACTIVITY, duration.String())
}
