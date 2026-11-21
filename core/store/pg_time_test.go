package store

import (
	"testing"
	"time"

	"omixor-core/model"
)

func TestTimeHelpers(t *testing.T) {
	loc := time.FixedZone("x", 8*3600)
	ts := time.Date(2026, 1, 2, 3, 4, 5, 999, loc)
	if g := secs(ts); g.Location() != time.UTC || g.Nanosecond() != 0 || !g.Equal(ts.Truncate(time.Second)) {
		t.Errorf("secs = %v", g)
	}
	if nullSecs(model.Time{}) != nil || optSecs(nil) != nil || optSecs(&model.Time{}) != nil {
		t.Error("零時間/nil 應是 NULL")
	}
	if g := nullSecs(model.Time{Time: ts}); g == nil || g.Nanosecond() != 0 || g.Location() != time.UTC {
		t.Errorf("nullSecs = %v", g)
	}
	if fromNull(nil) != (model.Time{}) || tp(nil) != nil {
		t.Error("nil")
	}
	if g := fromNull(&ts); g.Location() != time.UTC || !g.Equal(ts) || g.Nanosecond() != 999 {
		t.Errorf("fromNull = %v", g)
	}
	if g := tp(&ts); g == nil || g.Location() != time.UTC || !g.Equal(ts) {
		t.Errorf("tp = %v", g)
	}
}
