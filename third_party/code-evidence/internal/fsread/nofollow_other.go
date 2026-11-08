//go:build !unix

package fsread

const oNoFollow = 0

// oNonBlock keeps an open from blocking when a FIFO replaces a regular
// file between the Lstat check and the open (regular files ignore it).
const oNonBlock = 0
