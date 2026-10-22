FROM golang:1.27 AS build
WORKDIR /src
COPY cli/go.mod cli/go.sum ./
RUN go mod download
COPY cli/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /prism .

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ripgrep curl ca-certificates python3 python3-venv python-is-python3 nodejs npm build-essential \
 && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /home/prism/.config/prism && chmod -R 777 /home/prism
COPY --from=build /prism /usr/local/bin/prism
ENV HOME=/home/prism
WORKDIR /workspace
ENTRYPOINT ["prism"]
