package main

import "github.com/diegovillafuerte1/claudingtin/proto"

// Story 1.6 fills this in: launch, tail the transcript, and speak ready/busy
// over the websocket.
//
// The reference to proto below is deliberate: it makes the companion -> proto
// dependency edge real for scripts/check_deps.sh.
func main() {
	_ = proto.PROTOCOL_VERSION
}
