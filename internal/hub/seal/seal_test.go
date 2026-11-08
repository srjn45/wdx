package seal

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

func key(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestOpenWhatWasSealed(t *testing.T) {
	t.Parallel()
	k := key(t)
	plaintext, ad := []byte("JBSWY3DPEHPK3PXP"), []byte("usr-1a2b3c4d")
	sealed, err := Seal(k, plaintext, ad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatalf("the plaintext is in the sealed bytes %x", sealed)
	}
	got, err := Open(k, sealed, ad)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Fatalf("Open = %q, %v; want %q", got, err, plaintext)
	}
	again, err := Seal(k, plaintext, ad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(again, sealed) {
		t.Error("two seals of one plaintext are the same: the nonce is not random")
	}
}

func TestOpenRefuses(t *testing.T) {
	t.Parallel()
	k := key(t)
	ad := []byte("usr-1a2b3c4d")
	sealed, err := Seal(k, []byte("secret"), ad)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name       string
		key        []byte
		sealed, ad []byte
	}{
		{"a changed byte of the nonce", k, flip(sealed, 0), ad},
		{"a changed byte of the ciphertext", k, flip(sealed, len(sealed)-1), ad},
		{"another key", key(t), sealed, ad},
		{"other additional data", k, sealed, []byte("usr-5e6f7a8b")},
		{"bytes too few", k, sealed[:10], ad},
	} {
		if got, err := Open(c.key, c.sealed, c.ad); !errors.Is(err, ErrOpen) {
			t.Errorf("%s: Open = %q, %v; want ErrOpen", c.name, got, err)
		}
	}
}

func TestKeySize(t *testing.T) {
	t.Parallel()
	if _, err := Seal(make([]byte, 16), []byte("x"), nil); err == nil {
		t.Error("Seal took a 16-byte key")
	}
	if _, err := Open(make([]byte, 31), make([]byte, 64), nil); err == nil || errors.Is(err, ErrOpen) {
		t.Errorf("Open with a 31-byte key = %v, want the key refused", err)
	}
}

func flip(b []byte, i int) []byte {
	c := bytes.Clone(b)
	c[i] ^= 1
	return c
}
