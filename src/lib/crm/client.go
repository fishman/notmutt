// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

// Package crm holds the CRM follow-up workflow's provider boundary: vendor
// clients speak only HTTP+JSON here - no app/ai imports - so the bearer key
// arrives already resolved (NewClient) and vendor marker properties are
// caller data, never client config. HubSpot HTTP 429/5xx map to ErrRetry;
// the caller owns retry policy. Core bus types (core.CrmContact and
// friends) are the neutral domain surface; this file is the HubSpot wire
// surface (vendor-neutral types live in core).
package crm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const apiBase = "https://api.hubspot.com"

// ErrRetry marks a HubSpot failure the caller may retry (HTTP 429 or any
// 5xx); the caller decides the backoff.
var ErrRetry = errors.New("crm: retryable hubspot error")

// Client is a HubSpot CRM v3 client.
type Client struct {
	key  []byte
	base string // apiBase, overridable in tests
	hc   *http.Client
}

// Contact is one HubSpot contact, with the id of its primary associated
// company when one is present.
type Contact struct {
	ID, Email, FirstName, LastName, JobTitle string
	CompanyID                                string
	CreatedAt                                time.Time
}

// Company is one HubSpot company.
type Company struct {
	ID, Name, Domain, Industry, Description string
}

// NewClient returns a HubSpot CRM client. The bearer key is used verbatim
// on every request; ctx is accepted for call-site symmetry.
func NewClient(_ context.Context, key []byte) *Client {
	return &Client{
		key:  key,
		base: apiBase,
		// Per-request cap mirroring the ai-package posture; a caller ctx
		// with an earlier deadline still wins.
		hc: &http.Client{Timeout: 30 * time.Second},
	}
}

// ListUnprocessed pages contacts lacking the marker property, newest
// first, limit 100 per page, following paging.next.after until absent.
// createdAfter, when non-empty, is an RFC3339 instant narrowing the search
// to contacts created after it (HubSpot date filters take epoch-ms, so the
// client converts).
func (c *Client) ListUnprocessed(ctx context.Context, marker, createdAfter string) ([]Contact, error) {
	filters := []filter{{PropertyName: marker, Operator: "NOT_HAS_PROPERTY"}}
	if createdAfter != "" {
		t, err := time.Parse(time.RFC3339, createdAfter)
		if err != nil {
			return nil, fmt.Errorf("hubspot: created_after %q is not RFC3339: %v", createdAfter, err)
		}
		filters = append(filters, filter{
			PropertyName: "createdate",
			Operator:     "GT",
			Value:        strconv.FormatInt(t.UnixMilli(), 10),
		})
	}
	body := searchRequest{
		FilterGroups: []filterGroup{{Filters: filters}},
		Sorts:        []sortSpec{{PropertyName: "createdate", Direction: "DESCENDING"}},
		Limit:        100,
	}
	var contacts []Contact
	for {
		var page searchResponse
		if err := c.do(ctx, http.MethodPost, "/crm/v3/objects/contacts/search", body, &page); err != nil {
			return nil, err
		}
		for _, w := range page.Results {
			contacts = append(contacts, w.contact())
		}
		if page.Paging == nil || page.Paging.Next == nil || page.Paging.Next.After == "" {
			return contacts, nil
		}
		body.After = page.Paging.Next.After
	}
}

// Contact returns one contact by id plus its primary associated company's
// id (CompanyID is empty when there is no association).
func (c *Client) Contact(ctx context.Context, id string) (Contact, error) {
	var w contactWire
	path := "/crm/v3/objects/contacts/" + id + "?associations=company"
	if err := c.do(ctx, http.MethodGet, path, nil, &w); err != nil {
		return Contact{}, err
	}
	return w.contact(), nil
}

// Company returns one company by id.
func (c *Client) Company(ctx context.Context, id string) (Company, error) {
	var w companyWire
	path := "/crm/v3/objects/companies/" + id
	if err := c.do(ctx, http.MethodGet, path, nil, &w); err != nil {
		return Company{}, err
	}
	return Company{
		ID:          w.ID,
		Name:        w.Properties["name"],
		Domain:      w.Properties["domain"],
		Industry:    w.Properties["industry"],
		Description: w.Properties["description"],
	}, nil
}

// MarkFollowedUp records a follow-up by setting the marker property to
// "true" on the contact.
func (c *Client) MarkFollowedUp(ctx context.Context, id, marker string) error {
	body := struct {
		Properties map[string]string `json:"properties"`
	}{Properties: map[string]string{marker: "true"}}
	return c.do(ctx, http.MethodPatch, "/crm/v3/objects/contacts/"+id, body, nil)
}

// wire request/response types (HubSpot CRM v3 shapes).

type searchRequest struct {
	FilterGroups []filterGroup `json:"filterGroups"`
	Sorts        []sortSpec    `json:"sorts"`
	Limit        int           `json:"limit"`
	After        string        `json:"after,omitempty"`
}

type filterGroup struct {
	Filters []filter `json:"filters"`
}

// filter mirrors HubSpot's search filter. Value is omitted for operators
// that take none (NOT_HAS_PROPERTY errors on a blank value field).
type filter struct {
	PropertyName string `json:"propertyName"`
	Operator     string `json:"operator"`
	Value        string `json:"value,omitempty"`
}

type sortSpec struct {
	PropertyName string `json:"propertyName"`
	Direction    string `json:"direction"`
}

type searchResponse struct {
	Results []contactWire `json:"results"`
	Paging  *paging       `json:"paging"`
}

type paging struct {
	Next *pagingNext `json:"next"`
}

type pagingNext struct {
	After string `json:"after"`
}

type contactWire struct {
	ID           string            `json:"id"`
	Properties   map[string]string `json:"properties"`
	CreatedAt    string            `json:"createdAt"` // top-level, ISO-8601
	Associations *assocWire        `json:"associations"`
}

func (w contactWire) contact() Contact {
	c := Contact{
		ID:        w.ID,
		Email:     w.Properties["email"],
		FirstName: w.Properties["firstname"],
		LastName:  w.Properties["lastname"],
		JobTitle:  w.Properties["jobtitle"],
		CreatedAt: parseDate(w.CreatedAt),
	}
	if a := w.Associations; a != nil && a.Company != nil && len(a.Company.Results) > 0 {
		c.CompanyID = a.Company.Results[0].ID
	}
	return c
}

type companyWire struct {
	ID         string            `json:"id"`
	Properties map[string]string `json:"properties"`
}

type assocWire struct {
	Company *assocResults `json:"company"`
}

type assocResults struct {
	Results []assocID `json:"results"`
}

type assocID struct {
	ID string `json:"id"`
}

// parseDate decodes a HubSpot timestamp. Object timestamps (createdAt) are
// ISO-8601 and date-property values are epoch-ms strings; both occur.
func parseDate(v string) time.Time {
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.UnixMilli(ms)
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t
}

type apiError struct {
	Message string `json:"message"`
}

// do performs one authenticated request: JSON body when set, JSON response
// decoded into out when non-nil.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+string(c.key))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return c.apiErr(resp)
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// apiErr maps a non-2xx response: 429 and any 5xx become ErrRetry; other
// statuses carry the HubSpot message text (API text only, never the key).
func (c *Client) apiErr(resp *http.Response) error {
	var ae apiError
	msg := ""
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&ae); err == nil {
		msg = ae.Message
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return ErrRetry
	}
	if msg == "" {
		return fmt.Errorf("hubspot: %s", resp.Status)
	}
	return fmt.Errorf("hubspot: %s: %s", resp.Status, msg)
}
