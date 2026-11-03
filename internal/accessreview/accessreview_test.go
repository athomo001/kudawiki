package accessreview

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/database/dbtest"
	"github.com/athomo001/kudawiki/internal/mail"
	"github.com/athomo001/kudawiki/internal/repo"
	"github.com/oklog/ulid/v2"
)

type fakeMail struct{ sent []mail.Message }

func (m *fakeMail) Enabled(context.Context) (bool, error) { return true, nil }
func (m *fakeMail) Enqueue(_ context.Context, msg mail.Message) error {
	m.sent = append(m.sent, msg)
	return nil
}

// §21.2.7: al vencer el plazo, los admins del área reciben un aviso (uno por vencimiento, no
// uno por pasada) y los superadmins un correo; confirmar corre el plazo y no vuelve a avisar
// hasta el próximo vencimiento.
func TestNotices(t *testing.T) {
	ctx := context.Background()
	db := dbtest.Open(t)
	store := repo.New(db)
	rec := audit.NewRecorder(store, time.Now, slog.New(slog.DiscardHandler))
	users := map[string]repo.User{}
	for _, name := range []string{"super", "admin", "editor"} {
		u := repo.User{ID: ulid.Make().String(), Email: name + "@x.cl", FullName: name, IsActive: true, Locale: "es", IsGlobalAdmin: name == "super"}
		if err := store.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		users[name] = u
	}
	ws := repo.Workspace{ID: ulid.Make().String(), Slug: "ti", Name: "TI"}
	if err := store.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	sc := repo.Scope{WsID: ws.ID, MaxRank: 3}
	store.SetMember(ctx, sc, users["admin"].ID, "admin")
	store.SetMember(ctx, sc, users["editor"].ID, "editor")

	clock := time.Now()
	m := &fakeMail{}
	s := NewService(store, rec, m, 90*24*time.Hour, "https://wiki.x.cl/", func() time.Time { return clock })
	unread := func(name string) int {
		ns, _, err := store.Notifications(ctx, users[name].ID, 50)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, x := range ns {
			if x.Kind == "access_review_due" && !x.Read {
				n++
			}
		}
		return n
	}

	if err := s.Notices(ctx); err != nil || unread("admin") != 0 || len(m.sent) != 0 {
		t.Fatalf("before the due date: %v, %d, %v", err, unread("admin"), m.sent)
	}
	clock = clock.Add(91 * 24 * time.Hour)
	for range 2 { // la segunda pasada no repite
		if err := s.Notices(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if unread("admin") != 1 || unread("editor") != 0 {
		t.Errorf("area notice: admin %d, editor %d", unread("admin"), unread("editor"))
	}
	if len(m.sent) != 1 || m.sent[0].To != "super@x.cl" || m.sent[0].Template != "access_review_global" || m.sent[0].URL != "https://wiki.x.cl/admin/access-review" {
		t.Errorf("superadmin mail: %+v", m.sent)
	}
	if st, _, _ := s.Area(ctx, sc); !st.Overdue || st.Last != nil {
		t.Errorf("area status: %+v", st)
	}

	if err := s.ConfirmArea(ctx, sc, users["admin"]); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := s.Area(ctx, sc); st.Overdue || st.Last == nil || st.Last.ReviewerName != "admin" || !st.DueAt.After(clock) {
		t.Errorf("after confirming: %+v", st)
	}
	if err := s.ConfirmGlobal(ctx, users["super"]); err != nil {
		t.Fatal(err)
	}
	if st, admins, areas, _ := s.Global(ctx); st.Overdue || len(admins) != 2 || len(areas) != 1 || areas[0].Overdue {
		t.Errorf("global after confirming: %+v %d %+v", st, len(admins), areas)
	}
	sent := len(m.sent)
	clock = clock.Add(24 * time.Hour)
	if err := s.Notices(ctx); err != nil || len(m.sent) != sent {
		t.Errorf("noticed again after confirming: %v %d", err, len(m.sent))
	}
}
