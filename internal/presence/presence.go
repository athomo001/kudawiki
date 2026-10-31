// Package presence sabe quién tiene abierta cada página en el editor, para avisar "Ana también
// está editando esta página" (SPEC §4.11). Vive en memoria: Kudawiki es un solo proceso, y si se
// reinicia, los editores abiertos se vuelven a conectar solos. No resuelve conflictos: eso lo
// hace el bloqueo optimista al guardar (§4.1).
package presence

import (
	"cmp"
	"errors"
	"slices"
	"sync"
)

// maxPerUser acota las conexiones abiertas de una persona: cada una es una conexión viva en el
// servidor, y sin tope una sesión podría abrir miles.
const maxPerUser = 10

// ErrTooMany: la persona ya tiene maxPerUser editores conectados.
var ErrTooMany = errors.New("too_many_editors")

type Editor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type sub struct {
	editor Editor
	ch     chan []Editor
}

type Hub struct {
	mu     sync.Mutex
	pages  map[string]map[*sub]struct{}
	users  map[string]int
	done   chan struct{}
	closed bool
}

func NewHub() *Hub {
	return &Hub{pages: map[string]map[*sub]struct{}{}, users: map[string]int{}, done: make(chan struct{})}
}

// Join anota a e en la página. Por updates llega la lista de quienes la editan (e incluido)
// cada vez que cambia, empezando por la de ahora; leave lo saca. Una lista que no se alcanzó a
// leer se reemplaza por la nueva: importa quién está ahora, no el camino.
func (h *Hub) Join(pageID string, e Editor) (updates <-chan []Editor, leave func(), err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.users[e.ID] >= maxPerUser {
		return nil, nil, ErrTooMany
	}
	s := &sub{editor: e, ch: make(chan []Editor, 1)}
	if h.pages[pageID] == nil {
		h.pages[pageID] = map[*sub]struct{}{}
	}
	h.pages[pageID][s] = struct{}{}
	h.users[e.ID]++
	h.broadcast(pageID)
	var once sync.Once
	return s.ch, func() { once.Do(func() { h.leave(pageID, s) }) }, nil
}

func (h *Hub) leave(pageID string, s *sub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.pages[pageID], s)
	if len(h.pages[pageID]) == 0 {
		delete(h.pages, pageID)
	}
	if h.users[s.editor.ID]--; h.users[s.editor.ID] == 0 {
		delete(h.users, s.editor.ID)
	}
	h.broadcast(pageID)
}

// broadcast manda a cada conexión de la página la lista sin repetidos (la misma persona en dos
// pestañas es una), ordenada por nombre. Llamar con mu tomado.
func (h *Hub) broadcast(pageID string) {
	seen := map[string]bool{}
	var list []Editor
	for s := range h.pages[pageID] {
		if !seen[s.editor.ID] {
			seen[s.editor.ID] = true
			list = append(list, s.editor)
		}
	}
	slices.SortFunc(list, func(a, b Editor) int { return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID)) })
	for s := range h.pages[pageID] {
		select {
		case <-s.ch: // la anterior, sin leer
		default:
		}
		s.ch <- list
	}
}

// Done se cierra al apagar el servidor: http.Server.Shutdown espera a que terminen las
// peticiones, y una conexión de presencia no termina sola.
func (h *Hub) Done() <-chan struct{} { return h.done }

// Close corta todas las conexiones. Se registra con http.Server.RegisterOnShutdown.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		close(h.done)
	}
}
