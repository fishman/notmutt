// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua && crm

// Package hubspot implements the crm.Client seam for HubSpot: bearer-auth
// HTTP against the CRM v3 API, the caller-supplied marker property, and
// HubSpot-specific retry signaling (ErrRetry). The package is wire-only -
// no app/ai imports - so the bearer key arrives already resolved
// (NewClient) and the neutral domain types live in the parent package
// (notmutt/lib/crm).
package hubspot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"notmutt/lib/crm"
)

const (
	apiBase          = "https://api.hubspot.com"
	maxResponseBytes = 8 << 20 // success-path decode cap, mirrors the bounded-body habit
	maxErrBody       = 1 << 16 // error-path message read cap
)

// contactProps and companyProps are the properties the mapping reads,
// requested explicitly because HubSpot's default search/read property set
// does not reliably include jobtitle (and excludes custom properties).
// Read-only.
var (
	contactProps = []string{"email", "firstname", "lastname", "jobtitle"}
	companyProps = []string{"name", "domain", "industry", "description"}
)

// ErrRetry marks a HubSpot failure the caller may retry (HTTP 429 or any
// 5xx); the caller decides the backoff.
var ErrRetry = errors.New("crm: retryable hubspot error")

// Client is a HubSpot CRM v3 client. It satisfies crm.Client.
type Client struct {
	key  []byte
	base string // apiBase, overridable in tests
	hc   *http.Client
}

var _ crm.Client = (*Client)(nil)

// NewClient returns a HubSpot CRM client against apiBase. The bearer key
// is used verbatim on every request; ctx is accepted for call-site
// symmetry.
func NewClient(ctx context.Context, key []byte) *Client {
	return NewClientURL(ctx, key, "")
}

// NewClientURL returns a HubSpot CRM client against baseURL, which must be
// an absolute http(s) origin ("" = apiBase). The base override is the
// wire seam a caller (the app integration test) uses to point a real
// client at an httptest server.
func NewClientURL(_ context.Context, key []byte, baseURL string) *Client {
	if baseURL == "" {
		baseURL = apiBase
	}
	return &Client{
		key:  key,
		base: baseURL,
		// Per-request cap mirroring the ai-package posture; a caller ctx
		// with an earlier deadline still wins.
		hc: &http.Client{Timeout: 30 * time.Second},
	}
}

// Provider reports the routing id stamped on every published
// row/briefing/draft.
func (c *Client) Provider() string { return "hubspot" }

// ListUnprocessed pages contacts lacking the marker property, newest
// first, limit 100 per page, following paging.next.after until absent.
// createdAfter, when non-empty, is an RFC3339 instant narrowing the search
// to contacts created after it (HubSpot date filters take epoch-ms, so the
// client converts).
func (c *Client) ListUnprocessed(ctx context.Context, marker, createdAfter string) ([]crm.Contact, error) {
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
		Properties:   contactProps,
		Sorts:        []sortSpec{{PropertyName: "createdate", Direction: "DESCENDING"}},
		Limit:        100,
	}
	var contacts []crm.Contact
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
func (c *Client) Contact(ctx context.Context, id string) (crm.Contact, error) {
	var w contactWire
	path := "/crm/v3/objects/contacts/" + id + "?associations=company&properties=" + strings.Join(contactProps, ",")
	if err := c.do(ctx, http.MethodGet, path, nil, &w); err != nil {
		return crm.Contact{}, err
	}
	return w.contact(), nil
}

// Company returns one company by id.
func (c *Client) Company(ctx context.Context, id string) (crm.Company, error) {
	var w companyWire
	path := "/crm/v3/objects/companies/" + id + "?properties=" + strings.Join(companyProps, ",")
	if err := c.do(ctx, http.MethodGet, path, nil, &w); err != nil {
		return crm.Company{}, err
	}
	return crm.Company{
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
	Properties   []string      `json:"properties,omitempty"`
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

func (w contactWire) contact() crm.Contact {
	c := crm.Contact{
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
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out); err != nil {
		return fmt.Errorf("hubspot: decode response: %w", err)
	}
	return nil
}

// apiErr maps a non-2xx response: 429 and any 5xx become ErrRetry; other
// statuses carry the HubSpot message text (API text only, never the key).
func (c *Client) apiErr(resp *http.Response) error {
	var ae apiError
	msg := ""
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrBody)).Decode(&ae); err == nil {
		msg = ae.Message
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return fmt.Errorf("hubspot: %s: %w", resp.Status, ErrRetry)
	}
	if msg == "" {
		return fmt.Errorf("hubspot: %s", resp.Status)
	}
	return fmt.Errorf("hubspot: %s: %s", resp.Status, msg)
}
