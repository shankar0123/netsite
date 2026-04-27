// Copyright 2026 Shankar Reddy. All Rights Reserved.
//
// Licensed under the Business Source License 1.1 (the "License").
// You may not use this file except in compliance with the License.
// A copy of the License is bundled with this distribution at ./LICENSE
// in the repository root, or available at https://mariadb.com/bsl11/.
//
// Licensed Work:  NetSite
// Change Date:    2125-01-01
// Change License: Apache License, Version 2.0
//
// On the Change Date, the rights granted in this License terminate and
// you are granted rights under the Change License instead.

// Package popagent implements the NetSite POP-side agent that runs
// canaries on a schedule and publishes results to NATS JetStream.
//
// What: a Scheduler that fires Tests at their configured interval, a
// Publisher that ships Results to JetStream, and a Config loader that
// reads test definitions from YAML.
//
// How: each Test from the loaded Config is given its own goroutine
// driving a per-test ticker; per-test goroutines call into a Runner
// (chosen by Test.Kind) and forward the Result to the Publisher. The
// scheduler is the only place that holds a clock; the Runner is
// pure-stateless logic.
//
// Why a YAML file rather than the gRPC config-pull originally listed
// in PROJECT_STATE §7 task 0.17: per OQ-03 (PROJECT_STATE §5), Phase
// 0 uses HTTP/file config because the control plane does not yet
// have a config-vending endpoint. Adding gRPC would be premature at
// this stage; we ship YAML and revisit when there are multiple agent
// types (ns-pop, ns-bgp, ns-flow) that benefit from a unified
// pull-based protocol.
package popagent
