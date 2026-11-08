# cpace

`filippo.io/cpace` at v0.0.0-20210101143347-24d601e2e469 (its one version, of 2021-01-01, from proxy.golang.org), which `go.mod` replaces it by. `cpace.go`, `cpace_test.go` and `LICENSE` (BSD-3-Clause) are as they came; `go.mod` here moves its requirements up to those wdx uses.

CPace is the password-authenticated key exchange that opens a session between two nodes with the host's authenticator code (the wdx doc HUB_TUNNEL, "Opening a session"). It is copied rather than required because upstream says it is experimental, it is not maintained, and it follows draft-haase-cpace-01, of 2021, which the IRTF has since restructured: its test vector is its own, not the current draft's. A copy keeps what wdx runs fixed and in view. It is the one piece of cryptography wdx carries itself rather than taking from Go's standard library, and is named as such in the scope of the outside security review.

It uses `github.com/gtank/ristretto255` (BSD-3-Clause), here at v0.2.0 in place of upstream's v0.1.2, so that the group runs on `filippo.io/edwards25519` (BSD-3-Clause) rather than v0.1.2's own copy of it; `cpace.go` needs no change for it, and `TestTranscript`'s vector still matches. And `golang.org/x/crypto`'s `hkdf` and `cryptobyte` (BSD-3-Clause), which wdx requires already.

`make test` runs its tests here, as a module of its own.

Drop this copy for a maintained CPace that follows the published RFC, in Go's standard library or `x/crypto`, when there is one: the context and MACs `internal/remote` adds stay as they are.
