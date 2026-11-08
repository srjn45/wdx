package authcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func setUp(t *testing.T) (*Files, Pending, string) {
	t.Helper()
	p, err := Generate("wdx", "ann@desktop")
	if err != nil {
		t.Fatal(err)
	}
	f := Open(t.TempDir())
	key, err := f.Write(context.Background(), p.Secret, Step(now)-5)
	if err != nil {
		t.Fatal(err)
	}
	return f, p, key
}

func code(t *testing.T, secret string, step int64) string {
	t.Helper()
	c, err := CodeAt(secret, step)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGenerate(t *testing.T) {
	t.Parallel()
	p, err := Generate("wdx", "ann@desktop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.URL, "otpauth://totp/wdx:ann@desktop?") || !strings.Contains(p.URL, "secret="+p.Secret) || !strings.Contains(p.URL, "period=30") {
		t.Errorf("URL = %q", p.URL)
	}
}

func TestFilesAndModes(t *testing.T) {
	t.Parallel()
	f, _, key := setUp(t)
	for _, name := range []string{"code.secret", "code.step", "recovery.hash"} {
		info, err := os.Stat(filepath.Join(f.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", name, info.Mode().Perm())
		}
	}
	b, err := os.ReadFile(filepath.Join(f.dir, "recovery.hash"))
	if err != nil || strings.Contains(string(b), key) || len(strings.TrimSpace(string(b))) != 64 {
		t.Errorf("recovery.hash = %q, %v; want the key's SHA-256 only", b, err)
	}
	if on, err := f.SetUp(); !on || err != nil {
		t.Errorf("SetUp = %v, %v", on, err)
	}
}

func TestNotSetUp(t *testing.T) {
	t.Parallel()
	f := Open(t.TempDir())
	if on, err := f.SetUp(); on || err != nil {
		t.Errorf("SetUp = %v, %v; want false", on, err)
	}
	if _, err := f.Check(context.Background(), "123456", now); !errors.Is(err, ErrNotSetUp) {
		t.Errorf("Check = %v, want ErrNotSetUp", err)
	}
	if _, err := f.CodeAt(context.Background(), Step(now), now); !errors.Is(err, ErrNotSetUp) {
		t.Errorf("CodeAt = %v, want ErrNotSetUp", err)
	}
	if _, err := f.CheckRecoveryKey("ABCD-EFGH-JKMN-PQRS-TVWX"); !errors.Is(err, ErrNotSetUp) {
		t.Errorf("CheckRecoveryKey = %v, want ErrNotSetUp", err)
	}
}

func TestCodeUsedOnce(t *testing.T) {
	t.Parallel()
	f, p, _ := setUp(t)
	ctx := context.Background()
	c := code(t, p.Secret, Step(now))
	step, err := f.Check(ctx, c, now)
	if err != nil || step != Step(now) {
		t.Fatalf("Check = %d, %v; want now's step", step, err)
	}
	if _, err := f.Check(ctx, c, now); !errors.Is(err, ErrWrongCode) {
		t.Errorf("Check again = %v, want ErrWrongCode", err)
	}
	// The step before is no longer after the last accepted either.
	if _, err := f.Check(ctx, code(t, p.Secret, Step(now)-1), now); !errors.Is(err, ErrWrongCode) {
		t.Errorf("Check of the step before = %v, want ErrWrongCode", err)
	}
	if _, err := f.Check(ctx, code(t, p.Secret, Step(now)+1), now); err != nil {
		t.Errorf("Check of the step after = %v, want it accepted", err)
	}
}

func TestStepRange(t *testing.T) {
	t.Parallel()
	f, p, _ := setUp(t)
	ctx := context.Background()
	for _, off := range []int64{-2, 2} {
		if _, err := f.Check(ctx, code(t, p.Secret, Step(now)+off), now); !errors.Is(err, ErrWrongCode) {
			t.Errorf("a code %d steps off = %v, want ErrWrongCode", off, err)
		}
		if _, err := f.CodeAt(ctx, Step(now)+off, now); !errors.Is(err, ErrStepRefused) {
			t.Errorf("CodeAt %d steps off = %v, want ErrStepRefused", off, err)
		}
	}
	got, err := f.CodeAt(ctx, Step(now)-1, now)
	if err != nil || got != code(t, p.Secret, Step(now)-1) {
		t.Fatalf("CodeAt the step before = %q, %v", got, err)
	}
	if err := f.Use(ctx, Step(now)); err != nil {
		t.Fatal(err)
	}
	if err := f.Use(ctx, Step(now)); !errors.Is(err, ErrStepRefused) {
		t.Errorf("Use again = %v, want ErrStepRefused", err)
	}
	if _, err := f.CodeAt(ctx, Step(now), now); !errors.Is(err, ErrStepRefused) {
		t.Errorf("CodeAt a used step = %v, want ErrStepRefused", err)
	}
	// The step survives the Files, as across a restart.
	if _, err := Open(f.dir).CodeAt(ctx, Step(now), now); !errors.Is(err, ErrStepRefused) {
		t.Errorf("CodeAt a used step, opened again = %v, want ErrStepRefused", err)
	}
}

func TestRecoveryKey(t *testing.T) {
	t.Parallel()
	f, p, key := setUp(t)
	groups := strings.Split(key, "-")
	if len(groups) != 5 {
		t.Fatalf("key = %q, want five groups", key)
	}
	for _, g := range groups {
		if len(g) != 4 {
			t.Fatalf("key = %q, want groups of four", key)
		}
	}
	for _, typed := range []string{key, strings.ToLower(key), strings.ReplaceAll(key, "-", " "), strings.ReplaceAll(key, "-", "")} {
		if ok, err := f.CheckRecoveryKey(typed); !ok || err != nil {
			t.Errorf("CheckRecoveryKey(%q) = %v, %v; want it right", typed, ok, err)
		}
	}
	for _, wrong := range []string{"", "ABCD", key + "A", NewRecoveryKey(), "!!!!-!!!!-!!!!-!!!!-!!!!"} {
		if ok, err := f.CheckRecoveryKey(wrong); ok || err != nil {
			t.Errorf("CheckRecoveryKey(%q) = %v, %v; want it wrong", wrong, ok, err)
		}
	}
	// A new setup spends the old key.
	if _, err := f.Write(context.Background(), p.Secret, Step(now)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.CheckRecoveryKey(key); ok {
		t.Error("want the old recovery key spent once a new one is written")
	}
}

func TestRecoveryKeyLookalikes(t *testing.T) {
	t.Parallel()
	if a, _ := normalRecoveryKey("ILO0-1111-1111-1111-1111"); a != "11001111111111111111" {
		t.Errorf("normal = %q, want I, L and O read as 1, 1 and 0", a)
	}
}
