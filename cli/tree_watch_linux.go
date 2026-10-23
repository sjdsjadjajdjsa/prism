//go:build linux

package main

import (
	"context"
	"path"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const treeWatchMask = syscall.IN_CREATE | syscall.IN_DELETE | syscall.IN_CLOSE_WRITE | syscall.IN_MOVED_FROM |
	syscall.IN_MOVED_TO | syscall.IN_DELETE_SELF | syscall.IN_MODIFY

// watch keeps the tree current with inotify: one watch per folder, events batched for 150 ms,
// then only the changed paths are refreshed. It costs no CPU while nothing changes.
func (t *projectTree) watch(ctx context.Context) error {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC)
	if err != nil {
		return err
	}
	var mu sync.Mutex
	dirs := map[int32]string{}
	watched := map[string]bool{}
	add := func(rel string) {
		mu.Lock()
		defer mu.Unlock()
		if watched[rel] || len(watched) >= 8192 {
			return
		}
		wd, err := syscall.InotifyAddWatch(fd, filepath.Join(t.root, filepath.FromSlash(rel)), treeWatchMask)
		if err == nil {
			dirs[int32(wd)], watched[rel] = rel, true
		}
	}
	addAll := func() {
		t.mu.Lock()
		var list []string
		for p, n := range t.nodes {
			if n.Dir {
				list = append(list, p)
			}
		}
		t.mu.Unlock()
		add("")
		for _, p := range list {
			add(p)
		}
	}
	addAll()
	forget := func(wd int32) {
		mu.Lock()
		defer mu.Unlock()
		if rel, ok := dirs[wd]; ok {
			delete(dirs, wd)
			delete(watched, rel)
		}
	}
	events := make(chan string, 1024)
	go func() {
		<-ctx.Done()
		syscall.Close(fd)
	}()
	go func() {
		defer close(events)
		buf := make([]byte, 64<<10)
		for {
			n, err := syscall.Read(fd, buf)
			if err != nil || n <= 0 {
				return
			}
			for off := 0; off+syscall.SizeofInotifyEvent <= n; {
				ev := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[off]))
				name := ""
				if ev.Len > 0 {
					raw := buf[off+syscall.SizeofInotifyEvent : off+syscall.SizeofInotifyEvent+int(ev.Len)]
					for i, c := range raw {
						if c == 0 {
							raw = raw[:i]
							break
						}
					}
					name = string(raw)
				}
				off += syscall.SizeofInotifyEvent + int(ev.Len)
				if ev.Mask&syscall.IN_Q_OVERFLOW != 0 {
					events <- "\x00rescan"
					continue
				}
				if ev.Mask&syscall.IN_IGNORED != 0 {
					forget(ev.Wd)
					continue
				}
				mu.Lock()
				dir, ok := dirs[ev.Wd]
				mu.Unlock()
				if ok && name != "" {
					events <- path.Join(dir, name)
				}
			}
		}
	}()
	for first := range events {
		batch := map[string]bool{first: true}
		timer := time.NewTimer(150 * time.Millisecond)
	collect:
		for {
			select {
			case p, ok := <-events:
				if !ok {
					break collect
				}
				batch[p] = true
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		var paths []string
		for p := range batch {
			if p == "\x00rescan" || path.Base(p) == ".gitignore" {
				t.scan()
				addAll()
				paths = nil
				break
			}
			paths = append(paths, p)
		}
		if len(paths) > 0 {
			t.refresh(paths)
			addAll()
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// The inotify descriptor failed: keep the tree current by polling instead.
	return t.poll(ctx)
}
