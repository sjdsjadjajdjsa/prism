//go:build !linux

package main

import "context"

// watch polls for changes where inotify is not available.
func (t *projectTree) watch(ctx context.Context) error { return t.poll(ctx) }
