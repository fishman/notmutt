// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua && crm

// Package crm holds the CRM follow-up workflow's vendor-neutral core: the
// Client interface the workflow drives plus the Contact/Company domain
// types. No wire code lives here - vendor implementations sit in sibling
// subpackages (lib/crm/hubspot) and satisfy Client.
package crm

import (
	"context"
	"time"
)

// Client is the CRM the workflow drives. Provider() is the routing id
// stamped on every published row/briefing/draft ("hubspot" today).
type Client interface {
	Provider() string
	ListUnprocessed(ctx context.Context, marker, createdAfter string) ([]Contact, error)
	Contact(ctx context.Context, id string) (Contact, error)
	Company(ctx context.Context, id string) (Company, error)
	MarkFollowedUp(ctx context.Context, id, marker string) error
}

// Contact is one CRM contact, with the id of its primary associated
// company when one is present.
type Contact struct {
	ID, Email, FirstName, LastName, JobTitle string
	CompanyID                                string
	CreatedAt                                time.Time
}

// Company is one CRM company.
type Company struct {
	ID, Name, Domain, Industry, Description string
}
