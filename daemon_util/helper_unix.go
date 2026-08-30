// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

//go:build darwin || freebsd || linux

package daemon_util

import (
	"os/exec"
	"strconv"
	"strings"
)

const statNotInstalled = "Service not installed"

func checkPrivileges() (bool, error) {
	output, err := exec.Command("id", "-g").Output()
	if err != nil {
		return false, ErrUnsupportedSystem
	}

	gid, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 32)
	if err != nil {
		return false, ErrUnsupportedSystem
	}
	if gid != 0 {
		return false, ErrRootPrivileges
	}
	return true, nil
}
