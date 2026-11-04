#!/usr/bin/env bash
# Imprime la próxima versión (vX.Y.Z) para el commit dado, o nada si no corresponde una (§13.9).
# La deciden los prefijos de los commits desde la última etiqueta (Conventional Commits):
#   tipo!: … o "BREAKING CHANGE:" en el cuerpo  → mayor
#   feat: …                                     → menor
#   fix: … · perf: …                            → parche
#   docs, chore, ci, test, refactor, style…     → no hay versión
# Sin etiqueta previa la primera es v1.0.0 (decisión del dueño, 2026-11-03).
# Uso: next-version.sh <commit>
set -euo pipefail

sha=${1:?usage: next-version.sh <commit>}

# Las de prueba (v1.2.0-rc.1) no cuentan como base.
last=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-*' "$sha" 2>/dev/null || true)
if [[ -z $last ]]; then
  echo v1.0.0
  exit 0
fi

if [[ ! $last =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  echo "next-version: the last tag $last is not vX.Y.Z" >&2
  exit 1
fi
major=${BASH_REMATCH[1]} minor=${BASH_REMATCH[2]} patch=${BASH_REMATCH[3]}

# En variables: bash no acepta paréntesis ni ! sueltos dentro de [[ =~ ]].
breaking_re='^[a-z]+(\([^)]*\))?!:'
feat_re='^feat(\([^)]*\))?:'
fix_re='^(fix|perf)(\([^)]*\))?:'

bump=0 # 0 nada · 1 parche · 2 menor · 3 mayor
while IFS= read -r -d '' msg || [[ -n $msg ]]; do
  subject=${msg%%$'\n'*}
  if [[ $subject =~ $breaking_re ]] || grep -qE '^BREAKING[ -]CHANGE:' <<<"$msg"; then
    level=3
  elif [[ $subject =~ $feat_re ]]; then
    level=2
  elif [[ $subject =~ $fix_re ]]; then
    level=1
  else
    level=0
  fi
  if ((level > bump)); then bump=$level; fi
done < <(git log -z --format=%B "$last..$sha")

case $bump in
  3) echo "v$((major + 1)).0.0" ;;
  2) echo "v$major.$((minor + 1)).0" ;;
  1) echo "v$major.$minor.$((patch + 1))" ;;
  0) ;;
esac
