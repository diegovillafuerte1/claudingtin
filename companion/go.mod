module github.com/diegovillafuerte1/claudingtin/companion

go 1.27

require (
	github.com/diegovillafuerte1/claudingtin/proto v0.0.0
	github.com/fsnotify/fsnotify v1.10.1
)

require golang.org/x/sys v0.13.0 // indirect

replace github.com/diegovillafuerte1/claudingtin/proto => ../proto
