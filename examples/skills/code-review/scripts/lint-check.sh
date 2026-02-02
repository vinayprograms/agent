#!/bin/bash
# Run language-specific linters on the given file
FILE="$1"
EXT="${FILE##*.}"

case "$EXT" in
  go)
    go vet "$FILE" 2>&1
    ;;
  py)
    python3 -m py_compile "$FILE" 2>&1
    ;;
  js|ts)
    npx eslint "$FILE" 2>&1 || true
    ;;
  *)
    echo "No linter configured for .$EXT files"
    ;;
esac
