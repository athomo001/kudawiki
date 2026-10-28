package media

import "testing"

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		`informe final.pdf`:   "informe final.pdf",
		`../../etc/passwd`:    "passwd",
		`C:\Users\x\foto.png`: "foto.png",
		"a\"b\x00c\n.txt":     "abc.txt",
		"":                    "archivo",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}
