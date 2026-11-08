//go:build !unix

package store

import "io/fs"

func checkOwner(fs.FileInfo) error { return nil }
