package health

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// Un servidor NTP de prueba adelantado 5 s: el desfase se mide y es un problema de Salud;
// uno que no responde es un aviso, no un error.
func TestClock(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no UDP:", err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 48)
		for {
			_, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			resp := make([]byte, 48)
			resp[0], resp[1] = 0x24, 2 // versión 4, modo servidor, stratum 2
			now := time.Now().Add(5 * time.Second)
			sec := uint32(now.Unix() + ntpEpoch)
			frac := uint32((uint64(now.Nanosecond()) << 32) / 1e9)
			for _, off := range []int{32, 40} {
				binary.BigEndian.PutUint32(resp[off:], sec)
				binary.BigEndian.PutUint32(resp[off+4:], frac)
			}
			pc.WriteTo(resp, addr)
		}
	}()
	c := &Clock{Server: pc.LocalAddr().String(), Now: time.Now}
	st, ok := c.State(context.Background())
	if !ok || st.Error != "" || st.OffsetMS < 4500 || st.OffsetMS > 5500 {
		t.Fatalf("state: %+v", st)
	}
	if ps := ClockChecks(st, ok); len(ps) != 1 || ps[0].Check != "clock.skew" {
		t.Errorf("problems: %+v", ps)
	}
	if _, ok := (&Clock{Now: time.Now}).State(context.Background()); ok {
		t.Error("a clock without server reported a state")
	}
	dead := &Clock{Server: "127.0.0.1:1", Now: time.Now}
	if st, ok := dead.State(context.Background()); !ok || st.Error == "" || ClockChecks(st, ok)[0].Check != "clock.unreachable" {
		t.Errorf("unreachable: %+v", st)
	}
}
