#!/usr/bin/env bash
# Prism installer: curl -fsSL https://raw.githubusercontent.com/sjdsjadjajdjsa/prism/main/install.sh | bash
set -euo pipefail

REPO="https://github.com/sjdsjadjajdjsa/prism.git"
DIR="${PRISM_HOME:-$HOME/.prism}"

say() { printf '\033[1;35m◆\033[0m %s\n' "$*"; }

if ! command -v docker >/dev/null 2>&1; then
  say "Prism needs Docker (free). Install it, then run this again:"
  case "$(uname -s)" in
    Darwin) echo "  Docker Desktop: https://www.docker.com/products/docker-desktop/" ;;
    Linux)
      echo "  Ubuntu, Mint, Debian:"
      echo "    sudo apt install docker.io docker-compose-v2"
      echo "    sudo usermod -aG docker \$USER    (then log out and back in)"
      ;;
    *) echo "  https://docs.docker.com/get-docker/" ;;
  esac
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  say "Docker is installed but not running, or you don't have permission to use it."
  echo "  Mac/Windows: open Docker Desktop.  Linux: sudo usermod -aG docker \$USER, then log in again."
  exit 1
fi
docker compose version >/dev/null 2>&1 || { say "Docker Compose is missing. Linux: sudo apt install docker-compose-v2"; exit 1; }
command -v git >/dev/null 2>&1 || { say "Prism needs git. Linux: sudo apt install git   Mac: xcode-select --install"; exit 1; }

if [[ -d "$DIR/.git" ]]; then
  say "Updating Prism in $DIR"
  git -C "$DIR" pull --ff-only -q
else
  say "Downloading Prism to $DIR"
  git clone -q --depth 1 "$REPO" "$DIR"
fi
"$DIR/prism" install
say "Done. Open a project folder in a new terminal and type: prism"
say "The first start takes a few minutes while Docker builds Prism."
