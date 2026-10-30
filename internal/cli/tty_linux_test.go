package cli

import "golang.org/x/sys/unix"

// unreadInput is the ioctl that tells how many bytes a terminal holds that
// were typed and not read yet.
const unreadInput = unix.TIOCINQ
