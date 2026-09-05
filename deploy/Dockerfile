# Backend image for claudingtin — builds and launches ./backend/cmd/serve only.
#
# Multi-stage: a Go builder compiles a static (CGO_ENABLED=0) binary against the
# workspace — the whole repo is copied so go.work is present and the `proto`
# sibling resolves in workspace mode — then a `scratch` runtime carries just the
# binary (plus CA roots for future outbound TLS).
#
# serve is a stub in Epic 1; this image only needs it to compile and launch.
# Real serve logic (PORT, the websocket upgrade, GET /status) lands in Story 1.4.

FROM golang:1.27 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/serve ./backend/cmd/serve

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/serve /serve
EXPOSE 8080
ENTRYPOINT ["/serve"]
