// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua && crm

package crm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"notmutt/core"
)

// fakeClient is a scripted crm.Client for RunPull tests: ListUnprocessed
// returns the recorded contacts or error and captures its filter params.
type fakeClient struct {
	provider        string
	contacts        []Contact
	err             error
	gotMarker       string
	gotCreatedAfter string
}

func (f *fakeClient) Provider() string { return f.provider }
func (f *fakeClient) ListUnprocessed(_ context.Context, marker, createdAfter string) ([]Contact, error) {
	f.gotMarker, f.gotCreatedAfter = marker, createdAfter
	if f.err != nil {
		return nil, f.err
	}
	return f.contacts, nil
}
func (f *fakeClient) Contact(context.Context, string) (Contact, error)     { return Contact{}, nil }
func (f *fakeClient) Company(context.Context, string) (Company, error)     { return Company{}, nil }
func (f *fakeClient) MarkFollowedUp(context.Context, string, string) error { return nil }

// TestRunPullPublishesRows pins the Contact -> CrmContact mapping: each row
// carries the client's Provider, the mapped identity fields, an empty Company
// (search rows carry no association; the pull never fills it) and statusNew,
// all in one CrmQueue page.
func TestRunPullPublishesRows(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	now := time.Unix(1700000000, 0)
	client := &fakeClient{
		provider: "test-crm",
		contacts: []Contact{
			{ID: "201", Email: "alpha@example.com", FirstName: "Alpha", LastName: "Able", JobTitle: "CTO", CreatedAt: now},
			{ID: "202", Email: "atlas@example.com", FirstName: "Atlas", LastName: "Beta", JobTitle: "VP Eng", CompanyID: "901", CreatedAt: now.Add(time.Minute)},
		},
	}
	RunPull(bus, client, "notmutt_followed_up", "")

	if client.gotMarker != "notmutt_followed_up" {
		t.Errorf("ListUnprocessed marker = %q, want the pass-through", client.gotMarker)
	}
	q := recvCrmQueue(t, ch)
	if len(q.Contacts) != 2 {
		t.Fatalf("published %d rows, want 2", len(q.Contacts))
	}
	wants := []core.CrmContact{
		{Provider: "test-crm", ID: "201", Email: "alpha@example.com", First: "Alpha", Last: "Able", Title: "CTO", CreatedAt: now, Status: statusNew},
		{Provider: "test-crm", ID: "202", Email: "atlas@example.com", First: "Atlas", Last: "Beta", Title: "VP Eng", CreatedAt: now.Add(time.Minute), Status: statusNew},
	}
	for i, want := range wants {
		got := q.Contacts[i]
		if got != want {
			t.Errorf("row %d = %+v, want %+v", i, got, want)
		}
	}
	// The queue is exactly one page: no further events follow the CrmQueue.
	select {
	case e := <-ch:
		t.Fatalf("unexpected extra event %T after the queue page", e)
	default:
	}
}

// TestRunPullErrorPublishesRowError pins the failure path: a ListUnprocessed
// error publishes one CrmRowError (Provider set, ContactID empty - no row
// context exists) and no CrmQueue page, so rows already received stay.
func TestRunPullErrorPublishesRowError(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	client := &fakeClient{provider: "test-crm", err: errors.New("search exploded")}
	RunPull(bus, client, "m", "2023-01-01T00:00:00Z")

	if client.gotCreatedAfter != "2023-01-01T00:00:00Z" {
		t.Errorf("ListUnprocessed createdAfter = %q, want the pass-through", client.gotCreatedAfter)
	}
	e := recvCrmRowError(t, ch)
	if e.Provider != "test-crm" {
		t.Errorf("CrmRowError Provider = %q, want test-crm", e.Provider)
	}
	if e.ContactID != "" {
		t.Errorf("CrmRowError ContactID = %q, want empty", e.ContactID)
	}
	if e.Err == nil {
		t.Error("CrmRowError Err = nil, want the ListUnprocessed error")
	}
	select {
	case e := <-ch:
		t.Fatalf("unexpected extra event %T after the row error", e)
	default:
	}
}

// blockingClient counts ListUnprocessed calls and parks the first until
// release closes, so a test can hold the RunPull guard open.
type blockingClient struct {
	provider string
	entered  chan struct{}
	release  chan struct{}
	calls    int32
	once     sync.Once
}

func (f *blockingClient) Provider() string { return f.provider }
func (f *blockingClient) ListUnprocessed(context.Context, string, string) ([]Contact, error) {
	atomic.AddInt32(&f.calls, 1)
	f.once.Do(func() { close(f.entered) })
	<-f.release
	return nil, nil
}
func (f *blockingClient) Contact(context.Context, string) (Contact, error)     { return Contact{}, nil }
func (f *blockingClient) Company(context.Context, string) (Company, error)     { return Company{}, nil }
func (f *blockingClient) MarkFollowedUp(context.Context, string, string) error { return nil }

// TestRunPullOverlapNoops pins the run guard: a pull already in flight makes
// an overlapping RunPull return without a second ListUnprocessed call.
func TestRunPullOverlapNoops(t *testing.T) {
	bus := core.NewBus()
	client := &blockingClient{provider: "test-crm", entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(client.release) }) }
	t.Cleanup(release)

	done := make(chan struct{})
	go func() {
		RunPull(bus, client, "m", "")
		close(done)
	}()
	select {
	case <-client.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first pull never reached ListUnprocessed")
	}
	RunPull(bus, client, "m", "") // overlapping: the guard no-ops
	release()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("first pull never returned")
	}
	if got := atomic.LoadInt32(&client.calls); got != 1 {
		t.Fatalf("ListUnprocessed calls = %d, want 1 (overlap must no-op)", got)
	}
}

func recvCrmQueue(t *testing.T, ch <-chan core.Event) core.CrmQueue {
	t.Helper()
	select {
	case e := <-ch:
		q, ok := e.(core.CrmQueue)
		if !ok {
			t.Fatalf("event is %T, want core.CrmQueue", e)
		}
		return q
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CrmQueue")
		return core.CrmQueue{}
	}
}

func recvCrmRowError(t *testing.T, ch <-chan core.Event) core.CrmRowError {
	t.Helper()
	select {
	case e := <-ch:
		r, ok := e.(core.CrmRowError)
		if !ok {
			t.Fatalf("event is %T, want core.CrmRowError", e)
		}
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CrmRowError")
		return core.CrmRowError{}
	}
}
