//go:build race

package wiki

// raceDetector: el binario de prueba corre con -race, mucho más lento (ver TestPathologicalMarkdown).
const raceDetector = true
