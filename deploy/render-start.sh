#!/usr/bin/env sh
set -eu
: "${DATABASE_URL:?DATABASE_URL is required}"
# Render's internal URL has no sslmode by default. Require encrypted transport.
case "$DATABASE_URL" in
  *sslmode=*) ;;
  *\?*) DATABASE_URL="${DATABASE_URL}&sslmode=require" ;;
  *) DATABASE_URL="${DATABASE_URL}?sslmode=require" ;;
esac
export DATABASE_URL
exec ./bin/northstar
