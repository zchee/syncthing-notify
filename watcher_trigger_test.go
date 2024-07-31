// Copyright (c) 2014-2017 The Notify Authors. All rights reserved.
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

//go:build (darwin && kqueue) || (darwin && !cgo) || dragonfly || freebsd || netbsd || openbsd || solaris || illumos
// +build darwin,kqueue darwin,!cgo dragonfly freebsd netbsd openbsd solaris illumos

package notify

import "testing"

func TestWatcherCreateOnly(t *testing.T) {
	w := NewWatcherTest(t, "testdata/vfs.txt", Create)
	defer w.Close()

	cases := [...]WCase{
		create(w, "dir/"),
		create(w, "dir2/"),
	}

	w.ExpectAny(cases[:])
}
