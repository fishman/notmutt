// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

// The remote image fetch (the render-images remote mode): http(s) srcs
// from the mail fetch ONLY when the user cycles the render-images key
// to the remote mode - scheme whitelist, size cap, timeout, off the
// render path. The bytes land as ImageFetched; the TUI attaches them
// to the image lines.

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"notmutt/core"
)

const maxRemoteImg = 10 << 20 // 10MB cap: a mail-embedded fetch budget

// fetchImage fetches one remote image src (the seam target): the
// scheme whitelist (http/https only - the default client already
// refuses non-http redirects), a size cap and a 10s timeout. The
// result publishes as ImageFetched; failures keep the Alt row.
// allowTracking lifts the 1x1 tracking-pixel block ([pager]
// allow-tracking-images).
func fetchImage(bus *core.Bus, src string, allowTracking bool) {
	data, err := fetchRemoteImage(src, allowTracking)
	bus.Publish(core.ImageFetched{URL: src, Data: data, Err: err})
}

func fetchRemoteImage(src string, allowTracking bool) ([]byte, error) {
	u, err := url.Parse(src)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("unsupported image url")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(src)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image fetch: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteImg+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRemoteImg {
		return nil, errors.New("image too large")
	}
	if !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return nil, errors.New("not an image")
	}
	// tracking pixels (1x1 web beacons) drop unless the config allows
	// them - the read receipt never renders (and the config opt-in is
	// explicit)
	if !allowTracking {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil && cfg.Width == 1 && cfg.Height == 1 {
			return nil, errors.New("tracking pixel")
		}
	}
	return data, nil
}
