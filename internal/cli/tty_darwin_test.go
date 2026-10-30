package cli

// unreadInput is the ioctl that tells how many bytes a terminal holds that
// were typed and not read yet: FIONREAD, which x/sys/unix does not define
// for darwin.
const unreadInput = 0x4004667f
