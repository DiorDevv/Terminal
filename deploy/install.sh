#!/usr/bin/env bash
#
# Installs the Squid Admin panel as an UNPRIVILEGED systemd service.
#
# The panel runs as its own user (default: squidadmin), never as root. It gets
# exactly what it needs and nothing more:
#   * write access to a few squid files (squid.conf, the block list, the passwd file)
#   * read access to squid's access log (via squid's group)
#   * a sudoers rule for the handful of fixed `systemctl ... squid` commands
#
# Usage:
#   sudo ./deploy/install.sh [options]
#   sudo ./deploy/install.sh --uninstall
#
# Options:
#   --bin PATH        prebuilt backend binary (default: build it with `go build`)
#   --prefix DIR      install directory                       (default /opt/squidadmin)
#   --user NAME       service account                         (default squidadmin)
#   --port N          port the panel listens on               (default 8080)
#   --origin URL      browser origin(s) allowed to call the API, comma separated
#                                                              (default http://localhost:5173)
#   --service NAME    systemd unit name of squid              (default squid)
#   --unit NAME       name for the panel's own unit           (default squidadmin)
#   --uninstall       remove the service, sudoers rule and account (keeps data + squid files)
#
set -euo pipefail

PREFIX=/opt/squidadmin
SVC_USER=squidadmin
PORT=8080
ORIGIN=http://localhost:5173
SQUID_UNIT=squid
UNIT=squidadmin
BIN=""
UNINSTALL=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bin)       BIN="$2"; shift 2 ;;
    --prefix)    PREFIX="$2"; shift 2 ;;
    --user)      SVC_USER="$2"; shift 2 ;;
    --port)      PORT="$2"; shift 2 ;;
    --origin)    ORIGIN="$2"; shift 2 ;;
    --service)   SQUID_UNIT="$2"; shift 2 ;;
    --unit)      UNIT="$2"; shift 2 ;;
    --uninstall) UNINSTALL=1; shift ;;
    -h|--help)   sed -n '2,25p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarn:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "run as root (sudo)"

UNIT_FILE=/etc/systemd/system/${UNIT}.service
SUDOERS_FILE=/etc/sudoers.d/${UNIT}
ENV_DIR=/etc/${UNIT}
DATA_DIR=/var/lib/${UNIT}

if [[ $UNINSTALL -eq 1 ]]; then
  log "removing ${UNIT}"
  systemctl disable --now "${UNIT}" 2>/dev/null || true
  rm -f "$UNIT_FILE" "$SUDOERS_FILE"
  systemctl daemon-reload
  if id "$SVC_USER" &>/dev/null; then userdel "$SVC_USER" 2>/dev/null || warn "could not remove user $SVC_USER"; fi
  echo "Kept (remove by hand if unwanted): $PREFIX  $ENV_DIR  $DATA_DIR  and the squid files."
  exit 0
fi

# ---------------------------------------------------------------- requirements
command -v systemctl >/dev/null || die "systemd is required (systemctl not found)"
SYSTEMCTL=$(readlink -f "$(command -v systemctl)")
SQUID=$(command -v squid || true);            [[ -n $SQUID ]]    || die "squid is not installed"
SQUID=$(readlink -f "$SQUID")
command -v htpasswd >/dev/null || die "htpasswd not found (install apache2-utils / httpd-tools)"
command -v sudo     >/dev/null || die "sudo is required"
command -v visudo   >/dev/null || die "visudo is required"

SQUID_CONF=${SQUID_CONF:-/etc/squid/squid.conf}
SQUID_DIR=$(dirname "$SQUID_CONF")
BLACKLIST=${BLACKLIST:-$SQUID_DIR/blocked_sites.txt}
PASSWD=${PASSWD:-$SQUID_DIR/passwd}
ACCESS_LOG=${ACCESS_LOG:-/var/log/squid/access.log}
[[ -f $SQUID_CONF ]] || die "$SQUID_CONF not found"

# squid's own group: whoever owns its log directory (proxy on Debian, squid on RHEL).
SQUID_GROUP=$(stat -c %G "$(dirname "$ACCESS_LOG")" 2>/dev/null || echo proxy)
getent group "$SQUID_GROUP" >/dev/null || die "group '$SQUID_GROUP' (squid's group) does not exist"

# ---------------------------------------------------------------------- binary
mkdir -p "$PREFIX/bin"
if [[ -z $BIN ]]; then
  ROOT=$(cd "$(dirname "$0")/.." && pwd)
  command -v go >/dev/null || die "no --bin given and Go is not installed to build one"
  log "building backend"
  (cd "$ROOT/backend" && go build -buildvcs=false -o "$PREFIX/bin/squidadmin-backend" ./cmd/server)
else
  install -m 0755 "$BIN" "$PREFIX/bin/squidadmin-backend"
fi
chown -R root:root "$PREFIX"; chmod 0755 "$PREFIX" "$PREFIX/bin"

# ---------------------------------------------------------------------- account
if ! id "$SVC_USER" &>/dev/null; then
  log "creating system user $SVC_USER"
  useradd --system --home-dir "$DATA_DIR" --shell /usr/sbin/nologin "$SVC_USER"
fi
# Read squid's access.log (0640 squid:squid) for the live log page.
usermod -aG "$SQUID_GROUP" "$SVC_USER"

install -d -m 0750 -o "$SVC_USER" -g "$SVC_USER" "$DATA_DIR"

# ------------------------------------------------------------ squid file access
log "granting $SVC_USER write access to the squid files it manages"
[[ -e $SQUID_CONF.orig ]] || cp -p "$SQUID_CONF" "$SQUID_CONF.orig"
chgrp "$SVC_USER" "$SQUID_CONF"; chmod 0664 "$SQUID_CONF"

# List files squid itself must be able to read: owner is the panel, group is squid's.
for f in "$BLACKLIST" "$PASSWD"; do
  [[ -e $f ]] || : > "$f"
  chown "$SVC_USER:$SQUID_GROUP" "$f"
done
chmod 0664 "$BLACKLIST"
chmod 0640 "$PASSWD"   # password hashes: not world-readable

# Downloaded block lists: the panel writes them, squid (any user) reads them.
BLOCKLIST_DIR=${BLOCKLIST_DIR:-$SQUID_DIR/blocklists}
install -d -m 0755 -o "$SVC_USER" -g "$SQUID_GROUP" "$BLOCKLIST_DIR"

# --------------------------------------------------------------------- sudoers
# Fixed command lines only — no wildcards, so the panel can't be turned into a
# general root shell. `stop`/`restart` use --no-block because squid waits for
# open connections on shutdown.
log "installing sudoers rule"
TMP=$(mktemp)
cat > "$TMP" <<EOF
# Managed by squidadmin deploy/install.sh
Defaults:${SVC_USER} !requiretty
${SVC_USER} ALL=(root) NOPASSWD: \\
  ${SYSTEMCTL} start ${SQUID_UNIT}, \\
  ${SYSTEMCTL} reload ${SQUID_UNIT}, \\
  ${SYSTEMCTL} stop --no-block ${SQUID_UNIT}, \\
  ${SYSTEMCTL} restart --no-block ${SQUID_UNIT}, \\
  ${SYSTEMCTL} enable ${SQUID_UNIT}, \\
  ${SYSTEMCTL} disable ${SQUID_UNIT}, \\
  ${SQUID} -k rotate
EOF
visudo -cf "$TMP" >/dev/null || { rm -f "$TMP"; die "generated sudoers file is invalid"; }
install -m 0440 -o root -g root "$TMP" "$SUDOERS_FILE"; rm -f "$TMP"

# ------------------------------------------------------------------ environment
install -d -m 0750 -o root -g "$SVC_USER" "$ENV_DIR"
if [[ ! -f $ENV_DIR/env ]]; then
  log "writing $ENV_DIR/env"
  cat > "$ENV_DIR/env" <<EOF
PORT=${PORT}
DB_PATH=${DATA_DIR}/squidadmin.db
FRONTEND_ORIGIN=${ORIGIN}

SQUID_CONF_PATH=${SQUID_CONF}
SQUID_ACCESS_LOG=${ACCESS_LOG}
SQUID_BIN=${SQUID}
SQUID_BLACKLIST_PATH=${BLACKLIST}
SQUID_BLOCKLIST_DIR=${BLOCKLIST_DIR}
SQUID_PASSWD_PATH=${PASSWD}
SQUID_SERVICE=${SQUID_UNIT}
SYSTEMCTL_BIN=${SYSTEMCTL}

# The panel runs unprivileged and reaches squid through the sudoers rule above.
SQUID_USE_SUDO=true
SQUID_MANAGER=systemd

# Set to true when the panel is served over HTTPS.
COOKIE_SECURE=false

# First-start admin: leave ADMIN_PASSWORD unset and a random one is printed once
# in the service log (journalctl -u ${UNIT}).
ADMIN_USERNAME=admin
EOF
  chown root:"$SVC_USER" "$ENV_DIR/env"; chmod 0640 "$ENV_DIR/env"
else
  warn "$ENV_DIR/env already exists — left untouched"
fi

# ------------------------------------------------------------------------ unit
log "installing systemd unit"
cat > "$UNIT_FILE" <<EOF
[Unit]
Description=Squid Admin Panel
After=network.target ${SQUID_UNIT}.service
Wants=${SQUID_UNIT}.service

[Service]
Type=simple
User=${SVC_USER}
Group=${SVC_USER}
SupplementaryGroups=${SQUID_GROUP}
EnvironmentFile=${ENV_DIR}/env
WorkingDirectory=${DATA_DIR}
ExecStart=${PREFIX}/bin/squidadmin-backend
Restart=on-failure
RestartSec=2

# Hardening. NoNewPrivileges must stay OFF: the panel calls sudo for the few
# squid commands allowed above, and sudo is setuid.
PrivateTmp=yes
ProtectHome=yes
ProtectSystem=full
ReadWritePaths=${SQUID_DIR} ${DATA_DIR}
ProtectKernelTunables=yes
ProtectControlGroups=yes
RestrictNamespaces=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "${UNIT}" >/dev/null
systemctl restart "${UNIT}"
sleep 2

if systemctl is-active --quiet "${UNIT}"; then
  log "${UNIT} is running on port ${PORT} as user ${SVC_USER}"
  echo
  echo "First-start admin password (shown once):"
  journalctl -u "${UNIT}" --no-pager -n 40 2>/dev/null | grep -E "username:|password:" || echo "  (already created earlier — see journalctl -u ${UNIT})"
else
  journalctl -u "${UNIT}" --no-pager -n 30 >&2 || true
  die "${UNIT} failed to start"
fi
