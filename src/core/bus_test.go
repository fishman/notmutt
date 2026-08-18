// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"testing"
	"time"
)

func TestBusFanout(t *testing.T) {
	b := NewBus()
	ch1 := b.Subscribe()
	ch2 := b.Subscribe()
	b.Publish(WorkerDone{Job: "new"})
	select {
	case e := <-ch1:
		if e.(WorkerDone).Job != "new" {
			t.Fatalf("wrong event: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber 1 got nothing")
	}
	select {
	case <-ch2:
	case <-time.After(time.Second):
		t.Fatal("subscriber 2 got nothing")
	}
}

func TestBusSlowSubscriberDrops(t *testing.T) {
	b := NewBus()
	ch := b.Subscribe() // nobody drains this one
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			b.Publish(WorkerDone{Job: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a slow subscriber")
	}
	got := 0
drain:
	for {
		select {
		case <-ch:
			got++
		case <-time.After(100 * time.Millisecond):
			break drain
		}
	}
	if got != 64 {
		t.Fatalf("got %d events, want 64 (buffer capacity)", got)
	}
}

func TestBusAddressIndexSnapshot(t *testing.T) {
	b := NewBus()
	b.Publish(AddressIndex{Addrs: []AddressEntry{{Addr: "a@b.c", Name: "Ann"}}})
	got, ok := b.LatestAddressIndex()
	if !ok || len(got.Addrs) != 1 || got.Addrs[0].Addr != "a@b.c" {
		t.Fatalf("snapshot wrong: %+v %v", got, ok)
	}
}
