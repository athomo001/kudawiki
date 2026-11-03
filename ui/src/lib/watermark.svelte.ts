// Marca de agua (SPEC §4.15). App deja aquí a quien lee al cargar la sesión: así cada pantalla
// que la muestra no tiene que recibir a la persona por props.
export const viewer = $state({ name: '', email: '' });

// Va siempre en N3 (y en los sellados, que la ponen sin preguntar); en N2, si el área la pidió.
export const watermarked = (level: number | undefined, area: { watermark_n2?: boolean }) =>
  level === 3 || (level === 2 && !!area.watermark_n2);
