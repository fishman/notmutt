// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package hubspot

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testKey = "test-key"

const alphaContactJSON = `{"id":"201","properties":{"createdate":"1700000000000","email":"alpha@example.com","firstname":"Alpha","lastname":"Able","jobtitle":"CTO"},"createdAt":"2023-11-14T22:13:20Z"}`

const atlasContactJSON = `{"id":"202","properties":{"createdate":"1700000001000","email":"atlas@example.com","firstname":"Atlas","lastname":"Beta","jobtitle":"VP Eng"},"createdAt":"2023-11-14T22:13:21Z"}`

const acmeCompanyJSON = `{"id":"901","properties":{"name":"Acme Corp","domain":"acme.example","industry":"Software","description":"Makes example software"}}`

// call records one request the client made, for the test goroutine to
// assert against after the call returns (channel = happens-before).
type call struct {
	method      string
	target      string
	auth        string
	contentType string
	body        string
}

// start runs the handler behind a recording server and returns a client
// pointed at it; requests are captured on the returned channel.
func start(t *testing.T, h http.HandlerFunc) (*Client, <-chan call) {
	t.Helper()
	calls := make(chan call, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls <- call{
			method:      r.Method,
			target:      r.URL.RequestURI(),
			auth:        r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type"),
			body:        string(b),
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(context.Background(), []byte(testKey))
	c.base = srv.URL
	return c, calls
}

func nextCall(t *testing.T, calls <-chan call) call {
	t.Helper()
	select {
	case c := <-calls:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("client made no request")
		return call{}
	}
}

// TestProvider pins the routing id stamped on published rows/briefings.
func TestProvider(t *testing.T) {
	c, _ := start(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if got := c.Provider(); got != "hubspot" {
		t.Errorf("Provider() = %q, want hubspot", got)
	}
}

// TestListUnprocessed pins the search call (method, path, auth, exact
// request JSON) and the contact mapping when no createdAfter filter is
// requested.
func TestListUnprocessed(t *testing.T) {
	wantBody := `{"filterGroups":[{"filters":[{"propertyName":"notmutt_followed_up","operator":"NOT_HAS_PROPERTY"}]}],"properties":["email","firstname","lastname","jobtitle"],"sorts":[{"propertyName":"createdate","direction":"DESCENDING"}],"limit":100}`
	c, calls := start(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"results":[`+alphaContactJSON+`,`+atlasContactJSON+`]}`)
	})
	got, err := c.ListUnprocessed(context.Background(), "notmutt_followed_up", "")
	if err != nil {
		t.Fatalf("ListUnprocessed: %v", err)
	}
	req := nextCall(t, calls)
	if req.method != http.MethodPost {
		t.Errorf("method = %s, want POST", req.method)
	}
	if req.target != "/crm/v3/objects/contacts/search" {
		t.Errorf("target = %s, want /crm/v3/objects/contacts/search", req.target)
	}
	if req.auth != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want Bearer %s", req.auth, testKey)
	}
	if req.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", req.contentType)
	}
	if req.body != wantBody {
		t.Errorf("body = %s\nwant %s", req.body, wantBody)
	}
	if len(got) != 2 {
		t.Fatalf("got %d contacts, want 2", len(got))
	}
	alpha, atlas := got[0], got[1]
	if alpha.ID != "201" || alpha.Email != "alpha@example.com" || alpha.FirstName != "Alpha" || alpha.LastName != "Able" || alpha.JobTitle != "CTO" {
		t.Errorf("alpha contact mismatch: %+v", alpha)
	}
	if alpha.CompanyID != "" {
		t.Errorf("alpha CompanyID = %q, want empty on a list row", alpha.CompanyID)
	}
	if !alpha.CreatedAt.Equal(time.UnixMilli(1700000000000)) {
		t.Errorf("alpha CreatedAt = %v, want 1700000000000", alpha.CreatedAt)
	}
	if atlas.ID != "202" || atlas.Email != "atlas@example.com" {
		t.Errorf("atlas contact mismatch: %+v", atlas)
	}
}

// TestListUnprocessedPaging drives two search pages: the first response
// carries a paging.next.after link, so the client must re-issue with that
// after offset; the second has none, ending the walk.
func TestListUnprocessedPaging(t *testing.T) {
	// createdAfter arrives as RFC3339 (the config shape); the wire filter
	// value must be the same instant as epoch-ms.
	const createdAfter = "2023-11-14T22:13:20Z"
	wantPage1 := `{"filterGroups":[{"filters":[{"propertyName":"notmutt_followed_up","operator":"NOT_HAS_PROPERTY"},{"propertyName":"createdate","operator":"GT","value":"1700000000000"}]}],"properties":["email","firstname","lastname","jobtitle"],"sorts":[{"propertyName":"createdate","direction":"DESCENDING"}],"limit":100}`
	wantPage2 := `{"filterGroups":[{"filters":[{"propertyName":"notmutt_followed_up","operator":"NOT_HAS_PROPERTY"},{"propertyName":"createdate","operator":"GT","value":"1700000000000"}]}],"properties":["email","firstname","lastname","jobtitle"],"sorts":[{"propertyName":"createdate","direction":"DESCENDING"}],"limit":100,"after":"1"}`
	var served bool
	c, calls := start(t, func(w http.ResponseWriter, r *http.Request) {
		if !served {
			served = true
			io.WriteString(w, `{"results":[`+alphaContactJSON+`],"paging":{"next":{"after":"1","link":"https://api.hubspot.com/crm/v3/objects/contacts/search?after=1"}}}`)
			return
		}
		io.WriteString(w, `{"results":[`+atlasContactJSON+`]}`)
	})
	got, err := c.ListUnprocessed(context.Background(), "notmutt_followed_up", createdAfter)
	if err != nil {
		t.Fatalf("ListUnprocessed: %v", err)
	}
	p1 := nextCall(t, calls)
	if p1.body != wantPage1 {
		t.Errorf("page 1 body = %s\nwant %s", p1.body, wantPage1)
	}
	p2 := nextCall(t, calls)
	if p2.body != wantPage2 {
		t.Errorf("page 2 body = %s\nwant %s", p2.body, wantPage2)
	}
	select {
	case extra := <-calls:
		t.Errorf("unexpected third search request: %+v", extra)
	default:
	}
	if len(got) != 2 || got[0].ID != "201" || got[1].ID != "202" {
		t.Errorf("contacts = %+v, want [201 202] in order", got)
	}
}

// TestContact pins the GET with the company association and its mapping
// into the exported Contact (CompanyID from associations.company).
func TestContact(t *testing.T) {
	c, calls := start(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":"201","properties":{"createdate":"1700000000000","email":"alpha@example.com","firstname":"Alpha","lastname":"Able","jobtitle":"CTO"},"createdAt":"2023-11-14T22:13:20Z","associations":{"company":{"results":[{"id":"901","type":"contact_to_company"}]}}}`)
	})
	got, err := c.Contact(context.Background(), "201")
	if err != nil {
		t.Fatalf("Contact: %v", err)
	}
	req := nextCall(t, calls)
	if req.method != http.MethodGet {
		t.Errorf("method = %s, want GET", req.method)
	}
	if req.target != "/crm/v3/objects/contacts/201?associations=company&properties=email,firstname,lastname,jobtitle" {
		t.Errorf("target = %s, want /crm/v3/objects/contacts/201?associations=company&properties=email,firstname,lastname,jobtitle", req.target)
	}
	if req.auth != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want Bearer %s", req.auth, testKey)
	}
	if got.ID != "201" || got.Email != "alpha@example.com" || got.FirstName != "Alpha" || got.LastName != "Able" || got.JobTitle != "CTO" {
		t.Errorf("contact mismatch: %+v", got)
	}
	if got.CompanyID != "901" {
		t.Errorf("CompanyID = %q, want 901", got.CompanyID)
	}
	if !got.CreatedAt.Equal(time.UnixMilli(1700000000000)) {
		t.Errorf("CreatedAt = %v, want 1700000000000", got.CreatedAt)
	}
}

// TestContactNoCompany pins the missing-association case: no company
// association on the response yields an empty CompanyID, not an error.
func TestContactNoCompany(t *testing.T) {
	c, calls := start(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, alphaContactJSON)
	})
	got, err := c.Contact(context.Background(), "201")
	if err != nil {
		t.Fatalf("Contact: %v", err)
	}
	nextCall(t, calls)
	if got.CompanyID != "" {
		t.Errorf("CompanyID = %q, want empty", got.CompanyID)
	}
}

// TestCompany pins the company GET and its mapping.
func TestCompany(t *testing.T) {
	c, calls := start(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, acmeCompanyJSON)
	})
	got, err := c.Company(context.Background(), "901")
	if err != nil {
		t.Fatalf("Company: %v", err)
	}
	req := nextCall(t, calls)
	if req.method != http.MethodGet {
		t.Errorf("method = %s, want GET", req.method)
	}
	if req.target != "/crm/v3/objects/companies/901?properties=name,domain,industry,description" {
		t.Errorf("target = %s, want /crm/v3/objects/companies/901?properties=name,domain,industry,description", req.target)
	}
	if req.auth != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want Bearer %s", req.auth, testKey)
	}
	if got.ID != "901" || got.Name != "Acme Corp" || got.Domain != "acme.example" || got.Industry != "Software" || got.Description != "Makes example software" {
		t.Errorf("company mismatch: %+v", got)
	}
}

// TestMarkFollowedUp pins the PATCH that records a follow-up: marker
// property set to "true" on the contact.
func TestMarkFollowedUp(t *testing.T) {
	c, calls := start(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{}`)
	})
	err := c.MarkFollowedUp(context.Background(), "201", "notmutt_followed_up")
	if err != nil {
		t.Fatalf("MarkFollowedUp: %v", err)
	}
	req := nextCall(t, calls)
	if req.method != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", req.method)
	}
	if req.target != "/crm/v3/objects/contacts/201" {
		t.Errorf("target = %s, want /crm/v3/objects/contacts/201", req.target)
	}
	if req.auth != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want Bearer %s", req.auth, testKey)
	}
	if req.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", req.contentType)
	}
	if want := `{"properties":{"notmutt_followed_up":"true"}}`; req.body != want {
		t.Errorf("body = %s\nwant %s", req.body, want)
	}
}

// TestErrorMapping pins the retry sentinel: 429 and every 5xx map to
// ErrRetry, other statuses carry the API message as a plain error.
func TestErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		wantRetry bool
	}{
		{"rate limited", http.StatusTooManyRequests, true},
		{"server error", http.StatusInternalServerError, true},
		{"bad gateway", http.StatusBadGateway, true},
		{"bad request", http.StatusBadRequest, false},
		{"not found", http.StatusNotFound, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, calls := start(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, `{"message":"hubspot error text"}`)
			})
			_, err := c.Contact(context.Background(), "201")
			nextCall(t, calls)
			if tc.wantRetry {
				if !errors.Is(err, ErrRetry) {
					t.Fatalf("err = %v, want ErrRetry", err)
				}
				return
			}
			if errors.Is(err, ErrRetry) {
				t.Fatalf("err = ErrRetry, want a non-retryable error")
			}
			if err == nil || !strings.Contains(err.Error(), "hubspot error text") {
				t.Fatalf("err = %v, want the API message carried", err)
			}
		})
	}
}

// TestDecodeBound pins the bounded success decode: a response body larger
// than maxResponseBytes must fail to decode rather than be slurped whole.
// The payload is valid JSON complete with a pad string, so without the
// cap the decode would succeed; the truncating LimitReader cuts the JSON
// and Decode errors instead.
func TestDecodeBound(t *testing.T) {
	pad := strings.Repeat("A", maxResponseBytes) // pushes the closing braces past the cap
	c, calls := start(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"results":[{"id":"201","properties":{"pad":"`+pad+`"}}]}`)
	})
	_, err := c.ListUnprocessed(context.Background(), "notmutt_followed_up", "")
	nextCall(t, calls)
	if err == nil {
		t.Fatal("ListUnprocessed: want a decode error from the response cap")
	}
}

// TestParseDate pins the createdate decoding: epoch millis and ISO-8601
// both parse; empty and garbage yield the zero time.
func TestParseDate(t *testing.T) {
	want := time.UnixMilli(1700000000000)
	for _, v := range []string{"1700000000000", "2023-11-14T22:13:20Z"} {
		if got := parseDate(v); !got.Equal(want) {
			t.Errorf("parseDate(%q) = %v, want %v", v, got, want)
		}
	}
	for _, v := range []string{"", "garbage"} {
		if got := parseDate(v); !got.IsZero() {
			t.Errorf("parseDate(%q) = %v, want zero time", v, got)
		}
	}
}
