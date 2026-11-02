// Cuántos ajustes cambiaron entre lo guardado y el formulario, para la barra de guardado
// (SPEC §15): cada clave de primer nivel cuenta una vez, también si es una lista o un grupo.
export function countChanges(saved: Record<string, unknown>, now: Record<string, unknown>): number {
  return Object.keys(now).filter((k) => JSON.stringify(now[k]) !== JSON.stringify(saved[k])).length;
}
