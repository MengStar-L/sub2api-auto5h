#!/bin/sh
set -eu

repo="MengStar-L/sub2api-auto5h"
install_dir="/usr/local/bin"
config_dir="/etc/sub2api-auto5h"
state_dir="/var/lib/sub2api-auto5h"
service_name="sub2api-auto5h"

if [ "$(id -u)" -ne 0 ]; then
  echo "Run this installer as root." >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

version="${SUB2API_AUTO5H_VERSION:-latest}"
if [ "$version" = "latest" ]; then
  version=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
fi
if [ -z "$version" ]; then
  echo "Could not resolve a release version." >&2
  exit 1
fi

asset="sub2api-auto5h-linux-$arch.tar.gz"
base="https://github.com/$repo/releases/download/$version"
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT INT TERM
curl -fsSL "$base/$asset" -o "$tmp_dir/$asset"
curl -fsSL "$base/SHA256SUMS" -o "$tmp_dir/SHA256SUMS"
(cd "$tmp_dir" && grep "  $asset$" SHA256SUMS | sha256sum -c -)
tar -xzf "$tmp_dir/$asset" -C "$tmp_dir"

if ! getent group "$service_name" >/dev/null 2>&1; then groupadd --system "$service_name"; fi
if ! id "$service_name" >/dev/null 2>&1; then useradd --system --gid "$service_name" --home-dir "$state_dir" --shell /usr/sbin/nologin "$service_name"; fi
install -d -m 0750 -o root -g "$service_name" "$config_dir"
install -d -m 0700 -o "$service_name" -g "$service_name" "$state_dir"

if systemctl is-active --quiet "$service_name.service"; then
  systemctl stop "$service_name.service"
fi
if [ -f "$state_dir/app.db" ]; then
  backup="$state_dir/backup-before-$version-$(date -u +%Y%m%dT%H%M%SZ).tar.gz"
  tar -czf "$backup" -C "$state_dir" app.db app.db-wal app.db-shm 2>/dev/null || tar -czf "$backup" -C "$state_dir" app.db
  chown "$service_name:$service_name" "$backup"
  chmod 0600 "$backup"
fi

install -m 0755 "$tmp_dir/sub2api-auto5h" "$install_dir/sub2api-auto5h.new"
mv -f "$install_dir/sub2api-auto5h.new" "$install_dir/sub2api-auto5h"
install -m 0644 "$tmp_dir/sub2api-auto5h.service" "/etc/systemd/system/$service_name.service"

if [ ! -f "$config_dir/sub2api-auto5h.env" ]; then
  master_key=$($install_dir/sub2api-auto5h keygen | sed 's/^SUB2API_AUTO5H_MASTER_KEY=//')
  cat >"$config_dir/sub2api-auto5h.env" <<EOF
SUB2API_AUTO5H_MASTER_KEY=$master_key
SUB2API_AUTO5H_LISTEN=127.0.0.1:8090
SUB2API_AUTO5H_DB_PATH=$state_dir/app.db
SUB2API_AUTO5H_COOKIE_SECURE=false
EOF
  chown root:"$service_name" "$config_dir/sub2api-auto5h.env"
  chmod 0600 "$config_dir/sub2api-auto5h.env"
fi

systemctl daemon-reload
systemctl enable --now "$service_name.service"
echo "Installed sub2api-auto5h $version."
echo "Read the setup token with: journalctl -u $service_name -n 30 --no-pager"
