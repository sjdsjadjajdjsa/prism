#!/usr/bin/env bash
# Prism installer: curl -fsSL https://raw.githubusercontent.com/sjdsjadjajdjsa/prism/main/install.sh | bash
set -euo pipefail

REPO="https://github.com/sjdsjadjajdjsa/prism.git"
DIR="${PRISM_HOME:-$HOME/.prism}"

GUIDE="https://github.com/sjdsjadjajdjsa/prism/blob/main/docs/SETUP.md"

say() { printf '\033[1;35m◆\033[0m %s\n' "$*"; }

if grep -qi microsoft /proc/version 2>/dev/null; then WSL=1; else WSL=0; fi

if ! command -v docker >/dev/null 2>&1; then
  if [ "$WSL" = 1 ]; then
    say "Install Docker Desktop on Windows and open it."
    echo "  Gear icon > Resources > WSL integration > turn on ${WSL_DISTRO_NAME:-this Linux} > Apply & restart."
    echo "  Step-by-step help: https://github.com/sjdsjadjajdjsa/prism/blob/main/docs/WINDOWS.md"
  else
    say "Prism needs Docker (free). Install it, then run this again:"
    case "$(uname -s)" in
      Darwin) echo "  Docker Desktop: https://www.docker.com/products/docker-desktop/" ;;
      Linux)
        echo "  Ubuntu, Mint, Debian:"
        echo "    sudo apt update"
        echo "    sudo apt install docker.io docker-compose-v2"
        echo "    sudo usermod -aG docker \$USER    (then log out and back in)"
        ;;
      *) echo "  https://docs.docker.com/get-docker/" ;;
    esac
    echo "  Step-by-step help: $GUIDE"
  fi
  exit 1
fi
if [ "$WSL" = 1 ] && [ ! -e /proc/sys/fs/binfmt_misc/WSLInterop ] && ! (cd /mnt/c 2>/dev/null && cmd.exe /c ver >/dev/null 2>&1); then
  say "This Linux can't start Windows programs, which Docker Desktop needs. Fix it once:"
  echo "  sudo mkdir -p /usr/lib/binfmt.d && echo ':WSLInterop:M::MZ::/init:PF' | sudo tee /usr/lib/binfmt.d/WSLInterop.conf"
  echo "  Then run wsl --shutdown in Windows PowerShell and reopen the terminal."
  exit 1
fi
if ! command -v curl >/dev/null 2>&1 || ! command -v git >/dev/null 2>&1; then
  say "Prism needs curl and git."
  if [ -r /etc/os-release ] && grep -Eqi '^(ID|ID_LIKE)=.*(debian|ubuntu)' /etc/os-release; then
    echo "  sudo apt update && sudo apt install -y curl git"
  elif [ "$(uname -s)" = Darwin ]; then
    echo "  xcode-select --install"
  else
    echo "  Install curl and git, then run this again."
  fi
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  say "Docker is installed but not running, or you don't have permission to use it."
  echo "  Mac/Windows: open Docker Desktop.  Linux: sudo usermod -aG docker \$USER, then log in again."
  echo "  Step-by-step help: $GUIDE"
  exit 1
fi
docker compose version >/dev/null 2>&1 || { say "Docker Compose is missing. Linux: sudo apt install docker-compose-v2"; exit 1; }

if [[ -d "$DIR/.git" ]]; then
  say "Updating Prism in $DIR"
  git -C "$DIR" pull --ff-only || {
    echo "Couldn't update Prism in $DIR (local changes, a different version, or no internet). To start fresh, move the folder away (mv \"$DIR\" \"$DIR-old\") and run this again." >&2
    exit 1
  }
else
  say "Downloading Prism to $DIR"
  git clone -q --depth 1 "$REPO" "$DIR"
fi
"$DIR/prism" install
say "Done. Open a project folder in a new terminal and type: prism"
say "The first start takes a few minutes while Docker builds Prism."
