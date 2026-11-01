package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/repo"
)

const (
	uaChromeWin  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	uaFirefoxMac = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14.6; rv:141.0) Gecko/20100101 Firefox/141.0"
)

func TestDeviceOf(t *testing.T) {
	for ua, want := range map[string]Device{
		uaChromeWin:  {"Chrome", "Windows"},
		uaFirefoxMac: {"Firefox", "macOS"},
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0":           {"Edge", "Windows"},
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 OPR/122.0.0.0":                     {"Opera", "Linux"},
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Mobile/15E148 Safari/604.1": {"Safari", "iOS"},
		"Mozilla/5.0 (iPad; CPU OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/140.0.0.0 Mobile/15E148 Safari/604.1":       {"Chrome", "iOS"},
		"Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36":                   {"Chrome", "Android"},
		"Mozilla/5.0 (X11; CrOS x86_64 16181.61.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36":                         {"Chrome", "ChromeOS"},
		"curl/8.9.1": {},
		"":           {},
	} {
		if got := DeviceOf(ua); got != want {
			t.Errorf("DeviceOf(%q) = %+v, want %+v", ua, got, want)
		}
	}
	if got := (Device{}).label(); got != "?" {
		t.Errorf("unknown device label = %q", got)
	}
}

// §8.2: mis sesiones (solo las propias y vigentes) y el aviso cuando la IP o el navegador son nuevos.
func TestSessionsAndNewLogin(t *testing.T) {
	s, r, c, m := withMail(t)
	ctx := context.Background()
	login := func(ip, ua string) string {
		t.Helper()
		token, err := session(s.Login(WithClient(ctx, Client{IP: ip, UserAgent: ua}), ip, "ana@empresa.cl", goodPassword))
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	first := login("10.0.0.1", uaChromeWin)
	if n := len(m.of("new_login")); n != 0 {
		t.Fatalf("the first login of an account sent %d notices", n)
	}
	login("10.0.0.1", uaChromeWin)
	if n := len(m.of("new_login")); n != 0 {
		t.Errorf("same IP and browser sent %d notices", n)
	}
	login("10.0.0.9", uaChromeWin)
	login("10.0.0.1", uaFirefoxMac)
	notices := m.of("new_login")
	if len(notices) != 2 {
		t.Fatalf("new IP and new browser: %d notices, want 2", len(notices))
	}
	if v := notices[1].Vars; v["device"] != "Firefox · macOS" || v["ip"] != "10.0.0.1" || notices[1].URL != "https://wiki.acme.cl/me/profile#sessions" {
		t.Errorf("notice = %+v %s", v, notices[1].URL)
	}
	// La versión del navegador no lo hace nuevo.
	login("10.0.0.9", uaChromeWin[:len(uaChromeWin)-len("140.0.0.0 Safari/537.36")]+"141.0.0.0 Safari/537.36")
	if n := len(m.of("new_login")); n != 2 {
		t.Errorf("a browser update sent a notice: %d", n)
	}
	// Sin correo configurado no hay aviso, pero lo visto queda anotado.
	m.on = false
	login("10.0.0.50", uaChromeWin)
	m.on = true
	login("10.0.0.50", uaChromeWin)
	if n := len(m.of("new_login")); n != 2 {
		t.Errorf("a login seen while mail was off counted as new later: %d", n)
	}

	ana, _ := r.UserByEmail(ctx, "ana@empresa.cl")
	list, err := s.Sessions(ctx, ana, first)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 7 {
		t.Fatalf("%d sessions, want 7", len(list))
	}
	var current int
	other := ""
	for _, x := range list {
		if !x.Current {
			other = x.ID
		}
		if x.Current {
			current++
			if x.Device != (Device{"Chrome", "Windows"}) || x.IP != "10.0.0.1" {
				t.Errorf("current session = %+v", x)
			}
		}
	}
	if current != 1 || !list[0].Current {
		t.Errorf("%d sessions marked current; first is current: %v", current, list[0].Current)
	}

	// La de otra persona no se cierra: el mismo error que una que no existe.
	beto := repo.User{ID: "01BETO0000000000000000000"}
	if err := s.RevokeSession(ctx, beto, other); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("revoke someone else's session: %v", err)
	}
	if err := s.RevokeSession(ctx, ana, other); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RevokeOtherSessions(ctx, ana, first); err != nil || n != 5 {
		t.Errorf("revoke others: %d %v, want 5", n, err)
	}
	if list, _ = s.Sessions(ctx, ana, first); len(list) != 1 || !list[0].Current {
		t.Errorf("after revoking the others: %+v", list)
	}
	// Las vencidas ya no se muestran.
	c.t = c.t.Add(sessionIdle + time.Minute)
	if list, _ = s.Sessions(ctx, ana, first); len(list) != 0 {
		t.Errorf("an expired session is listed: %+v", list)
	}

	// El superadmin cierra todas las de otra persona (B16); sin persona, ninguna.
	login("10.0.0.1", uaChromeWin)
	if n, err := s.AdminRevokeSessions(ctx, ana.ID); err != nil || n != 2 {
		t.Errorf("admin revoke: %d %v, want 2", n, err)
	}
	if _, err := s.AdminRevokeSessions(ctx, ""); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("admin revoke without a person: %v", err)
	}
}
