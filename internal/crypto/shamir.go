package crypto

import (
	"crypto/rand"
	"errors"
)

// Custodia repartida de la MASTER_KEY (SPEC §6.5): reparto de Shamir sobre GF(2^8), byte a byte.
// Cada parte es x (1..255) seguido de un byte por cada byte de la llave. Con threshold partes
// cualquiera se reconstruye; con menos no se sabe nada de ella. Implementación propia, sin
// dependencias: son pocas líneas y se prueban con los vectores de FIPS-197 para el campo.

var (
	ErrShamirParams = errors.New("shamir: need 2 <= threshold <= shares <= 255 and a non-empty secret")
	ErrShamirShares = errors.New("shamir: need at least 2 shares of the same length with different, non-zero x")
)

// Split parte secret en shares partes; threshold de ellas lo reconstruyen.
func Split(secret []byte, shares, threshold int) ([][]byte, error) {
	if len(secret) == 0 || threshold < 2 || threshold > shares || shares > 255 {
		return nil, ErrShamirParams
	}
	// Un polinomio de grado threshold-1 por byte: el término libre es el byte de la llave y los
	// demás coeficientes son aleatorios.
	coeffs := make([]byte, len(secret)*(threshold-1))
	if _, err := rand.Read(coeffs); err != nil {
		return nil, err
	}
	return split(secret, shares, threshold, coeffs), nil
}

// split evalúa los polinomios en x = 1..shares. coeffs lleva threshold-1 coeficientes por byte.
func split(secret []byte, shares, threshold int, coeffs []byte) [][]byte {
	out := make([][]byte, shares)
	for i := range out {
		x := byte(i + 1)
		share := make([]byte, 1+len(secret))
		share[0] = x
		for j, s := range secret {
			c := coeffs[j*(threshold-1) : (j+1)*(threshold-1)]
			// Horner, del coeficiente más alto al término libre.
			var y byte
			for k := len(c) - 1; k >= 0; k-- {
				y = gfMul(y, x) ^ c[k]
			}
			share[1+j] = gfMul(y, x) ^ s
		}
		out[i] = share
	}
	return out
}

// Combine reconstruye la llave con interpolación de Lagrange en x = 0. Con menos partes que el
// umbral devuelve otra cosa sin poder darse cuenta: quien llama la compara con KeyCheck.
func Combine(shares [][]byte) ([]byte, error) {
	if len(shares) < 2 || len(shares[0]) < 2 {
		return nil, ErrShamirShares
	}
	seen := map[byte]bool{}
	for _, s := range shares {
		if len(s) != len(shares[0]) || s[0] == 0 || seen[s[0]] {
			return nil, ErrShamirShares
		}
		seen[s[0]] = true
	}
	secret := make([]byte, len(shares[0])-1)
	for i, si := range shares {
		// l_i(0) = Π x_m / (x_m - x_i); en GF(2^8) restar es XOR.
		basis := byte(1)
		for m, sm := range shares {
			if m != i {
				basis = gfMul(basis, gfMul(sm[0], gfInv(sm[0]^si[0])))
			}
		}
		for j := range secret {
			secret[j] ^= gfMul(si[1+j], basis)
		}
	}
	return secret, nil
}

// gfMul multiplica en GF(2^8) con el polinomio de AES (x^8 + x^4 + x^3 + x + 1). Sin tablas ni
// ramas que dependan de los datos: los accesos a memoria no delatan la llave.
func gfMul(a, b byte) byte {
	var p byte
	for range 8 {
		p ^= a & -(b & 1)
		a = a<<1 ^ 0x1b&-(a>>7)
		b >>= 1
	}
	return p
}

// gfInv es el inverso multiplicativo, a^254 (a^255 = 1 para a distinto de 0).
func gfInv(a byte) byte {
	r := byte(1)
	for e := 254; e > 0; e >>= 1 {
		if e&1 == 1 {
			r = gfMul(r, a)
		}
		a = gfMul(a, a)
	}
	return r
}
