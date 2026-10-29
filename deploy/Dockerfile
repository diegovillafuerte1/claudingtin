# Backend image for claudingtin — builds and launches ./backend/cmd/serve only.
#
# Multi-stage: a Go builder compiles a static (CGO_ENABLED=0) binary against the
# workspace — the whole repo is copied so go.work is present and the `proto`
# sibling resolves in workspace mode — then a `scratch` runtime carries just the
# binary (plus CA roots for future outbound TLS).
#
# serve reads PORT (default 8080) and DB_PATH (the SQLite policy store; set
# here to /data, where a volume belongs), and serves GET /status and the /v1
# long-poll transport.

FROM golang:1.27 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/serve ./backend/cmd/serve
# scratch has no mkdir: the empty /data the store's file lives in comes from here.
RUN mkdir -p /out/data

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/serve /serve
COPY --from=build /out/data /data
ENV DB_PATH=/data/claudingtin.db
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/serve"]
