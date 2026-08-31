// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

// Package ai streams chat completions to the configured AI providers
// (R8): the anthropic protocol (/v1/messages) and OpenAI-compatible
// endpoints (/v1/chat/completions). The type selects protocol AND a
// vendor default URL - anthropic, openai, deepseek, openrouter -
// that
// base-url overrides (any protocol-compatible endpoint: local ollama,
// a proxy, DeepSeek's Anthropic-compatible URL, ...). The client talks
// HTTP itself - no SDKs (supply-chain policy, decision record 7):
// stdlib net/http with http2 forced (Go's net/http bundles h2), manual
// eventstream line parsing (NDJSON for anthropic, SSE for openai). API
// keys come from the provider's pass_cmd argv, fetched per request,
// held only for that request, never logged (F6).
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"notmutt/config"
)

const (
	keyTimeout       = 10 * time.Second
	maxLine          = 1 << 20 // one streamed delta line cap (the summary is text)
	anthropicVersion = "2023-06-01"
)

// defaultBase resolves a provider type's vendor base URL from the
// AIProviders registry; the config's base-url overrides it, so any type
// can point at any protocol-compatible endpoint (local ollama, a proxy,
// DeepSeek's Anthropic-compatible URL, ...).
func defaultBase(typ string) string {
	return config.AIProviders[typ].DefaultURL
}

// FetchKey runs one pass_cmd argv (F4: tokenized at load, argv exec,
// never the sh -c of the matcha precedent) and returns the trimmed
// stdout as a []byte the caller owns and must clear after use. A
// failing command reports argv[0] and the error only - its stdout and
// stderr are dropped, never logged (a failure must not leak the
// secret). Empty argv returns nil, nil (no auth).
func FetchKey(ctx context.Context, argv []string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, keyTimeout)
	defer cancel()
	out := boundedBuf{left: maxLine}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w", argv[0], err)
	}
	return bytes.TrimRight(out.Bytes(), "\r\n"), nil
}

// boundedBuf caps a child's stdout at maxLine: a runaway tool must
// not balloon memory - the timeout kills a child that blocks writing
// past the cap.
type boundedBuf struct {
	bytes.Buffer
	left int
}

func (b *boundedBuf) Write(p []byte) (int, error) {
	if b.left <= 0 {
		return len(p), nil
	}
	if len(p) > b.left {
		b.Buffer.Write(p[:b.left])
		b.left = 0
		return len(p), nil
	}
	b.Buffer.Write(p)
	b.left -= len(p)
	return len(p), nil
}

func newClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2: true, // net/http bundles h2; no vendored x/net/http2 needed
			DialContext:       (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		},
		Timeout: timeout,
	}
}

// Chat streams one completion: the request is built per provider
// protocol (PassCmd supplies the key header when set), the response
// body is parsed line-wise for text deltas. emit receives each delta
// as it arrives; the accumulated text is returned. The key []byte is
// cleared after the request is sent - the header string copy lives
// until the request is collected (Go strings are not zeroable, the
// documented limit); nothing is ever logged.
func Chat(ctx context.Context, p config.AIProvider, model, system, text string, emit func(string)) (string, error) {
	key, err := FetchKey(ctx, p.PassCmd)
	if err != nil {
		return "", err
	}
	defer clear(key)
	def, ok := config.AIProviders[p.Type]
	if !ok {
		return "", fmt.Errorf("ai: unknown provider type %q", p.Type)
	}
	var req *http.Request
	switch def.Protocol {
	case "anthropic":
		req, err = anthropicRequest(ctx, p, model, system, text, key)
	default: // "openai"
		req, err = openAIRequest(ctx, p, model, system, text, key)
	}
	if err != nil {
		return "", err
	}
	resp, err := newClient(timeout(p.Timeout)).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// the status line, never the body: a provider error can echo
		// the request text (F6)
		io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("ai: %s", resp.Status)
	}
	var out strings.Builder
	err = streamBody(resp.Body, def.Protocol, func(delta string) {
		out.WriteString(delta)
		if emit != nil {
			emit(delta)
		}
	})
	if err != nil {
		return out.String(), err
	}
	return out.String(), nil
}

func timeout(sec int) time.Duration {
	if sec <= 0 {
		return 180 * time.Second
	}
	return time.Duration(sec) * time.Second
}

func baseURL(p config.AIProvider, def string) (string, error) {
	u := p.BaseURL
	if u == "" {
		u = def
	}
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return "", fmt.Errorf("ai: base-url %q: http/https only", u)
	}
	return strings.TrimSuffix(u, "/"), nil
}

func anthropicRequest(ctx context.Context, p config.AIProvider, model, system, text string, key []byte) (*http.Request, error) {
	u, err := baseURL(p, defaultBase(p.Type))
	if err != nil {
		return nil, err
	}
	mt := p.MaxTokens
	if mt <= 0 {
		mt = 1024
	}
	body, err := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": mt,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": text}},
		"stream":     true,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", string(key))
	req.Header.Set("anthropic-version", anthropicVersion)
	return req, nil
}

func openAIRequest(ctx context.Context, p config.AIProvider, model, system, text string, key []byte) (*http.Request, error) {
	u, err := baseURL(p, defaultBase(p.Type))
	if err != nil {
		return nil, err
	}
	messages := []map[string]string{}
	if system != "" {
		messages = append(messages, map[string]string{"role": "system", "content": system})
	}
	messages = append(messages, map[string]string{"role": "user", "content": text})
	body, err := json.Marshal(map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   true,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	if len(key) > 0 {
		req.Header.Set("authorization", "Bearer "+string(key))
	}
	return req, nil
}

// streamBody reads the provider's eventstream line-wise and hands each
// text delta to emit, dispatching on the wire protocol (the registry
// row's Protocol): anthropic NDJSON data: lines with
// content_block_delta payloads, or OpenAI SSE data: lines with
// choices[0].delta.content terminated by data: [DONE].
func streamBody(r io.Reader, protocol string, emit func(string)) error {
	sc := newScanner(r)
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimPrefix(line, "data:")
		payload = strings.TrimSpace(payload)
		if payload == "" {
			continue
		}
		switch protocol {
		case "anthropic":
			var ev anthropicEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				return fmt.Errorf("ai: anthropic stream line: %w", err)
			}
			if ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta" {
				emit(ev.Delta.Text)
			}
		default: // "openai"
			if payload == "[DONE]" {
				return nil
			}
			var ev openAIEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				return fmt.Errorf("ai: openai stream line: %w", err)
			}
			if len(ev.Choices) > 0 {
				emit(ev.Choices[0].Delta.Content)
			}
		}
	}
	return sc.Err()
}

func newScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxLine)
	return sc
}

type anthropicEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

type openAIEvent struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}
