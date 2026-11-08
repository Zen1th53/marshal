//go:build unix

package fsread

import "syscall"

const oNoFollow = syscall.O_NOFOLLOW

// oNonBlock keeps an open from blocking when a FIFO replaces a regular
// file between the Lstat check and the open (regular files ignore it).
const oNonBlock = syscall.O_NONBLOCK
