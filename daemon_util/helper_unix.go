// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

//go:build darwin || freebsd || linux

package daemon_util

import (
	"fmt"
	"os"
)

const statNotInstalled = "Service not installed"

func checkPrivileges() (bool, error) {
	if os.Geteuid() != 0 {
		return false, ErrRootPrivileges
	}
	return true, nil
}

func validateExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: path must name a regular file", ErrInvalidExecutablePath)
	}
	if info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("%w: file is not executable", ErrInvalidExecutablePath)
	}
	return nil
}

func createServiceLinks(target string, links []string) error {
	created := make([]string, 0, len(links))
	for _, link := range links {
		if err := os.Symlink(target, link); err != nil {
			for _, path := range created {
				_ = os.Remove(path)
			}
			return err
		}
		created = append(created, link)
	}
	return nil
}
