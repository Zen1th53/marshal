//go:build !unix

package statepath

import "os"

func multipleLinks(info os.FileInfo) bool { return false }
