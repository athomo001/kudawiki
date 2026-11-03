-- F7.3: restablecer la contraseña de un sellado (SPEC §6.3).

-- Una fila por documento: el pedido del autor y la aprobación del superadmin, que vale 24 horas.
-- No se guarda ningún token: el autor lo completa con su sesión reautenticada, que ya prueba que
-- es él, y la aprobación queda atada al documento. Se borra al completarse o con el sellado.
CREATE TABLE sealed_password_resets (
    page_id TEXT PRIMARY KEY REFERENCES sealed_pages(id) ON DELETE CASCADE,
    requested_by TEXT REFERENCES users(id),
    requested_at DATETIME,
    approved_by TEXT REFERENCES users(id),
    approved_at DATETIME,
    expires_at DATETIME,
    CHECK ((approved_by IS NULL) = (expires_at IS NULL))
);
