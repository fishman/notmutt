package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"notmutt/cache"
	"notmutt/core"
)

const scanPage = 40

// cacheJob fills the MIME cache for visible rows, budgeted to 2
// concurrent scans. Results land in the row model; the TUI repaints on
// any event.
type cacheJob struct {
	bus   *core.Bus
	view  *core.View
	cache cache.Cache
}

func newCacheJob(bus *core.Bus, w workerAPI, view *core.View, dbPath string) *cacheJob {
	cj := &cacheJob{bus: bus, view: view}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return cj
	}
	c, err := cache.Open(dbPath)
	if err != nil {
		return cj
	}
	cj.cache = c
	return cj
}

func (c *cacheJob) Run(ctx context.Context) {
	if c.cache == nil {
		return
	}
	ch := c.bus.Subscribe()
	sem := make(chan struct{}, 2)
	c.scanVisible(sem) // startup scan covers whatever rows already exist; ViewDiff drives steady state
	for {
		select {
		case <-ctx.Done():
			c.cache.Close()
			return
		case e := <-ch:
			switch e.(type) {
			case core.ViewDiff, core.QueryBatch:
				c.scanVisible(sem)
			}
		}
	}
}

func scanWorthy(r core.Row) bool {
	m := r.Msg
	return m != nil && len(m.Paths) > 0 && len(m.Atts) == 0
}

func (c *cacheJob) scanVisible(sem chan struct{}) {
	rows := c.view.Rows()
	if len(rows) > scanPage {
		rows = rows[:scanPage]
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var puts []cache.Entry
	total := 0
	for _, r := range rows {
		if scanWorthy(r) {
			total++
		}
	}
	done := 0
	for _, r := range rows {
		if !scanWorthy(r) {
			continue
		}
		m := r.Msg
		wg.Add(1)
		go func() {
			defer wg.Done()
			// registered after wg.Done: LIFO runs the publish first, so
			// wg.Wait() is a publish barrier
			defer func() {
				mu.Lock()
				done++
				c.bus.Publish(core.Progress{Job: "cache", View: c.view.Name, Done: done, Total: total})
				mu.Unlock()
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			for _, p := range m.Paths {
				fi, err := os.Stat(p)
				if err != nil {
					continue
				}
				k := cache.Key{Path: p, Size: fi.Size(), Mtime: fi.ModTime().Unix()}
				if atts, ok, err := c.cache.Get(k); err == nil && ok {
					c.view.SetAtts(m.ID, atts)
					c.bus.Publish(core.CacheResult{MsgID: m.ID, Atts: atts})
					return
				}
				atts, err := cache.ScanAttachments(p)
				if err != nil {
					continue
				}
				// one PutBatch per scan run: per-message transactions
				// fsync each write
				mu.Lock()
				puts = append(puts, cache.Entry{Key: k, Atts: atts})
				mu.Unlock()
				c.view.SetAtts(m.ID, atts)
				c.bus.Publish(core.CacheResult{MsgID: m.ID, Atts: atts})
				return
			}
		}()
	}
	wg.Wait()
	if len(puts) > 0 {
		c.cache.PutBatch(puts)
	}
}
