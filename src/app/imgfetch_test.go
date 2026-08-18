package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 31 % 256), uint8(y * 17 % 256), uint8(x + y), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFetchRemoteImage(t *testing.T) {
	img := testPNG(t, 40, 20)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.png":
			w.Write(img)
		case "/nope":
			w.WriteHeader(http.StatusNotFound)
		case "/text.txt":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("not an image"))
		case "/huge.png":
			w.Write(bytes.Repeat([]byte{0}, maxRemoteImg+1))
		}
	}))
	defer srv.Close()

	if got, err := fetchRemoteImage(srv.URL+"/ok.png", false); err != nil || !bytes.Equal(got, img) {
		t.Fatalf("ok fetch: %v, %v", len(got), err)
	}
	if _, err := fetchRemoteImage(srv.URL+"/nope", false); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("non-200 must error, got %v", err)
	}
	if _, err := fetchRemoteImage(srv.URL+"/text.txt", false); err == nil || !strings.Contains(err.Error(), "not an image") {
		t.Fatalf("non-image must error, got %v", err)
	}
	if _, err := fetchRemoteImage(srv.URL+"/huge.png", false); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized must error, got %v", err)
	}
	if _, err := fetchRemoteImage("file:///etc/hostname", false); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("bad scheme must error, got %v", err)
	}

	// tracking pixels: 1x1 beacons drop by default, lift with the config
	// opt-in
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(testPNG(t, 1, 1))
	}))
	defer srv2.Close()
	if _, err := fetchRemoteImage(srv2.URL, false); err == nil || !strings.Contains(err.Error(), "tracking") {
		t.Fatalf("1x1 must be blocked, got %v", err)
	}
	if got, err := fetchRemoteImage(srv2.URL, true); err != nil || len(got) == 0 {
		t.Fatalf("1x1 with allow-tracking must fetch, got %v", err)
	}
	// a 1x1 image that is not a tracking pixel: blocked identically (the
	// dims are the signal, there is no way to tell intent)
	if _, err := fetchRemoteImage(srv.URL+"/ok.png", false); err != nil {
		t.Fatalf("non-1x1 must pass the tracking check, got %v", err)
	}
}
