package wiki

import (
	"container/list"
	"sync"
)

// RenderCache guarda el HTML de las páginas ya renderizadas (SPEC §13.4, B11), LRU con un tope
// de bytes. La clave la arma quien llama con todo lo que cambia el resultado: la página y su
// revisión, la base de los enlaces y el árbol que ve quien lee (un [[enlace]] a una página que no
// ve sale roto, §17). Así dos personas con distinto nivel nunca comparten una entrada.
type RenderCache struct {
	mu    sync.Mutex
	max   int
	size  int
	order *list.List // del más reciente al más viejo
	items map[string]*list.Element
}

type cached struct {
	key  string
	html string
	toc  []Heading
}

func NewRenderCache(maxBytes int) *RenderCache {
	return &RenderCache{max: maxBytes, order: list.New(), items: map[string]*list.Element{}}
}

func (c *RenderCache) Get(key string) (string, []Heading, bool) {
	if c == nil {
		return "", nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return "", nil, false
	}
	c.order.MoveToFront(e)
	v := e.Value.(cached)
	return v.html, v.toc, true
}

func (c *RenderCache) Put(key, html string, toc []Heading) {
	if c == nil || len(html) > c.max/4 { // una página enorme no vacía la caché entera
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.order.MoveToFront(e)
		return
	}
	c.items[key] = c.order.PushFront(cached{key: key, html: html, toc: toc})
	c.size += len(key) + len(html)
	for c.size > c.max {
		last := c.order.Back()
		v := last.Value.(cached)
		c.order.Remove(last)
		delete(c.items, v.key)
		c.size -= len(v.key) + len(v.html)
	}
}
