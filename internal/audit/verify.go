package audit

import (
	"context"
	"fmt"

	"github.com/athomo001/kudawiki/internal/repo"
)

type chain interface {
	AuditChain(ctx context.Context, fn func(repo.AuditRow) error) error
}

// Report es lo que revisó Verify.
type Report struct {
	Events          int
	FirstID, LastID int64
}

// BrokenError dice en qué evento se rompe la cadena.
type BrokenError struct {
	ID     int64
	Reason string
}

func (e BrokenError) Error() string {
	return fmt.Sprintf("audit chain broken at event %d: %s", e.ID, e.Reason)
}

// Verify recorre la cadena entera. El primer evento que queda es el ancla: si la retención
// borró los anteriores (§11.2), su prev_hash ya no se puede comprobar; si es el evento 1, tiene
// que partir de Genesis. Lo que sí se detecta es cualquier fila cambiada, borrada en medio o
// intercalada. Borrar los últimos eventos no se ve en la cadena: para eso está el piso de 180
// días en la BD y, más adelante, la copia a syslog.
func Verify(ctx context.Context, c chain) (Report, error) {
	var rep Report
	var prev string
	err := c.AuditChain(ctx, func(row repo.AuditRow) error {
		switch {
		case rep.Events == 0 && row.ID == 1 && row.PrevHash != Genesis:
			return BrokenError{row.ID, "the first event does not start the chain"}
		case rep.Events > 0 && row.PrevHash != prev:
			return BrokenError{row.ID, "prev_hash does not match the previous event (a row was removed or inserted)"}
		case Seal(row.PrevHash, row) != row.Hash:
			return BrokenError{row.ID, "hash does not match its content (the row was modified)"}
		}
		if rep.Events == 0 {
			rep.FirstID = row.ID
		}
		rep.Events++
		rep.LastID, prev = row.ID, row.Hash
		return nil
	})
	return rep, err
}
