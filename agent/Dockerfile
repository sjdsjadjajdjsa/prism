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
 && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /home/prism/.config/prism && chmod -R 777 /home/prism
COPY --from=build /prism /usr/local/bin/prism
COPY --from=build /hosts /opt/prism/hosts
COPY web /usr/share/prism/web
ENV HOME=/home/prism
WORKDIR /workspace
ENTRYPOINT ["prism"]
