# cancelreader

github.com/muesli/cancelreader v0.2.2 with one change, which `go.mod` replaces it by.

Bubble Tea ends a program by cancelling its input's reader, waiting half a second for the read to end, and closing the reader even if it has not. On a loaded machine it may not have: the read then uses what `Close` has closed. The race detector fails the test running, and a read that reaches its wait after the close can wait forever, on a descriptor closed or by then another's (`docs/FLAKES.md`).

So here `Close` cancels and waits for a read in progress, in `cancelreader_linux.go` and `cancelreader_bsd.go`. It cannot wait long: a cancel wakes the read at once, and the read of the file itself, which `Close` does not touch, is outside what it waits for. `TestInputReaderClosedDuringARead` in `internal/cli` checks it.

Drop this copy when upstream closes safely.
