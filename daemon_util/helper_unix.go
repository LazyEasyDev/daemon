// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by
// license that can be found in the LICENSE file.

//go:build darwin || freebsd || linux

package daemon_util

import (
	"fmt"
	"os"
	"runtime"
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
	return validateNativeExecutable(path)
}

func validateNativeExecutable(path string) error {
	magic, err := readExecutableMagic(path)
	if runtime.GOOS == "darwin" {
		if err == nil {
			switch magic {
			case [4]byte{0xfe, 0xed, 0xfa, 0xce},
				[4]byte{0xce, 0xfa, 0xed, 0xfe},
				[4]byte{0xfe, 0xed, 0xfa, 0xcf},
				[4]byte{0xcf, 0xfa, 0xed, 0xfe},
				[4]byte{0xca, 0xfe, 0xba, 0xbe},
				[4]byte{0xbe, 0xba, 0xfe, 0xca},
				[4]byte{0xca, 0xfe, 0xba, 0xbf},
				[4]byte{0xbf, 0xba, 0xfe, 0xca}:
				return nil
			}
		}
		return fmt.Errorf("%w: file is not a native Mach-O executable", ErrInvalidExecutablePath)
	}

	if err == nil && magic == [4]byte{0x7f, 'E', 'L', 'F'} {
		return nil
	}
	return fmt.Errorf("%w: file is not a native ELF executable", ErrInvalidExecutablePath)
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
