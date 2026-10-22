#!/bin/sh
# Stub headless chrome for tests: writes a minimal PDF to stdout
# (--print-to-pdf=-) or to the file given as --print-to-pdf=<path>,
# ignoring everything else about the invocation.
# When the URL carries the query marker emit_big=1 it emits >25MB so the
# tests can exercise the archive output cap.
# When STUB_ARGS_FILE is set, the received arguments are appended there
# (one per line) so tests can assert the exact chromium flag contract.
out=""
prev=""
for arg in "$@"; do
  case "$prev" in
    --print-to-pdf) out="$arg" ;;
  esac
  prev="$arg"
done
if [ -n "$STUB_ARGS_FILE" ]; then
  for a in "$@"; do
    printf '%s\n' "$a" >> "$STUB_ARGS_FILE"
  done
fi
case "$*" in
  *emit_big=1*)
    if [ -n "$out" ] && [ "$out" != "-" ]; then
      { printf '%%PDF-1.4\n'; dd if=/dev/zero bs=1048576 count=26 2>/dev/null; } > "$out"
    else
      printf '%%PDF-1.4\n'
      dd if=/dev/zero bs=1048576 count=26 2>/dev/null
    fi
    exit 0
    ;;
esac
pdf='%PDF-1.4 stub-chrome-output'
if [ -n "$out" ] && [ "$out" != "-" ]; then
  printf '%s' "$pdf" > "$out"
else
  printf '%s' "$pdf"
fi
exit 0
