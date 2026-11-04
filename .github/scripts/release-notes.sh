#!/usr/bin/env bash
# Notas de un release (SPEC §13.9): los commits desde la versión anterior, si trae migraciones de
# base, si se puede saltar versiones y la imagen publicada. Lo que git no sabe (avisos de
# seguridad, si hay que reindexar) lo dice el CHANGELOG, y se puede agregar editando el release.
# Uso: release-notes.sh <etiqueta>   (con GITHUB_REPOSITORY y, si existe, dist/IMAGE)
set -euo pipefail

tag=${1:?usage: release-notes.sh <tag>}
repo=${GITHUB_REPOSITORY:-athomo001/kudawiki}
prev=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-*' "$tag^" 2>/dev/null || true)
major=${tag%%.*}
prev_major=${prev%%.*}

echo "## Cambios"
echo
if [[ -n $prev ]]; then
  git log --no-merges --format='- %s (%h)' "$prev..$tag"
else
  echo "Primera versión publicada."
fi
echo
echo "## Antes de actualizar"
echo
if [[ -z $prev ]]; then
  echo "- Instalación nueva: [deploy/README.md](https://github.com/$repo/blob/$tag/deploy/README.md)."
else
  added=$(git diff --name-only --diff-filter=A "$prev" "$tag" -- internal/database/migrations)
  if [[ -n $added ]]; then
    echo '- **Trae migraciones de base.** Al arrancar, la wiki respalda la base (`backups/kudawiki-pre-migrate-…`) y después las aplica. Para volver atrás hay que restaurar ese respaldo; `kudawiki migrate --dry-run` con el binario nuevo muestra antes qué cambia:'
    while IFS= read -r f; do echo "  - \`${f##*/}\`"; done <<<"$added"
  else
    echo "- Sin migraciones de base: para volver atrás basta con la imagen anterior."
  fi
  if [[ $major == "$prev_major" ]]; then
    echo "- Se llega directo desde cualquier ${major#v}.x anterior."
  else
    echo "- **Versión mayor**: actualizar primero a la última ${prev_major#v}.x y desde ahí a esta."
  fi
fi
echo "- Avisos de seguridad y si hay que reindexar la búsqueda: [CHANGELOG.md](https://github.com/$repo/blob/$tag/CHANGELOG.md)."
if [[ -f dist/IMAGE ]]; then
  echo
  echo "## Imagen"
  echo
  echo '```'
  cat dist/IMAGE
  echo '```'
  echo
  echo "Cómo verificar su firma y actualizar: [deploy/README.md](https://github.com/$repo/blob/$tag/deploy/README.md#3-bajar-la-imagen-y-verificar-su-firma)."
fi
