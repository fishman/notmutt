// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua && crm

package crm

import (
	"context"
	"sync"

	"notmutt/core"
)

// pullMu serializes pull runs: the app adapter launches RunPull on a fresh
// goroutine per pull request, and an overlapping run no-ops (the refresher's
// non-blocking guard, refresh.go) instead of stacking ListUnprocessed calls
// against the CRM.
var pullMu sync.Mutex

// RunPull lists the client's unprocessed contacts and publishes them as
// core.CrmContact rows in one core.CrmQueue page (the R3 refresh shape: each
// pull publishes its own page the queue view diff-inserts, never a rebuild).
// marker and createdAfter pass through to ListUnprocessed unchanged;
// createdAfter may be empty. Rows carry client.Provider() as their routing id
// - never a vendor literal. Search rows carry no company association, so
// Company stays empty (analyze refetches and fills it) and Status starts at
// statusNew. A ListUnprocessed error publishes CrmRowError and stops; rows
// already published stay visible.
func RunPull(bus *core.Bus, client Client, marker, createdAfter string) {
	if !pullMu.TryLock() {
		return
	}
	defer pullMu.Unlock()

	provider := client.Provider()
	contacts, err := client.ListUnprocessed(context.Background(), marker, createdAfter)
	if err != nil {
		bus.Publish(core.CrmRowError{Provider: provider, Err: err})
		return
	}
	rows := make([]core.CrmContact, 0, len(contacts))
	for _, c := range contacts {
		rows = append(rows, core.CrmContact{
			Provider:  provider,
			ID:        c.ID,
			Email:     c.Email,
			First:     c.FirstName,
			Last:      c.LastName,
			Title:     c.JobTitle,
			CreatedAt: c.CreatedAt,
			Status:    statusNew,
		})
	}
	bus.Publish(core.CrmQueue{Contacts: rows})
}
