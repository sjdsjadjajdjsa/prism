#!/usr/bin/env python3
"""Recording pass-through proxy in front of the Prism router.

usage: proxy.py <listen-port> <upstream-base> <log.jsonl>

Every harness run talks to its own proxy instance, so each request can be
attributed to exactly one run. For every request it appends one JSON line:
time, method, path, status, the upstream route that served it (router's
x-free-route header), router wait, request size, response size, duration.
Streaming (SSE) responses are forwarded chunk by chunk.
"""
import http.client
import json
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

PORT = int(sys.argv[1])
UP = urlsplit(sys.argv[2])
LOG = sys.argv[3]
lock = threading.Lock()
HOP = {"connection", "keep-alive", "transfer-encoding", "content-length", "host", "proxy-connection", "te", "upgrade"}


def record(entry):
    with lock, open(LOG, "a") as f:
        f.write(json.dumps(entry) + "\n")


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def handle_any(self):
        start = time.time()
        body = self.rfile.read(int(self.headers.get("content-length") or 0))
        model = None
        if body:
            try:
                model = json.loads(body).get("model")
            except Exception:
                pass
        headers = {k: v for k, v in self.headers.items() if k.lower() not in HOP}
        entry = {"t": round(start, 3), "method": self.command, "path": self.path, "model": model, "req_bytes": len(body)}
        try:
            conn = http.client.HTTPConnection(UP.hostname, UP.port or 80, timeout=600)
            conn.request(self.command, self.path, body=body or None, headers=headers)
            resp = conn.getresponse()
        except Exception as e:
            entry.update(status=0, error=str(e)[:300], secs=round(time.time() - start, 2))
            record(entry)
            self.send_error(502, "proxy could not reach router")
            return
        self.send_response(resp.status)
        for k, v in resp.getheaders():
            if k.lower() not in HOP:
                self.send_header(k, v)
        self.send_header("Connection", "close")
        self.end_headers()
        size = 0
        err_body = b""
        try:
            while True:
                chunk = resp.read1(65536)
                if not chunk:
                    break
                size += len(chunk)
                if resp.status >= 400 and len(err_body) < 2000:
                    err_body += chunk
                self.wfile.write(chunk)
                self.wfile.flush()
        except Exception as e:
            entry["stream_error"] = str(e)[:200]
        self.close_connection = True
        entry.update(
            status=resp.status,
            route=resp.getheader("x-free-route"),
            router_wait_ms=resp.getheader("x-free-wait"),
            resp_bytes=size,
            secs=round(time.time() - start, 2),
        )
        if err_body:
            entry["error"] = err_body.decode("utf8", "replace")[:500]
        record(entry)

    do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = do_OPTIONS = do_HEAD = handle_any


ThreadingHTTPServer.daemon_threads = True
server = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)  # port 0 = pick a free one
print(server.server_address[1], flush=True)  # run.sh reads the chosen port from here
server.serve_forever()
