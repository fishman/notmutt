// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua && crm

package crm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"notmutt/config"
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

// clientStub implements every Client method as a no-op so a test fake embeds
// it and overrides only the calls it scripts.
type clientStub struct{}

func (clientStub) Provider() string { return "" }
func (clientStub) ListUnprocessed(context.Context, string, string) ([]Contact, error) {
	return nil, nil
}
func (clientStub) Contact(context.Context, string) (Contact, error)     { return Contact{}, nil }
func (clientStub) Company(context.Context, string) (Company, error)     { return Company{}, nil }
func (clientStub) MarkFollowedUp(context.Context, string, string) error { return nil }

// analyzeClient is a scripted crm.Client for RunAnalyze tests: Contact and
// Company return the recorded value (or error) and count their calls.
type analyzeClient struct {
	clientStub
	provider     string
	contact      Contact
	contactErr   error
	company      Company
	companyErr   error
	contactCalls int
	companyCalls int
}

func (f *analyzeClient) Provider() string { return f.provider }
func (f *analyzeClient) Contact(_ context.Context, _ string) (Contact, error) {
	f.contactCalls++
	if f.contactErr != nil {
		return Contact{}, f.contactErr
	}
	return f.contact, nil
}
func (f *analyzeClient) Company(_ context.Context, _ string) (Company, error) {
	f.companyCalls++
	if f.companyErr != nil {
		return Company{}, f.companyErr
	}
	return f.company, nil
}

// analyzeCall records one RunAnalyze chat invocation.
type analyzeCall struct {
	model  string
	system string
	text   string
}

// analyzeChat returns a fake ChatFn that records every invocation and replies
// to the research pass (researchSystem) with researchReply and to the
// briefing pass with briefingText.
func analyzeChat(researchReply, briefingText string, calls *[]analyzeCall) ChatFn {
	return func(_ context.Context, _ config.AIProvider, model, system, text string, _ func(string)) (string, error) {
		*calls = append(*calls, analyzeCall{model: model, system: system, text: text})
		if system == researchSystem {
			return researchReply, nil
		}
		return briefingText, nil
	}
}

// chatError returns a fake ChatFn that fails every call with err.
func chatError(err error) ChatFn {
	return func(context.Context, config.AIProvider, string, string, string, func(string)) (string, error) {
		return "", err
	}
}

func recvCrmBriefing(t *testing.T, ch <-chan core.Event) core.CrmBriefing {
	t.Helper()
	select {
	case e := <-ch:
		b, ok := e.(core.CrmBriefing)
		if !ok {
			t.Fatalf("event is %T, want core.CrmBriefing", e)
		}
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CrmBriefing")
		return core.CrmBriefing{}
	}
}

func analyzeProvider() config.AIProvider {
	return config.AIProvider{Type: "anthropic", Model: "claude-acme"}
}

// TestRunAnalyzePublishesBriefing pins the happy path: the contact is
// refetched to learn CompanyID, the company is fetched once, research and the
// briefing pass both run over chat, and one CrmBriefing carries the chat text
// with the client's Provider and the row's ContactID.
func TestRunAnalyzePublishesBriefing(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	row := Contact{ID: "201", FirstName: "Alpha", LastName: "Atlas", JobTitle: "SDR (search result)"}
	refetched := row
	refetched.JobTitle = "Head of Procurement" // the refetch is fresher than the search row
	refetched.CompanyID = "901"
	client := &analyzeClient{
		provider: "test-crm",
		contact:  refetched,
		company:  Company{ID: "901", Name: "Acme Corp", Domain: "acme.example", Industry: "Software", Description: "Makes example software"},
	}
	const researchReply = "1. Acme makes analytics software | https://acme.example/products | Core product.\n"
	const briefingText = "Alpha Atlas heads procurement at Acme Corp, which makes example software."
	var calls []analyzeCall

	RunAnalyze(bus, client, analyzeProvider(), row, analyzeChat(researchReply, briefingText, &calls))

	if client.contactCalls != 1 {
		t.Errorf("Contact calls = %d, want 1", client.contactCalls)
	}
	if client.companyCalls != 1 {
		t.Errorf("Company calls = %d, want 1", client.companyCalls)
	}
	b := recvCrmBriefing(t, ch)
	if b.Provider != "test-crm" {
		t.Errorf("CrmBriefing Provider = %q, want test-crm", b.Provider)
	}
	if b.ContactID != "201" {
		t.Errorf("CrmBriefing ContactID = %q, want 201", b.ContactID)
	}
	if b.Text != briefingText {
		t.Errorf("CrmBriefing Text = %q, want the fake chat reply", b.Text)
	}
	if len(calls) != 2 {
		t.Fatalf("chat calls = %d, want 2", len(calls))
	}
	if calls[0].system != researchSystem || calls[0].model != analyzeProvider().Model {
		t.Errorf("research chat = (model %q, system %q), want the provider model and researchSystem", calls[0].model, calls[0].system)
	}
	if !strings.Contains(calls[0].text, "Acme Corp") {
		t.Errorf("research text = %q, want the refetched company name", calls[0].text)
	}
	if calls[1].system != briefingSystem || calls[1].model != analyzeProvider().Model {
		t.Errorf("briefing chat = (model %q, system %q), want the provider model and briefingSystem", calls[1].model, calls[1].system)
	}
	// The briefing context carries the REFETCHED contact, never the stale search
	// row: the title the refetch returned is present and the row's is absent.
	for _, want := range []string{"Alpha Atlas", "Head of Procurement", "Acme Corp", "makes analytics software"} {
		if !strings.Contains(calls[1].text, want) {
			t.Errorf("briefing context missing %q\n%s", want, calls[1].text)
		}
	}
	if strings.Contains(calls[1].text, "SDR (search result)") {
		t.Errorf("briefing context used the stale search row, not the refetch\n%s", calls[1].text)
	}
	select {
	case e := <-ch:
		t.Fatalf("unexpected extra event %T after the briefing", e)
	default:
	}
}

// TestRunAnalyzeEmptyCompanyIDSkipsCompanyFetch pins the no-association path:
// a refetched contact without CompanyID skips the company fetch, research
// degrades to the unknown-company placeholder, and the briefing still
// publishes.
func TestRunAnalyzeEmptyCompanyIDSkipsCompanyFetch(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	row := Contact{ID: "201", FirstName: "Alpha", LastName: "Atlas", JobTitle: "CTO"}
	client := &analyzeClient{provider: "test-crm", contact: row}
	const briefingText = "Alpha Atlas leads the CTO office."
	var calls []analyzeCall

	RunAnalyze(bus, client, analyzeProvider(), row, analyzeChat("", briefingText, &calls))

	if client.companyCalls != 0 {
		t.Errorf("Company calls = %d, want 0 when the contact has no company", client.companyCalls)
	}
	b := recvCrmBriefing(t, ch)
	if b.Text != briefingText {
		t.Errorf("CrmBriefing Text = %q, want the fake chat reply", b.Text)
	}
	if len(calls) != 2 {
		t.Fatalf("chat calls = %d, want 2", len(calls))
	}
	if !strings.Contains(calls[0].text, "unknown company") {
		t.Errorf("research text = %q, want the unknown-company placeholder", calls[0].text)
	}
}

// TestRunAnalyzeContactErrorPublishesRowError pins the refetch failure path: a
// Contact error publishes one CrmRowError naming the row and no CrmBriefing,
// so the row keeps its prior status.
func TestRunAnalyzeContactErrorPublishesRowError(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	sentinel := errors.New("contact lookup failed")
	client := &analyzeClient{provider: "test-crm", contactErr: sentinel}
	var calls []analyzeCall

	RunAnalyze(bus, client, analyzeProvider(), Contact{ID: "201"}, analyzeChat("", "", &calls))

	if client.contactCalls != 1 {
		t.Errorf("Contact calls = %d, want 1", client.contactCalls)
	}
	if client.companyCalls != 0 {
		t.Errorf("Company calls = %d, want 0 (contact error stops before the company fetch)", client.companyCalls)
	}
	e := recvCrmRowError(t, ch)
	if e.Provider != "test-crm" || e.ContactID != "201" {
		t.Errorf("CrmRowError = (%q, %q), want (test-crm, 201)", e.Provider, e.ContactID)
	}
	if !errors.Is(e.Err, sentinel) {
		t.Errorf("CrmRowError Err = %v, want the contact error", e.Err)
	}
	if !strings.Contains(e.Err.Error(), "crm: contact:") {
		t.Errorf("CrmRowError Err = %v, want the crm: contact: wrap", e.Err)
	}
	assertNoCrmEvent(t, ch)
}

// TestRunAnalyzeCompanyErrorPublishesRowError pins the company fetch failure
// path after a successful contact refetch.
func TestRunAnalyzeCompanyErrorPublishesRowError(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	sentinel := errors.New("company lookup failed")
	client := &analyzeClient{
		provider:   "test-crm",
		contact:    Contact{ID: "201", CompanyID: "901"},
		companyErr: sentinel,
	}
	var calls []analyzeCall

	RunAnalyze(bus, client, analyzeProvider(), Contact{ID: "201"}, analyzeChat("", "", &calls))

	if client.contactCalls != 1 || client.companyCalls != 1 {
		t.Errorf("fetches = (contact %d, company %d), want (1, 1)", client.contactCalls, client.companyCalls)
	}
	e := recvCrmRowError(t, ch)
	if e.ContactID != "201" {
		t.Errorf("CrmRowError ContactID = %q, want 201", e.ContactID)
	}
	if !errors.Is(e.Err, sentinel) || !strings.Contains(e.Err.Error(), "crm: company:") {
		t.Errorf("CrmRowError Err = %v, want the crm: company: wrapped error", e.Err)
	}
	assertNoCrmEvent(t, ch)
}

// TestRunAnalyzeChatErrorPublishesRowError pins a mid-flow chat failure: the
// research pass error publishes one CrmRowError and no CrmBriefing.
func TestRunAnalyzeChatErrorPublishesRowError(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	sentinel := errors.New("chat down")
	client := &analyzeClient{
		provider: "test-crm",
		contact:  Contact{ID: "201", CompanyID: "901"},
		company:  Company{ID: "901", Name: "Acme Corp"},
	}

	RunAnalyze(bus, client, analyzeProvider(), Contact{ID: "201"}, chatError(sentinel))

	e := recvCrmRowError(t, ch)
	if e.Provider != "test-crm" || e.ContactID != "201" {
		t.Errorf("CrmRowError = (%q, %q), want (test-crm, 201)", e.Provider, e.ContactID)
	}
	if !errors.Is(e.Err, sentinel) {
		t.Errorf("CrmRowError Err = %v, want the chat error", e.Err)
	}
	if !strings.Contains(e.Err.Error(), "crm: analyze: research:") {
		t.Errorf("CrmRowError Err = %v, want the crm: analyze: research: wrap", e.Err)
	}
	assertNoCrmEvent(t, ch)
}

// TestRunAnalyzeBriefingChatErrorPublishesRowError pins the second-chat
// failure path: research succeeds but the briefing-writer chat errors, so one
// CrmRowError publishes and no CrmBriefing does.
func TestRunAnalyzeBriefingChatErrorPublishesRowError(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	sentinel := errors.New("briefing chat down")
	client := &analyzeClient{
		provider: "test-crm",
		contact:  Contact{ID: "201", CompanyID: "901"},
		company:  Company{ID: "901", Name: "Acme Corp"},
	}
	chat := func(_ context.Context, _ config.AIProvider, _ string, system, _ string, _ func(string)) (string, error) {
		if system == briefingSystem {
			return "", sentinel
		}
		return "1. Acme sells widgets | https://acme.example | Snip.", nil
	}

	RunAnalyze(bus, client, analyzeProvider(), Contact{ID: "201"}, chat)

	e := recvCrmRowError(t, ch)
	if e.ContactID != "201" {
		t.Errorf("CrmRowError ContactID = %q, want 201", e.ContactID)
	}
	if !errors.Is(e.Err, sentinel) {
		t.Errorf("CrmRowError Err = %v, want the briefing-chat error", e.Err)
	}
	if !strings.Contains(e.Err.Error(), "crm: analyze: briefing:") {
		t.Errorf("CrmRowError Err = %v, want the crm: analyze: briefing: wrap", e.Err)
	}
	assertNoCrmEvent(t, ch)
}

// TestRunAnalyzeBriefingErrorPublishesRowError pins the assembly failure path:
// briefing refuses a refetched contact carrying malformed UTF-8, so one
// CrmRowError publishes and no CrmBriefing does.
func TestRunAnalyzeBriefingErrorPublishesRowError(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	client := &analyzeClient{
		provider: "test-crm",
		contact:  Contact{ID: "201", FirstName: "Alpha", LastName: "\xff", JobTitle: "CTO", CompanyID: "901"},
		company:  Company{ID: "901", Name: "Acme Corp"},
	}
	var calls []analyzeCall

	RunAnalyze(bus, client, analyzeProvider(), Contact{ID: "201"}, analyzeChat("", "", &calls))

	if client.contactCalls != 1 || client.companyCalls != 1 {
		t.Errorf("fetches = (contact %d, company %d), want (1, 1)", client.contactCalls, client.companyCalls)
	}
	e := recvCrmRowError(t, ch)
	if e.ContactID != "201" {
		t.Errorf("CrmRowError ContactID = %q, want 201", e.ContactID)
	}
	if e.Err == nil || !strings.Contains(e.Err.Error(), "crm: briefing:") {
		t.Errorf("CrmRowError Err = %v, want the crm: briefing: assembly error", e.Err)
	}
	assertNoCrmEvent(t, ch)
}

// TestRunAnalyzeNilChatErrors pins the nil-chat guard: RunAnalyze refuses
// before any fetch and publishes one CrmRowError.
func TestRunAnalyzeNilChatErrors(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	client := &analyzeClient{provider: "test-crm", contact: Contact{ID: "201", CompanyID: "901"}}

	RunAnalyze(bus, client, analyzeProvider(), Contact{ID: "201"}, nil)

	if client.contactCalls != 0 {
		t.Errorf("Contact calls = %d, want 0 (nil chat guards before any fetch)", client.contactCalls)
	}
	e := recvCrmRowError(t, ch)
	if e.Provider != "test-crm" || e.ContactID != "201" {
		t.Errorf("CrmRowError = (%q, %q), want (test-crm, 201)", e.Provider, e.ContactID)
	}
	if e.Err == nil || !strings.Contains(e.Err.Error(), "crm: analyze: nil chat fn") {
		t.Errorf("CrmRowError Err = %v, want the nil-chat guard error", e.Err)
	}
	assertNoCrmEvent(t, ch)
}

// assertNoCrmEvent fails if any further event follows the one already
// received, pinning that a run publishes exactly one Crm* event.
func assertNoCrmEvent(t *testing.T, ch <-chan core.Event) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("unexpected extra event %T after the row error", e)
	default:
	}
}

// markClient is a scripted crm.Client for RunMark tests: it records
// MarkFollowedUp calls (id and marker) and returns the scripted error.
type markClient struct {
	clientStub
	provider string
	markErr  error
	id       string
	marker   string
	calls    int
}

func (f *markClient) Provider() string { return f.provider }
func (f *markClient) MarkFollowedUp(_ context.Context, id, marker string) error {
	f.calls++
	f.id, f.marker = id, marker
	return f.markErr
}

// TestRunMarkSuccessPublishesNothing pins the happy path: MarkFollowedUp runs
// once with the right id and marker, and no event follows (row-leave is
// pull-driven - a marked contact drops from the next pull page).
func TestRunMarkSuccessPublishesNothing(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	client := &markClient{provider: "test-crm"}

	RunMark(bus, client, "201", "notmutt_followed_up")

	if client.calls != 1 {
		t.Errorf("MarkFollowedUp calls = %d, want 1", client.calls)
	}
	if client.id != "201" || client.marker != "notmutt_followed_up" {
		t.Errorf("MarkFollowedUp = (%q, %q), want (201, notmutt_followed_up)", client.id, client.marker)
	}
	assertNoCrmEvent(t, ch)
}

// TestRunMarkErrorPublishesRowError pins the failure path: a MarkFollowedUp
// error publishes one CrmRowError with the client's Provider, the contact id,
// and the crm: mark: wrap, so the row stays retryable.
func TestRunMarkErrorPublishesRowError(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	sentinel := errors.New("mark exploded")
	client := &markClient{provider: "test-crm", markErr: sentinel}

	RunMark(bus, client, "201", "notmutt_followed_up")

	e := recvCrmRowError(t, ch)
	if e.Provider != "test-crm" || e.ContactID != "201" {
		t.Errorf("CrmRowError = (%q, %q), want (test-crm, 201)", e.Provider, e.ContactID)
	}
	if !errors.Is(e.Err, sentinel) || !strings.Contains(e.Err.Error(), "crm: mark:") {
		t.Errorf("CrmRowError Err = %v, want the crm: mark: wrapped error", e.Err)
	}
	assertNoCrmEvent(t, ch)
}
