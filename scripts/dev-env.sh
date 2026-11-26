#!/usr/bin/env bash
# Creates .env for local development from .env.example: a fresh encryption
# key, KIT_ENV=dev, and ports that aren't already taken on this machine (so
# a second checkout, or another project's Postgres, doesn't collide).
# Does nothing if .env already exists.
set -euo pipefail

if [ -f .env ]; then
	exit 0
fi

# in_use reports whether something is listening on localhost:$1.
in_use() {
	(exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}

# free_port prints the first port at or after $1 that nothing listens on,
# stepping by 10 so a set of ports moves together (5489 -> 5499 ...).
free_port() {
	local p=$1
	while in_use "$p"; do
		p=$((p + 10))
	done
	echo "$p"
}

pg=$(free_port 5489)
redis=$(free_port 6389)
app=$(free_port 8488)
key=$(openssl rand -hex 32)

sed \
	-e "s|^PG_PORT=.*|PG_PORT=$pg|" \
	-e "s|^REDIS_PORT=.*|REDIS_PORT=$redis|" \
	-e "s|^PORT=.*|PORT=$app|" \
	-e "s|^DATABASE_URL=.*|DATABASE_URL=postgres://kit:kit@localhost:$pg/kit?sslmode=disable|" \
	-e "s|^REDIS_URL=.*|REDIS_URL=redis://localhost:$redis|" \
	-e "s|^ENCRYPTION_KEY=.*|ENCRYPTION_KEY=$key|" \
	.env.example >.env
echo "KIT_ENV=dev" >>.env

echo "Created .env (Postgres on $pg, Kit on $app)"
