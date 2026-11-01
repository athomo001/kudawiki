-- F6.1b: reautenticación (SPEC §8.2). Hasta cuándo esta sesión puede hacer lo delicado sin volver
-- a pedir contraseña y código. Es de la sesión y no de la persona: otra sesión abierta, quizá
-- robada en otro navegador, no hereda la confirmación de esta.
ALTER TABLE sessions ADD COLUMN reauth_until DATETIME;
