package presence

import (
	"errors"
	"slices"
	"testing"
)

func names(list []Editor) []string {
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = e.Name
	}
	return out
}

// Cada conexión ve quién edita su página, sin repetir a quien la tiene abierta en dos pestañas,
// y ve salir a los demás. Otra página no se entera.
func TestHub(t *testing.T) {
	h := NewHub()
	ana := Editor{ID: "1", Name: "Ana"}
	beto := Editor{ID: "2", Name: "Beto"}

	a, leaveA, _ := h.Join("p", ana)
	if got := names(<-a); !slices.Equal(got, []string{"Ana"}) {
		t.Fatalf("alone: %v", got)
	}
	b, leaveB, _ := h.Join("p", beto)
	_, leaveB2, _ := h.Join("p", beto) // segunda pestaña
	other, leaveO, _ := h.Join("q", Editor{ID: "3", Name: "Carla"})
	defer leaveO()
	if got := names(<-a); !slices.Equal(got, []string{"Ana", "Beto"}) {
		t.Errorf("Ana sees %v", got)
	}
	if got := names(<-b); !slices.Equal(got, []string{"Ana", "Beto"}) {
		t.Errorf("Beto sees %v", got)
	}
	if got := names(<-other); !slices.Equal(got, []string{"Carla"}) {
		t.Errorf("another page sees %v", got)
	}

	leaveB2()
	if got := names(<-a); !slices.Equal(got, []string{"Ana", "Beto"}) {
		t.Errorf("Beto still has a tab open, Ana sees %v", got)
	}
	leaveB()
	leaveB() // dos veces no descuenta dos
	if got := names(<-a); !slices.Equal(got, []string{"Ana"}) {
		t.Errorf("after Beto left, Ana sees %v", got)
	}
	leaveA()
	if len(h.pages) != 1 || h.users["2"] != 0 {
		t.Errorf("leftovers: pages %v, users %v", h.pages, h.users)
	}
}

func TestHubLimitAndClose(t *testing.T) {
	h := NewHub()
	e := Editor{ID: "1", Name: "Ana"}
	for range maxPerUser {
		if _, _, err := h.Join("p", e); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := h.Join("q", e); !errors.Is(err, ErrTooMany) {
		t.Errorf("join over the limit: %v", err)
	}
	h.Close()
	h.Close()
	select {
	case <-h.Done():
	default:
		t.Error("Done not closed")
	}
}
