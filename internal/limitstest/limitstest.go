// Package limitstest lengthens, in tests, the limits on how long wdx's
// processes wait for each other: a struct of time.Duration fields, such as
// hublink.Limits, remote.Limits and the hub's server.Limits.
package limitstest

import (
	"reflect"
	"testing"
	"time"

	"github.com/srjn45/wdx/internal/runtest"
	"github.com/srjn45/wdx/internal/waittest"
)

// Lengthen sets every time.Duration field of *limits to what is left of the
// test binary's -timeout, so that a loaded machine slows a test rather than
// fails it: a test fails on what it checks, or at its deadline. A test of one
// limit shortens that one after. In a run that keeps wdx's own limits
// (WDX_TEST_PRODUCTION_LIMITS=1) it leaves them alone.
func Lengthen[L any](t *testing.T, limits *L) {
	t.Helper()
	v := reflect.ValueOf(limits).Elem()
	if v.Kind() != reflect.Struct {
		t.Fatalf("limitstest: %T is not a struct of limits", *limits)
	}
	if runtest.ProductionLimits() {
		return
	}
	limit := reflect.ValueOf(waittest.Limit(t))
	for i := range v.NumField() {
		if f := v.Field(i); f.Type() == reflect.TypeFor[time.Duration]() && f.CanSet() {
			f.Set(limit)
		}
	}
}
