// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua && crm

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
	"notmutt/lib/crm"
	"notmutt/lib/crm/hubspot"
	"notmutt/notmuch"
)

// crmWriteKeyScript writes an executable argv that prints token on stdout
// (the pass_cmd / token_cmd shape: F4 argv, never a shell string).
func crmWriteKeyScript(t *testing.T, token string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho "+token+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}

// crmFakeWorker is a worker whose queries answer nothing: the draft's mail
// grounding finds no inbound thread and returns no context (the test
// fabricates contacts, never mail).
type crmFakeWorker struct{}

func (crmFakeWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	return notmuch.Reply{}, nil
}

// crmHSState is the httptest HubSpot server's mutable contact set: marked
// records the contacts a follow-up PATCH landed on, fail forces that mark
// to error. One mutex guards both (the client calls come from the workflow
// goroutines).
type crmHSState struct {
	mu      sync.Mutex
	marked  map[string]bool
	fail    map[string]bool
	patches []string
}

func newCRMHSState() *crmHSState {
	return &crmHSState{marked: map[string]bool{}, fail: map[string]bool{}}
}

func (s *crmHSState) isMarked(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.marked[id]
}

func (s *crmHSState) patchCount(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.patches {
		if p == id {
			n++
		}
	}
	return n
}

// crmContactJSON is one search/read wire object for id; the returned
// contact carries its company association so analyze learns CompanyID.
func crmContactJSON(id string) string {
	switch id {
	case "201":
		return `{"id":"201","properties":{"createdate":"1700000000000","email":"alpha@example.com","firstname":"Alpha","lastname":"Able","jobtitle":"CTO"},"createdAt":"2023-11-14T22:13:20Z","associations":{"company":{"results":[{"id":"901","type":"contact_to_company"}]}}}`
	case "202":
		return `{"id":"202","properties":{"createdate":"1700000001000","email":"atlas@example.com","firstname":"Atlas","lastname":"Beta","jobtitle":"VP Eng"},"createdAt":"2023-11-14T22:13:21Z"}`
	}
	return ""
}

const crmCompanyJSON = `{"id":"901","properties":{"name":"Acme Corp","domain":"acme.example","industry":"Software","description":"Makes example software"}}`

// handler answers the HubSpot surface the workflow drives. Search lists the
// unmarked contacts (fabricated ids only); the contact/company GETs serve
// fixtures; the marker PATCH records the id, or fails for the ids in fail.
func (s *crmHSState) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/crm/v3/objects/contacts/search", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		var results []string
		for _, id := range []string{"201", "202"} {
			if !s.marked[id] {
				results = append(results, crmContactJSON(id))
			}
		}
		s.mu.Unlock()
		fmt.Fprintf(w, `{"results":[%s]}`, strings.Join(results, ","))
	})
	mux.HandleFunc("/crm/v3/objects/contacts/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/crm/v3/objects/contacts/")
		if r.Method == http.MethodPatch {
			s.mu.Lock()
			shouldFail := s.fail[id]
			s.patches = append(s.patches, id) // an attempted mark, failing or not
			if !shouldFail {
				s.marked[id] = true
			}
			s.mu.Unlock()
			if shouldFail {
				http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
				return
			}
			w.Write([]byte(`{}`))
			return
		}
		if j := crmContactJSON(id); j != "" {
			w.Write([]byte(j))
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/crm/v3/objects/companies/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(crmCompanyJSON))
	})
	return mux
}

// crmAIContent/Delta/Event are the OpenAI SSE wire shape the test server
// streams back.
type crmAIContent struct {
	Content string `json:"content"`
}

type crmAIDelta struct {
	Delta crmAIContent `json:"delta"`
}

type crmAIEvent struct {
	Choices []crmAIDelta `json:"choices"`
}

// crmAIServer answers the OpenAI-compatible chat completions the workflow
// calls. It keys the reply off the request's system message so each stage
// (research, analyst briefing, draft) returns what its caller parses; the
// returned strings are the briefing and draft the test asserts against.
func crmAIServer() (srv *httptest.Server, briefing, draft string) {
	briefing = "Alpha Able is CTO at Acme Corp, which makes example software."
	draft = "Subject: Following up on Acme\n\nHi Alpha,\n\nIt was great meeting you.\n\nBest,\nAlex"
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		sys := ""
		for _, m := range body.Messages {
			if m.Role == "system" {
				sys = m.Content
			}
		}
		reply := briefing
		switch {
		case strings.Contains(sys, "researcher"):
			reply = "1. Acme sells example software. | https://acme.example | Acme makes example software."
		case strings.Contains(sys, "concise follow-up"):
			reply = draft
		}
		payload, _ := json.Marshal(crmAIEvent{Choices: []crmAIDelta{{Delta: crmAIContent{Content: reply}}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
	}))
	return srv, briefing, draft
}

// TestCrmHubspotWorkflow drives the whole follow-up wire through crmWire's
// bus reactions: pull -> queue rows, analyze -> briefing, draft -> compose,
// send-OK -> mark write-back, and a failing dismiss mark leaving its row on
// the next pull. The only place the test names a vendor is cfg.Crm.Provider
// and the seam's httptest base URL; every assertion reads bus events.
func TestCrmHubspotWorkflow(t *testing.T) {
	bus := core.NewBus()
	events := bus.Subscribe()

	aiSrv, briefing, draft := crmAIServer()
	defer aiSrv.Close()
	draftSubject := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(draft, "\n", 2)[0], "Subject: "))
	hs := newCRMHSState()
	hsSrv := httptest.NewServer(hs.handler())
	defer hsSrv.Close()

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"acme": {From: "Me <me@example.com>"}}
	cfg.AI = map[string]config.AIProvider{
		"test-ai": {Type: "openai", Model: "test-model", BaseURL: aiSrv.URL, PassCmd: []string{crmWriteKeyScript(t, "sk-test")}},
	}
	cfg.Crm = config.CrmConfig{
		Provider: "hubspot",
		Hubspot: &config.HubspotConfig{
			AI:             "test-ai",
			TokenCmd:       []string{crmWriteKeyScript(t, "test-token")},
			MarkerProperty: "notmutt_followed_up",
		},
	}
	root := t.TempDir()

	origNew := crmHubspotNew
	crmHubspotNew = func(ctx context.Context, key []byte) crm.Client {
		return hubspot.NewClientURL(ctx, key, hsSrv.URL)
	}
	defer func() { crmHubspotNew = origNew }()

	crmWire(context.Background(), bus, crmFakeWorker{}, cfg, root)

	// pull: the first RefreshRequested publishes one CrmQueue page whose rows
	// carry the client's provider routing id.
	bus.Publish(core.RefreshRequested{})
	q := crmWaitFor(t, events, func(e core.Event) bool {
		_, ok := e.(core.CrmQueue)
		return ok
	}).(core.CrmQueue)
	if len(q.Contacts) != 2 {
		t.Fatalf("pull page has %d rows, want 2", len(q.Contacts))
	}
	for _, c := range q.Contacts {
		if c.Provider != "hubspot" {
			t.Errorf("row provider = %q, want hubspot", c.Provider)
		}
	}
	if q.Contacts[0].ID != "201" || q.Contacts[1].ID != "202" {
		t.Fatalf("page order = %q, %q; want 201, 202", q.Contacts[0].ID, q.Contacts[1].ID)
	}

	// analyze: the action handler launches RunAnalyze, which refetches the
	// contact and company, researches, and briefs - the CrmBriefing carries
	// the analyst reply.
	crmRowAction("analyze", q.Contacts[0])
	b := crmWaitFor(t, events, func(e core.Event) bool {
		be, ok := e.(core.CrmBriefing)
		return ok && be.ContactID == "201"
	}).(core.CrmBriefing)
	if b.Provider != "hubspot" || b.Text != briefing {
		t.Errorf("briefing = provider %q text %q, want hubspot / %q", b.Provider, b.Text, briefing)
	}

	// draft: the handler looks up the cached briefing and opens the compose.
	crmRowAction("draft", q.Contacts[0])
	opened := crmWaitFor(t, events, func(e core.Event) bool {
		_, ok := e.(core.ComposeOpened)
		return ok
	}).(core.ComposeOpened)
	if len(opened.To) != 1 || opened.To[0] != "alpha@example.com" {
		t.Errorf("compose To = %v, want [alpha@example.com]", opened.To)
	}
	if opened.Subject != draftSubject {
		t.Errorf("compose Subject = %q, want the draft subject %q", opened.Subject, draftSubject)
	}
	if !strings.Contains(opened.Body, "Hi Alpha") {
		t.Errorf("compose Body = %q, want the draft body", opened.Body)
	}

	// send-OK: the write-back marks the contact once, and the next pull page
	// omits it.
	bus.Publish(core.SendResult{TabID: opened.TabID, OK: true})
	if !crmEventually(t, func() bool { return hs.patchCount("201") == 1 }) {
		t.Fatalf("send-OK mark: expected one PATCH on 201, got %d", hs.patchCount("201"))
	}
	bus.Publish(core.RefreshRequested{})
	q2 := crmWaitFor(t, events, func(e core.Event) bool {
		_, ok := e.(core.CrmQueue)
		return ok
	}).(core.CrmQueue)
	if len(q2.Contacts) != 1 || q2.Contacts[0].ID != "202" {
		t.Fatalf("page after the send write-back = %+v, want only 202", q2.Contacts)
	}

	// dismiss: a failing mark still calls MarkFollowedUp once on the row's
	// contact and publishes the row error; the row survives the next pull.
	hs.mu.Lock()
	hs.fail["202"] = true
	hs.mu.Unlock()
	crmRowAction("dismiss", q2.Contacts[0])
	crmWaitFor(t, events, func(e core.Event) bool {
		re, ok := e.(core.CrmRowError)
		return ok && re.ContactID == "202"
	})
	if hs.patchCount("202") != 1 {
		t.Errorf("dismiss mark: expected one PATCH on 202, got %d", hs.patchCount("202"))
	}
	bus.Publish(core.RefreshRequested{})
	q3 := crmWaitFor(t, events, func(e core.Event) bool {
		_, ok := e.(core.CrmQueue)
		return ok
	}).(core.CrmQueue)
	if len(q3.Contacts) != 1 || q3.Contacts[0].ID != "202" {
		t.Fatalf("page after the failed dismiss mark = %+v, want 202 kept", q3.Contacts)
	}
}

// crmWaitFor scans the bus until e satisfies pred (ignoring unrelated
// events) or the deadline passes.
func crmWaitFor(t *testing.T, ch <-chan core.Event, pred func(core.Event) bool) core.Event {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case e := <-ch:
			if pred(e) {
				return e
			}
		case <-deadline:
			t.Fatal("timed out waiting for the bus event")
			return nil
		}
	}
}

// crmEventually polls cond until it holds (the mark runs on a workflow
// goroutine, so no happens-before channel exists to wait on).
func crmEventually(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
