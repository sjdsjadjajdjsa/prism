FROM golang:1.27 AS build
WORKDIR /src
COPY cli/go.mod cli/go.sum ./
RUN go mod download
COPY cli/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /prism . \
 && for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
      CGO_ENABLED=0 GOOS=${target%/*} GOARCH=${target#*/} go build -trimpath -ldflags="-s -w" -o /hosts/prism-${target%/*}-${target#*/} . ; \
    done

# ffmpeg plays videos in the /tree viewer. The static build is one 76 MB file, where
# Debian's package pulls in over 600 MB. Pinned and checked against its SHA-256.
FROM debian:bookworm-slim AS ffmpeg
ARG FFMPEG_VERSION=6.0.1
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates xz-utils \
 && case "$(dpkg --print-architecture)" in \
      arm64) arch=arm64 sum=7dbd8e2f47bd83de591b9d6ea70e67d32d9aa97e7d47ae402b60c2fe3fd4d0ab ;; \
      *) arch=amd64 sum=28268bf402f1083833ea269331587f60a242848880073be8016501d864bd07a5 ;; \
    esac \
 && curl -fsSL -o /tmp/ff.txz "https://johnvansickle.com/ffmpeg/old-releases/ffmpeg-${FFMPEG_VERSION}-${arch}-static.tar.xz" \
 && echo "${sum}  /tmp/ff.txz" | sha256sum -c - \
 && tar xJf /tmp/ff.txz -C /tmp --strip-components=1 --wildcards '*/ffmpeg' \
 && install -m 755 /tmp/ffmpeg /usr/local/bin/ffmpeg

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
COPY --from=ffmpeg /usr/local/bin/ffmpeg /usr/local/bin/ffmpeg
COPY --from=build /hosts /opt/prism/hosts
COPY web /usr/share/prism/web
ENV HOME=/home/prism
WORKDIR /workspace
ENTRYPOINT ["prism"]
