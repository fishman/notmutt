// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua && crm

package crm

// statusNew is a queue row's starting status: pulled and awaiting analysis.
// The later workflow jobs advance it to briefing/drafted/sent/dismissed.
const statusNew = "new"
