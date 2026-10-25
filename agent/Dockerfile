FROM golang:1.27 AS build
WORKDIR /src
COPY cli/go.mod cli/go.sum ./
RUN go mod download
COPY cli/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /prism . \
 && for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
      CGO_ENABLED=0 GOOS=${target%/*} GOARCH=${target#*/} go build -trimpath -ldflags="-s -w" -o /hosts/prism-${target%/*}-${target#*/} . ; \
    done

FROM node:24-bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ripgrep curl ca-certificates python3 python3-venv python-is-python3 build-essential \
        fonts-liberation libnss3 libatk1.0-0 libatk-bridge2.0-0 libatspi2.0-0 libdbus-1-3 libx11-6 libxcb1 libxcomposite1 libxdamage1 libxext6 libxfixes3 libxrandr2 libxkbcommon0 libgbm1 libasound2 libglib2.0-0 \
 && rm -rf /var/lib/apt/lists/*
# Neovim for the editor panel (ctrl+e). The release tarball is newer than Debian's package
# and is checked against the SHA-256 GitHub publishes for it.
ARG NVIM_VERSION=v0.12.6
RUN case "$(dpkg --print-architecture)" in \
      arm64) arch=arm64 sum=8f1f64a0bdb97247034038c3823c6cbad5bdf9ecd5751b85494b71c3ee04c815 ;; \
      *) arch=x86_64 sum=474430d53e6264f6d6dd18db42d6dc9df3a1b56ca9e88a325bbf860e1a811d87 ;; \
    esac \
 && curl -fsSL -o /tmp/nvim.tgz "https://github.com/neovim/neovim/releases/download/${NVIM_VERSION}/nvim-linux-${arch}.tar.gz" \
 && echo "${sum}  /tmp/nvim.tgz" | sha256sum -c - \
 && tar xzf /tmp/nvim.tgz -C /opt \
 && ln -s /opt/nvim-linux-${arch}/bin/nvim /usr/local/bin/nvim \
 && rm /tmp/nvim.tgz
# The screenshot browser (chrome-headless-shell, ~126 MB) is NOT baked in: prism downloads it once on first
# screenshot into the persistent config volume. Only the shared libraries and a font it needs are installed.
RUN mkdir -p /home/prism/.config/prism && chmod -R 777 /home/prism
COPY --from=build /prism /usr/local/bin/prism
COPY --from=build /hosts /opt/prism/hosts
COPY web /usr/share/prism/web
ENV HOME=/home/prism
WORKDIR /workspace
ENTRYPOINT ["prism"]
