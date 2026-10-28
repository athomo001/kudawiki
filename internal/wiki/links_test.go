package wiki

import (
	"reflect"
	"strings"
	"testing"
)

func TestLinks(t *testing.T) {
	got := Links("Ver [[redes/vpn]] y [[redes/vpn|otra vez]], [[Ctrl+K]], [[toc]].\n\n```\n[[en/codigo]]\n```\n\n- [[a/b]]")
	if want := []string{"redes/vpn", "a/b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Links = %v, want %v", got, want)
	}
}

func TestRewriteMoves(t *testing.T) {
	moves := map[string]string{"redes/vpn": "infra/vpn", "redes/vpn/cliente": "infra/vpn/cliente"}
	// redes/vpn/oculta no se movió (quien movió no la ve): su enlace queda igual.
	got := rewriteMoves("[[redes/vpn]] [[redes/vpn|la VPN]] [[redes/vpn/cliente]] [[redes/vpn/oculta]] [[redes/vpn-2]] [[redes]]", moves)
	if want := "[[infra/vpn]] [[infra/vpn|la VPN]] [[infra/vpn/cliente]] [[redes/vpn/oculta]] [[redes/vpn-2]] [[redes]]"; got != want {
		t.Errorf("rewrite =\n %s\nwant\n %s", got, want)
	}
}

func TestRenderBrokenLinks(t *testing.T) {
	out, _ := render(t, "[[existe]] [[no-existe]]", Options{Base: "/w/ti", Exists: func(s string) bool { return s == "existe" }})
	if !strings.Contains(out, `class="wikilink" href="/w/ti/p/existe"`) || !strings.Contains(out, `class="wikilink broken" href="/w/ti/p/no-existe"`) {
		t.Errorf("broken links: %s", out)
	}
}
