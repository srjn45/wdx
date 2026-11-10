package hublink

import (
	"errors"
	"testing"

	"github.com/srjn45/wdx/internal/hubapi"
)

func TestHubLacks(t *testing.T) {
	t.Parallel()
	needs2 := feature{"sharing", 2}
	for _, tc := range []struct {
		hubAPI string
		f      feature
		lacks  bool
	}{
		{"", needs2, false}, // not welcomed yet: the hub's answer says
		{"1", featureStreams, false},
		{"3", needs2, false},
		{"1", needs2, true},
		{"one", featureStreams, true},
	} {
		err := lacks(tc.hubAPI, tc.f)
		if !tc.lacks {
			if err != nil {
				t.Errorf("hub %q, %s: %v", tc.hubAPI, tc.f.name, err)
			}
			continue
		}
		e, ok := errors.AsType[*HubError](err)
		if !ok || e.Problem.Code != hubapi.ErrorCodeHubVersionUnsupported || e.Problem.Details == nil || (*e.Problem.Details)["update"] != hubapi.BehindHub {
			t.Errorf("hub %q, %s: want hub_version_unsupported naming the hub, got %v", tc.hubAPI, tc.f.name, err)
		}
	}
}
