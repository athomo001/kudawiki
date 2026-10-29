package jobs

import (
	"testing"
	"time"
)

// El respaldo corre a la hora configurada: hoy si todavía no pasó, si no mañana.
func TestNextRun(t *testing.T) {
	loc := time.FixedZone("CLT", -3*3600)
	for _, tc := range []struct {
		now, want string
	}{
		{"2026-10-29 01:00", "2026-10-29 03:00"},
		{"2026-10-29 03:00", "2026-10-30 03:00"}, // justo a la hora: ya corrió, el próximo es mañana
		{"2026-10-29 23:59", "2026-10-30 03:00"},
		{"2026-12-31 04:00", "2027-01-01 03:00"},
	} {
		now, _ := time.ParseInLocation("2006-01-02 15:04", tc.now, loc)
		if got := nextRun(now, 3, 0).Format("2006-01-02 15:04"); got != tc.want {
			t.Errorf("nextRun(%s) = %s, want %s", tc.now, got, tc.want)
		}
	}
}
