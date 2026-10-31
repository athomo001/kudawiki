package wiki

import (
	"errors"
	"testing"
)

func TestMerge3(t *testing.T) {
	base := "# VPN\n\nPaso uno.\nPaso dos.\nPaso tres.\n\n## Si falla\n\nLlama a soporte.\n"
	cases := []struct {
		name, ours, theirs, want string
		conflict                 bool
	}{
		{name: "nadie cambió", ours: base, theirs: base, want: base},
		{name: "solo la página", ours: base + "Fin.\n", theirs: base, want: base + "Fin.\n"},
		{name: "solo la propuesta", ours: base, theirs: "# VPN corporativa\n" + base[6:], want: "# VPN corporativa\n" + base[6:]},
		{
			name:   "lejos: se juntan",
			ours:   "# VPN corporativa\n\nPaso uno.\nPaso dos.\nPaso tres.\n\n## Si falla\n\nLlama a soporte.\n",
			theirs: "# VPN\n\nPaso uno.\nPaso dos.\nPaso tres.\n\n## Si falla\n\nLlama a soporte (anexo 200).\n",
			want:   "# VPN corporativa\n\nPaso uno.\nPaso dos.\nPaso tres.\n\n## Si falla\n\nLlama a soporte (anexo 200).\n",
		},
		{
			name:   "los dos agregan lo mismo",
			ours:   base + "Fin.\n",
			theirs: base + "Fin.\n",
			want:   base + "Fin.\n",
		},
		{
			name:     "la misma línea",
			ours:     "# VPN\n\nPaso uno.\nPaso 2.\nPaso tres.\n\n## Si falla\n\nLlama a soporte.\n",
			theirs:   "# VPN\n\nPaso uno.\nSegundo paso.\nPaso tres.\n\n## Si falla\n\nLlama a soporte.\n",
			conflict: true,
		},
		{
			name:     "líneas contiguas: conflicto, como git",
			ours:     "# VPN\n\nPaso 1.\nPaso dos.\nPaso tres.\n\n## Si falla\n\nLlama a soporte.\n",
			theirs:   "# VPN\n\nPaso uno.\nPaso 2.\nPaso tres.\n\n## Si falla\n\nLlama a soporte.\n",
			conflict: true,
		},
		{
			name:     "los dos agregan al final cosas distintas",
			ours:     base + "Fin.\n",
			theirs:   base + "Anexo.\n",
			conflict: true,
		},
		{
			name:   "una borra lejos de lo que la otra cambia",
			ours:   "# VPN\n\nPaso uno.\nPaso dos.\nPaso tres.\n\n## Si falla\n\n",
			theirs: "# VPN\n\nPaso uno.\nPaso dos y medio.\nPaso tres.\n\n## Si falla\n\nLlama a soporte.\n",
			// "Paso dos" y la línea final no se tocan.
			want: "# VPN\n\nPaso uno.\nPaso dos y medio.\nPaso tres.\n\n## Si falla\n\n",
		},
		{name: "sin salto final", ours: "x\nb\nc", theirs: "a\nb\nc\nd", want: "x\nb\nc\nd"},
	}
	for _, c := range cases {
		b := base
		if c.name == "sin salto final" {
			b = "a\nb\nc"
		}
		got, err := Merge3(b, c.ours, c.theirs)
		switch {
		case c.conflict && !errors.Is(err, ErrMergeConflict):
			t.Errorf("%s: %q, %v; want a conflict", c.name, got, err)
		case !c.conflict && (err != nil || got != c.want):
			t.Errorf("%s:\n got %q, %v\nwant %q", c.name, got, err, c.want)
		}
	}
}
