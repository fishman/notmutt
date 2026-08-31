// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package ai

import (
	"context"
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
)

const secret = "s3cret-alpha"

// writeKeyScript creates an executable pass_cmd fixture: argv[0] mode
// "ok" prints the secret (with trailing CRLF - the trim case), "fail"
// prints the secret to stdout and stderr before exiting nonzero (the
// no-leak case).
func writeKeyScript(t *testing.T, dir string) []string {
	t.Helper()
	path := filepath.Join(dir, "keycmd")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n[ \"$1\" = fail ] && { echo "+secret+"; echo "+secret+" >&2; exit 1; }\nprintf '%s\\r\\n' \""+secret+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return []string{path}
}

func TestFetchKeyTrimsCRLF(t *testing.T) {
	argv := writeKeyScript(t, t.TempDir())
	key, err := FetchKey(context.Background(), argv)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	if string(key) != secret {
		t.Fatalf("key = %q, want %q", key, secret)
	}
}

func TestFetchKeyFailureLeaksNothing(t *testing.T) {
	argv := append(writeKeyScript(t, t.TempDir()), "fail")
	_, err := FetchKey(context.Background(), argv)
	if err == nil {
		t.Fatal("failing pass_cmd must error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaks the secret: %v", err)
	}
	if !strings.Contains(err.Error(), "keycmd") {
		t.Fatalf("error must name argv[0], got: %v", err)
	}
}

func TestFetchKeyEmpty(t *testing.T) {
	key, err := FetchKey(context.Background(), nil)
	if err != nil || key != nil {
		t.Fatalf("empty argv = (%v, %v), want nil, nil", key, err)
	}
}

// TestChatOpenAIStream exercises the full openai path against a fake
// endpoint: pass_cmd key lands in the authorization header, the
// request shape is right, SSE deltas stream in order, [DONE] stops.
func TestChatOpenAIStream(t *testing.T) {
	var gotKey string
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("authorization")
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Q2 revenue\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\" up 12%\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	argv := writeKeyScript(t, t.TempDir())
	p := config.AIProvider{Type: "openai", Model: "qwen3:8b", BaseURL: srv.URL, PassCmd: argv}
	var deltas []string
	out, err := Chat(context.Background(), p, "qwen3:8b", "Summarize.", "Quarterly report text", func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "Bearer "+secret {
		t.Fatalf("authorization = %q", gotKey)
	}
	if !strings.Contains(gotBody, "\"model\":\"qwen3:8b\"") || !strings.Contains(gotBody, "Quarterly report text") || !strings.Contains(gotBody, "\"stream\":true") {
		t.Fatalf("request body = %s", gotBody)
	}
	if strings.Join(deltas, "") != "Q2 revenue up 12%" {
		t.Fatalf("deltas = %v", deltas)
	}
	if out != "Q2 revenue up 12%" {
		t.Fatalf("accumulated = %q", out)
	}
}

// TestStreamBodyOrdering pins the OpenAI SSE arrival against reordering:
// the deltas land in wire order whatever the framing quirks - CRLF
// endings, a role-only first chunk, a finish_reason chunk, escaped
// newlines, and empty deltas must not scramble or drop the text.
func TestStreamBodyOrdering(t *testing.T) {
	// realistic DeepSeek/OpenAI stream: role chunk, content with escaped
	// newlines, trailing space deltas, finish_reason, CRLF + blank lines
	wire := strings.Join([]string{
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}",
		"",
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"**Thread state:** followed\"},\"finish_reason\":null}]}",
		"",
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\\n\\nAfter\"},\"finish_reason\":null}]}",
		"",
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\" meeting \"},\"finish_reason\":null}]}",
		"",
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Re'za\"},\"finish_reason\":null}]}",
		"",
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\\n\"},\"finish_reason\":null}]}",
		"",
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}",
		"",
		"data: [DONE]",
		"",
	}, "\r\n")
	var got []string
	err := streamBody(strings.NewReader(wire), config.ProtocolOpenAI, func(d string) { got = append(got, d) })
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, "")
	want := "**Thread state:** followed\n\nAfter meeting Re'za\n"
	if joined != want {
		t.Fatalf("deltas = %q, want %q", got, want)
	}
}

// TestDefaultBase pins the per-type vendor URLs: type selects the
// protocol AND its default endpoint (the config's base-url overrides).
func TestDefaultBase(t *testing.T) {
	cases := map[string]string{
		"anthropic":  "https://api.anthropic.com/v1",
		"openai":     "https://api.openai.com/v1",
		"deepseek":   "https://api.deepseek.com/v1",
		"openrouter": "https://openrouter.ai/api/v1",
	}
	for typ, want := range cases {
		if got := defaultBase(typ); got != want {
			t.Errorf("defaultBase(%q) = %q, want %q", typ, got, want)
		}
	}
}

// TestChatOpenAICompatTypes covers the deepseek/openrouter
// types: they route to the OpenAI protocol (SSE parsing), with their
// vendor URL as the default base.
func TestChatOpenAICompatTypes(t *testing.T) {
	for _, typ := range []string{"deepseek", "openrouter"} {
		var gotPath, gotKey string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotKey = r.Header.Get("authorization")
			w.Header().Set("content-type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
		}))
		argv := writeKeyScript(t, t.TempDir())
		p := config.AIProvider{Type: typ, Model: "test-model", BaseURL: srv.URL, PassCmd: argv}
		out, err := Chat(context.Background(), p, "test-model", "S", "T", nil)
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if gotPath != "/chat/completions" {
			t.Errorf("%s: path = %q, want /chat/completions", typ, gotPath)
		}
		if gotKey != "Bearer "+secret {
			t.Errorf("%s: authorization = %q", typ, gotKey)
		}
		if out != "ok" {
			t.Errorf("%s: out = %q", typ, out)
		}
	}
}

// TestChatAnthropicStream covers the anthropic protocol: x-api-key +
// anthropic-version headers, NDJSON content_block_delta extraction.
func TestChatAnthropicStream(t *testing.T) {
	var gotKey, gotVer, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVer = r.Header.Get("anthropic-version")
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hiring freeze\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	argv := writeKeyScript(t, t.TempDir())
	p := config.AIProvider{Type: "anthropic", Model: "claude-test", BaseURL: srv.URL, PassCmd: argv, MaxTokens: 512}
	var out string
	var mu sync.Mutex
	_, err := Chat(context.Background(), p, "claude-test", "Summarize.", "Meeting notes", func(d string) {
		mu.Lock()
		out += d
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != secret || gotVer != "2023-06-01" {
		t.Fatalf("headers x-api-key=%q version=%q", gotKey, gotVer)
	}
	if !strings.Contains(gotBody, "\"max_tokens\":512") || !strings.Contains(gotBody, "\"system\":\"Summarize.\"") {
		t.Fatalf("request body = %s", gotBody)
	}
	if out != "Hiring freeze" {
		t.Fatalf("accumulated = %q", out)
	}
}

func TestChatStatusErrorHidesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "{\"error\":{\"message\":\"Quarterly report text echoed\"}}")
	}))
	defer srv.Close()

	p := config.AIProvider{Type: "openai", Model: "m", BaseURL: srv.URL}
	_, err := Chat(context.Background(), p, "m", "", "Quarterly report text", nil)
	if err == nil {
		t.Fatal("401 must error")
	}
	if strings.Contains(err.Error(), "Quarterly report") {
		t.Fatalf("error echoes the request text: %v", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error must carry the status, got: %v", err)
	}
}

func TestChatTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(2 * time.Second) // never send [DONE]; the client timeout cuts
	}))
	defer srv.Close()

	p := config.AIProvider{Type: "openai", Model: "m", BaseURL: srv.URL, Timeout: 1}
	start := time.Now()
	_, err := Chat(context.Background(), p, "m", "", "text", nil)
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("stream must time out, err = %v", err)
	}
}
