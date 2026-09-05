package main

import "github.com/diegovillafuerte1/claudingtin/proto"

// Story 1.4 fills this in: read PORT, run the websocket upgrade + GET /status,
// and the channel-driven connection registry.
//
// The reference to proto below is deliberate: it makes the backend -> proto
// dependency edge real for scripts/check_deps.sh.
func main() {
	_ = proto.PROTOCOL_VERSION
}
