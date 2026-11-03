package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

// TestGF256 usa los ejemplos de FIPS-197 (§4.2 y la S-box): si el campo está mal, el reparto
// parece funcionar con 2 partes y falla con otras.
func TestGF256(t *testing.T) {
	for _, c := range []struct{ a, b, want byte }{
		{0x57, 0x83, 0xc1}, {0x57, 0x13, 0xfe}, {0x57, 0x02, 0xae}, {0x57, 0x04, 0x47}, {0x57, 0x08, 0x8e}, {0x57, 0x10, 0x07},
		{0x00, 0x83, 0x00}, {0x01, 0x83, 0x83},
	} {
		if got := gfMul(c.a, c.b); got != c.want {
			t.Errorf("{%02x}•{%02x} = {%02x}, want {%02x}", c.a, c.b, got, c.want)
		}
	}
	if got := gfInv(0x53); got != 0xca {
		t.Errorf("{53}^-1 = {%02x}, want {ca}", got)
	}
	for a := 1; a < 256; a++ {
		if gfMul(byte(a), gfInv(byte(a))) != 1 {
			t.Fatalf("{%02x} has no inverse", a)
		}
	}
}

// TestShamirVector es un reparto calculado a mano: f(x) = 0x42 + 0x57·x.
func TestShamirVector(t *testing.T) {
	shares := split([]byte{0x42}, 3, 2, []byte{0x57})
	want := [][]byte{{1, 0x42 ^ 0x57}, {2, 0x42 ^ 0xae}, {3, 0x42 ^ 0x57 ^ 0xae}}
	for i := range want {
		if !bytes.Equal(shares[i], want[i]) {
			t.Errorf("share %d = %x, want %x", i+1, shares[i], want[i])
		}
	}
	for _, pair := range [][2]int{{0, 1}, {0, 2}, {1, 2}} {
		if got, _ := Combine([][]byte{shares[pair[0]], shares[pair[1]]}); !bytes.Equal(got, []byte{0x42}) {
			t.Errorf("combine %v = %x", pair, got)
		}
	}
}

// TestShamir: cualquier grupo de threshold partes reconstruye la llave; con una menos sale
// otra cosa.
func TestShamir(t *testing.T) {
	key := make([]byte, MasterKeySize)
	rand.Read(key)
	for _, c := range []struct{ n, k int }{{2, 2}, {3, 2}, {5, 3}, {7, 7}} {
		shares, err := Split(key, c.n, c.k)
		if err != nil || len(shares) != c.n {
			t.Fatalf("split %d of %d: %v", c.k, c.n, err)
		}
		for _, group := range subsets(c.n, c.k) {
			pick := make([][]byte, len(group))
			for i, g := range group {
				pick[i] = shares[g]
			}
			if got, err := Combine(pick); err != nil || !bytes.Equal(got, key) {
				t.Fatalf("%d of %d, shares %v: %x %v", c.k, c.n, group, got, err)
			}
			if got, _ := Combine(pick[1:]); len(pick) > 2 && bytes.Equal(got, key) {
				t.Fatalf("%d of %d: %d shares were enough", c.k, c.n, len(pick)-1)
			}
		}
	}
}

func TestShamirRejects(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	for _, c := range []struct{ n, k int }{{3, 1}, {2, 3}, {256, 2}} {
		if _, err := Split(key, c.n, c.k); err != ErrShamirParams {
			t.Errorf("split %d of %d: %v", c.k, c.n, err)
		}
	}
	shares, _ := Split(key, 3, 2)
	for name, bad := range map[string][][]byte{
		"one":       shares[:1],
		"repeated":  {shares[0], shares[0]},
		"x zero":    {append([]byte{0}, shares[0][1:]...), shares[1]},
		"lengths":   {shares[0], shares[1][:10]},
		"too short": {{1}, {2}},
	} {
		if _, err := Combine(bad); err != ErrShamirShares {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// subsets son los grupos de k índices entre 0 y n-1.
func subsets(n, k int) [][]int {
	var out [][]int
	var rec func(start int, cur []int)
	rec = func(start int, cur []int) {
		if len(cur) == k {
			out = append(out, append([]int(nil), cur...))
			return
		}
		for i := start; i < n; i++ {
			rec(i+1, append(cur, i))
		}
	}
	rec(0, nil)
	return out
}
